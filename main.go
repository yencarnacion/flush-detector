package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"flush-detector/internal/bars"
	"flush-detector/internal/config"
	"flush-detector/internal/dashboard"
	"flush-detector/internal/filings"
	"flush-detector/internal/flush"
	"flush-detector/internal/massive"
	"flush-detector/internal/news"
	"flush-detector/internal/persistence"
	"flush-detector/internal/watchlist"
	"flush-detector/internal/webui"

	"github.com/joho/godotenv"
)

type App struct {
	log            *slog.Logger
	tz             *time.Location
	watchlistPaths []string

	mu         sync.RWMutex
	cfg        config.Config
	watchlist  []watchlist.Symbol
	watchIndex map[string]watchlist.Symbol

	processMu      sync.Mutex
	replayStateMu  sync.Mutex
	replaying      bool
	historicalMode bool
	replayDate     string
	livePausedAt   time.Time

	calendarMu          sync.Mutex
	replayCalendarCache map[string]replayCalendarCacheEntry
	gapperMu            sync.RWMutex
	gapperState         gapperSnapshot

	barCh    chan bars.Bar
	hub      *webui.Hub
	store    *persistence.Store
	alertLog *persistence.AlertCSVLogger
	detector *flush.Detector
	massive  *massive.Client
	news     *news.Service
	filings  *filings.Service
}

type statusPayload struct {
	Text       string `json:"text"`
	UpdatedAt  string `json:"updated_at"`
	Symbols    int    `json:"symbols"`
	Alerts     int    `json:"alerts"`
	Mode       string `json:"mode"`
	ReplayDate string `json:"replay_date,omitempty"`
	Replaying  bool   `json:"replaying"`
}

type extraPayload struct {
	Ticker  string         `json:"ticker"`
	Date    string         `json:"date"`
	News    []news.Item    `json:"news"`
	Filings []filings.Item `json:"filings"`
}

type liveApplyRequest struct {
	Flush  config.FlushConfig   `json:"flush"`
	Alert  config.AlertConfig   `json:"alert"`
	Gapper *config.GapperConfig `json:"gapper"`
}

type dashboardGenerateRequest struct {
	Date string `json:"date"`
}

const (
	startupWarmupTimeout = 20 * time.Second
	reloadWarmupTimeout  = 20 * time.Second
	resumeWarmupTimeout  = 30 * time.Second
	resumeRebuildTimeout = 15 * time.Minute
)

func main() {
	_ = godotenv.Load()

	var configPath string
	var watchlistsRaw string
	flag.StringVar(&configPath, "config", "config.yaml", "path to config.yaml")
	flag.StringVar(&watchlistsRaw, "watchlists", "watchlist.yaml", "comma separated watchlist yaml files")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		panic(err)
	}
	apiKey := config.APIKeyFromEnv()
	if apiKey == "" {
		panic("MASSIVE_API_KEY is required")
	}

	logger := newLogger(cfg.Logging.Level)
	if err := resetCacheDir("cache"); err != nil {
		panic(err)
	}
	tz := config.MustLocation(cfg.Timezone)
	watchlistPaths := watchlist.ParsePaths(watchlistsRaw)
	items, err := watchlist.Load(watchlistPaths)
	if err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	app := &App{
		log:                 logger,
		tz:                  tz,
		watchlistPaths:      watchlistPaths,
		cfg:                 cfg,
		watchlist:           items,
		watchIndex:          watchlist.ByTicker(items),
		barCh:               make(chan bars.Bar, 4096),
		hub:                 webui.NewHub(logger, 200),
		store:               persistence.New(cfg.Persistence.StateFile),
		alertLog:            persistence.NewAlertCSVLogger("log", tz),
		detector:            flush.NewDetector(cfg.Flush, cfg.Alert.CooldownSeconds, tz),
		replayCalendarCache: make(map[string]replayCalendarCacheEntry),
	}

	if state, err := app.store.Load(); err == nil {
		app.hub.SetHistory(state.Alerts)
	} else {
		logger.Warn("load state", "error", err)
	}

	app.hub.SetConfig(cfg)
	app.hub.SetWatchlist(items)
	app.clearGapperAnalysis()
	app.setStatus("starting")

	massiveClient, err := massive.New(apiKey, logger, app.setStatus)
	if err != nil {
		panic(err)
	}
	app.massive = massiveClient
	app.news = news.New(massiveClient.REST(), cfg.News, logger, 4)
	app.filings = filings.New(massiveClient.REST(), cfg.Filings, logger, 4)
	app.startLiveGapperAnalysis("startup")

	go app.runDetector(ctx)

	if err := app.massive.Connect(ctx, watchlist.Symbols(items), app.barCh); err != nil {
		panic(err)
	}
	app.setStatus("live")
	app.startWarmup("startup", startupWarmupTimeout, watchlist.Symbols(items))

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		app.massive.Close()
	}()

	logger.Info("flush-detector listening", "port", cfg.ServerPort)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func newLogger(level string) *slog.Logger {
	lvl := new(slog.LevelVar)
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl.Set(slog.LevelDebug)
	case "warn":
		lvl.Set(slog.LevelWarn)
	case "error":
		lvl.Set(slog.LevelError)
	default:
		lvl.Set(slog.LevelInfo)
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func resetCacheDir(path string) error {
	if strings.TrimSpace(path) == "" {
		path = "cache"
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "index.html"))
	})
	mux.HandleFunc("GET /news.html", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "news.html"))
	})
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "app.js"))
	})
	mux.HandleFunc("GET /styles.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "styles.css"))
	})
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", http.FileServer(http.Dir("dashboard"))))
	mux.HandleFunc("GET /ws", a.hub.HandleWS)

	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/config", a.handleConfig)
	mux.HandleFunc("POST /api/config/apply", a.handleApplyLive)
	mux.HandleFunc("GET /api/watchlist", a.handleWatchlist)
	mux.HandleFunc("POST /api/watchlist/reload", a.handleWatchlistReload)
	mux.HandleFunc("GET /api/gappers", a.handleGappers)
	mux.HandleFunc("GET /api/history", a.handleHistory)
	mux.HandleFunc("GET /api/replay-calendar", a.handleReplayCalendar)
	mux.HandleFunc("POST /api/replay-day", a.handleReplayDay)
	mux.HandleFunc("POST /api/replay-live", a.handleReplayLive)
	mux.HandleFunc("POST /api/dashboard/generate", a.handleGenerateDashboard)
	mux.HandleFunc("GET /api/extra", a.handleExtra)

	mux.HandleFunc("GET /alert.wav", a.soundHandler(func(cfg config.Config) string { return cfg.Alert.SoundFile }, 660))
	mux.HandleFunc("GET /alert-up.wav", a.soundHandler(func(cfg config.Config) string { return cfg.Alert.UpSoundFile }, 880))
	mux.HandleFunc("GET /alert-down.wav", a.soundHandler(func(cfg config.Config) string { return cfg.Alert.DownSoundFile }, 440))
	mux.HandleFunc("GET /flush.wav", a.soundHandler(func(cfg config.Config) string { return cfg.Alert.FlushSoundFile }, 720))

	return mux
}

func (a *App) runDetector(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case bar := <-a.barCh:
			a.handleLiveBar(bar)
		}
	}
}

func (a *App) warmup(ctx context.Context, symbols []string) {
	a.warmupUntil(ctx, symbols, time.Now())
}

func (a *App) warmupUntil(ctx context.Context, symbols []string, asOf time.Time) {
	cfg := a.currentConfig()
	nowET := asOf.In(a.tz)
	from := flush.VolumeWindowStart(nowET)
	to := nowET
	if to.Before(from) {
		return
	}
	limit := max(
		120,
		max(
			cfg.Flush.BackfillBars+cfg.Flush.WarmupLookbackBars+cfg.Flush.MinBarsBeforeAlerts+60,
			int(to.Sub(from)/time.Minute)+5,
		),
	)

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, symbol := range symbols {
		symbol := symbol
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			barsList, err := a.massive.BackfillBars(ctx, symbol, from, to, limit)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				a.log.Warn("warmup backfill failed", "symbol", symbol, "error", err)
				return
			}
			meta, ok := a.lookupSymbol(symbol)
			if !ok {
				return
			}
			for _, bar := range barsList {
				a.detector.Seed(flush.SymbolMeta{
					Symbol:  meta.Symbol,
					Name:    meta.Name,
					Sources: meta.Sources,
				}, bar)
			}
		}()
	}
	wg.Wait()
}

func (a *App) rebuildLiveHistory(ctx context.Context, symbols []string, to time.Time) []flush.Alert {
	from := flush.VolumeWindowStart(to.In(a.tz))
	if to.Before(from) {
		return nil
	}

	allBars := a.backfillDayBars(ctx, symbols, from, to)
	if ctx.Err() != nil {
		return nil
	}

	alerts := make([]flush.Alert, 0)
	seen := make(map[string]struct{})
	a.processMu.Lock()
	defer a.processMu.Unlock()
	for _, bar := range allBars {
		meta, ok := a.lookupSymbol(bar.Symbol)
		if !ok {
			continue
		}
		alert := a.evaluateBarLocked(symbolMeta(meta), bar)
		if alert == nil {
			continue
		}
		if _, ok := seen[alert.ID]; ok {
			continue
		}
		seen[alert.ID] = struct{}{}
		alerts = append(alerts, *alert)
	}
	return alerts
}

func (a *App) startWarmup(reason string, timeout time.Duration, symbols []string) {
	if len(symbols) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		a.warmup(ctx, symbols)
		if err := ctx.Err(); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			a.log.Warn("warmup ended", "reason", reason, "error", err)
		}
	}()
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"symbols": len(a.currentWatchlist()),
		"alerts":  len(a.hub.History()),
	})
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.currentConfig())
}

func (a *App) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"watchlist": a.currentWatchlist()})
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"alerts": a.hub.History()})
}

func (a *App) handleReplayCalendar(w http.ResponseWriter, r *http.Request) {
	month, err := parseReplayMonth(r.URL.Query().Get("month"), a.tz, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	payload, err := a.replayCalendar(ctx, month)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) handleReplayDay(w http.ResponseWriter, r *http.Request) {
	var req replayDayRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	day, err := parseReplayDate(req.Date, a.tz, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if !a.beginHistoricalReplay(day) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "replay already running"})
		return
	}

	go a.replayDay(day)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":          true,
		"status":      "replay_day_started",
		"replay_date": replayDateKey(day, a.tz),
	})
}

func (a *App) handleReplayLive(w http.ResponseWriter, r *http.Request) {
	state := a.snapshotReplayState()
	if !state.historicalMode && !state.replaying {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "already_live"})
		return
	}
	if !a.beginResumeLive() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "replay already running"})
		return
	}

	go a.resumeLive()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": "resume_live_started"})
}

func (a *App) handleGenerateDashboard(w http.ResponseWriter, r *http.Request) {
	state := a.snapshotReplayState()
	if state.replaying {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "replay already running"})
		return
	}

	var req dashboardGenerateRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	var day time.Time
	var err error
	switch {
	case strings.TrimSpace(req.Date) != "":
		day, err = parseReplayDate(req.Date, a.tz, time.Now())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	case state.historicalMode && state.replayDate != "":
		day, err = time.ParseInLocation("2006-01-02", state.replayDate, a.tz)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to parse replay date"})
			return
		}
	default:
		day = normalizeReplayDay(time.Now(), a.tz)
	}

	dateCompact := day.In(a.tz).Format("20060102")
	dateKey := replayDateKey(day, a.tz)
	csvPath := filepath.Join("log", fmt.Sprintf("alerts_%s.csv", dateCompact))
	if _, err := os.Stat(csvPath); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("missing alert log for %s", dateKey)})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	dashboardFilename := fmt.Sprintf("dashboard_%s.html", dateCompact)
	dashboardPath := filepath.Join("dashboard", dashboardFilename)
	cfg := a.currentConfig()
	dashboardInputPath := csvPath
	if cfg.Gapper.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), gapperAnalysisTimeout)
		defer cancel()
		asOf := time.Date(day.In(a.tz).Year(), day.In(a.tz).Month(), day.In(a.tz).Day(), 20, 0, 0, 0, a.tz)
		if replayDateKey(day, a.tz) == replayDateKey(time.Now(), a.tz) {
			asOf = time.Now().In(a.tz)
		}
		if err := a.ensureGapperAnalysisForDay(ctx, day, asOf, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		filteredPath := filepath.Join("tmp", fmt.Sprintf("gapper_alerts_%s.csv", dateCompact))
		count, err := filterAlertCSVBySymbols(csvPath, filteredPath, a.gapperSymbolSetForDay(day))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if count == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("no gapper alerts found for %s", dateKey)})
			return
		}
		dashboardInputPath = filteredPath
	}
	result, err := dashboard.GenerateDashboardWithSessionStart(dashboardInputPath, dashboardPath, cfg.UI.ChartOpenerBaseURL, cfg.Flush.StartTime)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	signalCSVFilename := strings.TrimSuffix(dashboardFilename, ".html") + "_polygon_signals.csv"
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"date":           dateKey,
		"alerts":         result.AlertCount,
		"dashboard_file": dashboardFilename,
		"dashboard_url":  "/dashboard/" + dashboardFilename,
		"signal_csv_url": "/dashboard/" + signalCSVFilename,
	})
}

func (a *App) handleWatchlistReload(w http.ResponseWriter, r *http.Request) {
	items, err := watchlist.Load(a.watchlistPaths)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	a.mu.Lock()
	a.watchlist = items
	a.watchIndex = watchlist.ByTicker(items)
	a.mu.Unlock()
	a.invalidateReplayCalendarCache()

	valid := make(map[string]struct{}, len(items))
	for _, item := range items {
		valid[item.Symbol] = struct{}{}
	}
	a.detector.ResetUnknownSymbols(valid)

	if err := a.massive.SyncSubscriptions(watchlist.Symbols(items)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	a.hub.SetWatchlist(items)
	a.setStatus("watchlist reloaded")
	a.startWarmup("watchlist reload", reloadWarmupTimeout, watchlist.Symbols(items))
	a.startLiveGapperAnalysis("watchlist reload")

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "watchlist": items})
}

func (a *App) handleApplyLive(w http.ResponseWriter, r *http.Request) {
	var req liveApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	cfg := a.currentConfig()
	cfg.Flush = req.Flush
	if req.Gapper != nil {
		cfg.Gapper = *req.Gapper
	}
	cfg.Alert.EnableSound = req.Alert.EnableSound
	cfg.Alert.CooldownSeconds = req.Alert.CooldownSeconds
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	a.detector.UpdateConfig(cfg.Flush, cfg.Alert.CooldownSeconds)
	a.hub.SetConfig(cfg)
	a.setStatus("live settings applied")
	a.startLiveGapperAnalysis("settings applied")
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleExtra(w http.ResponseWriter, r *http.Request) {
	ticker := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("ticker")))
	dateStr := strings.TrimSpace(r.URL.Query().Get("date"))
	days := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		fmt.Sscanf(raw, "%d", &days)
	}
	if ticker == "" || dateStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ticker and date are required"})
		return
	}
	date, err := time.ParseInLocation("2006-01-02", dateStr, a.tz)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	payload := extraPayload{
		Ticker:  ticker,
		Date:    dateStr,
		News:    []news.Item{},
		Filings: []filings.Item{},
	}

	var wg sync.WaitGroup
	var newsErr error
	var filingsErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		payload.News, newsErr = a.news.Get(ctx, ticker, date, days)
	}()
	go func() {
		defer wg.Done()
		payload.Filings, filingsErr = a.filings.Get(ctx, ticker, date, days)
	}()
	wg.Wait()

	if newsErr != nil {
		a.log.Warn("news enrichment failed", "ticker", ticker, "error", newsErr)
	}
	if filingsErr != nil {
		a.log.Warn("filings enrichment failed", "ticker", ticker, "error", filingsErr)
	}

	writeJSON(w, http.StatusOK, payload)
}

func (a *App) soundHandler(pathFn func(config.Config) string, freq float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := a.currentConfig()
		path := pathFn(cfg)
		w.Header().Set("Content-Type", "audio/wav")
		if path != "" {
			file, err := os.Open(path)
			if err == nil {
				defer file.Close()
				if _, err := io.Copy(w, file); err == nil {
					return
				}
			}
		}
		_, _ = w.Write(synthBeepWAV(freq, 180*time.Millisecond))
	}
}

func (a *App) currentConfig() config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *App) currentWatchlist() []watchlist.Symbol {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]watchlist.Symbol, len(a.watchlist))
	copy(out, a.watchlist)
	return out
}

func (a *App) lookupSymbol(symbol string) (watchlist.Symbol, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	item, ok := a.watchIndex[strings.ToUpper(symbol)]
	return item, ok
}

func (a *App) setStatus(text string) {
	state := a.snapshotReplayState()
	mode := "live"
	if state.historicalMode {
		mode = "historical"
	}
	a.hub.SetStatus(statusPayload{
		Text:       text,
		UpdatedAt:  time.Now().In(a.tz).Format(time.RFC3339),
		Symbols:    len(a.currentWatchlist()),
		Alerts:     len(a.hub.History()),
		Mode:       mode,
		ReplayDate: state.replayDate,
		Replaying:  state.replaying,
	})
}

func (a *App) handleLiveBar(bar bars.Bar) {
	meta, ok := a.lookupSymbol(bar.Symbol)
	if !ok {
		return
	}
	if a.suppressLiveProcessing() {
		return
	}
	a.observeLiveGapperBar(meta, bar)

	a.processMu.Lock()
	defer a.processMu.Unlock()
	if a.processBarLocked(symbolMeta(meta), bar, true) {
		a.setStatus("live")
	}
}

func (a *App) replayDay(day time.Time) {
	from, to := replayDayRange(day, time.Now(), a.tz)
	a.setStatus("historical replay loading")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	allBars := a.backfillDayBars(ctx, watchlist.Symbols(a.currentWatchlist()), from, to)
	if ctx.Err() != nil {
		a.log.Error("historical replay backfill context", "error", ctx.Err(), "replay_date", replayDateKey(day, a.tz))
		a.failHistoricalReplay()
		a.setStatus("historical replay failed")
		return
	}
	if len(allBars) == 0 {
		a.failHistoricalReplay()
		a.setStatus("historical replay unavailable")
		return
	}

	cfg := a.currentConfig()
	if cfg.Gapper.Enabled {
		if err := a.ensureGapperAnalysisForDay(ctx, day, to, cfg); err != nil {
			a.log.Error("historical gapper analysis", "error", err, "replay_date", replayDateKey(day, a.tz))
			a.failHistoricalReplay()
			a.setStatus("historical replay failed")
			return
		}
	}
	a.processMu.Lock()
	defer a.processMu.Unlock()
	if err := a.resetHistoricalReplayLocked(day, cfg); err != nil {
		a.log.Error("reset historical replay state", "error", err, "replay_date", replayDateKey(day, a.tz))
		a.failHistoricalReplay()
		a.setStatus("historical replay failed")
		return
	}

	a.setStatus("historical replay processing")
	for _, bar := range allBars {
		meta, ok := a.lookupSymbol(bar.Symbol)
		if !ok {
			continue
		}
		a.processBarLocked(symbolMeta(meta), bar, false)
	}
	a.finishHistoricalReplay(day)
	a.setStatus("historical replay ready")
}

func (a *App) resumeLive() {
	a.setStatus("resuming live")

	replayState := a.snapshotReplayState()

	cfg := a.currentConfig()
	symbols := watchlist.Symbols(a.currentWatchlist())
	now := time.Now().In(a.tz)

	a.processMu.Lock()
	a.detector.Reset(cfg.Flush, cfg.Alert.CooldownSeconds)
	a.processMu.Unlock()

	if replayState.livePausedAt.IsZero() {
		ctx, cancel := context.WithTimeout(context.Background(), resumeWarmupTimeout)
		defer cancel()

		state, err := a.store.Load()
		if err != nil {
			a.log.Warn("load live state during resume", "error", err)
		}
		a.hub.ReplaceHistory(state.Alerts)
		a.warmup(ctx, symbols)
		if err := ctx.Err(); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
			a.log.Warn("live warmup during resume", "error", ctx.Err())
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), resumeRebuildTimeout)
		defer cancel()

		a.setStatus("rebuilding live alerts")
		if cfg.Gapper.Enabled {
			if err := a.ensureGapperAnalysisForDay(ctx, normalizeReplayDay(now, a.tz), now, cfg); err != nil {
				a.log.Warn("live gapper analysis during resume rebuild", "error", err)
			}
		}
		alerts := a.rebuildLiveHistory(ctx, symbols, now)
		if ctx.Err() == nil {
			a.hub.ReplaceHistory(alerts)
			if err := a.store.SaveAlerts(alerts); err != nil {
				a.log.Warn("save rebuilt live state", "error", err)
			}
			if err := a.replaceAlertLogForDay(now, alerts); err != nil {
				a.log.Warn("replace live alert csv during resume", "error", err)
			}
		}
		if err := ctx.Err(); err != nil && err != context.Canceled {
			a.log.Warn("live alert rebuild during resume ended before completion", "error", err)
		}
	}

	a.finishResumeLive()
	a.startLiveGapperAnalysis("resume live")
	a.setStatus("live")
}

func (a *App) processBarLocked(meta flush.SymbolMeta, bar bars.Bar, persistState bool) bool {
	alert := a.evaluateBarLocked(meta, bar)
	if alert == nil {
		return false
	}
	if a.alertInHistory(alert.ID) {
		return false
	}

	a.hub.AddAlert(*alert)
	if err := a.alertLog.Append(*alert); err != nil {
		a.log.Warn("append alert csv", "error", err, "symbol", alert.Symbol, "alert_time", alert.AlertTime.Format(time.RFC3339))
	}
	if persistState {
		if err := a.store.SaveAlerts(a.hub.History()); err != nil {
			a.log.Warn("save state", "error", err)
		}
	}
	return true
}

func (a *App) evaluateBarLocked(meta flush.SymbolMeta, bar bars.Bar) *flush.Alert {
	if !a.gapperAllowsSignal(meta.Symbol, bar.End) {
		a.detector.Seed(meta, bar)
		return nil
	}

	alert := a.detector.Process(meta, bar)
	if alert == nil {
		return nil
	}
	if gap, ok := a.gapperGapForSignal(meta.Symbol, bar.End); ok {
		alert.GapPercent = gap
	}
	return alert
}

func (a *App) alertInHistory(id string) bool {
	for _, existing := range a.hub.History() {
		if existing.ID == id {
			return true
		}
	}
	return false
}

func (a *App) replaceAlertLogForDay(day time.Time, alerts []flush.Alert) error {
	if err := a.alertLog.DeleteDay(day); err != nil {
		return err
	}
	for _, alert := range alerts {
		if !normalizeReplayDay(alert.AlertTime, a.tz).Equal(normalizeReplayDay(day, a.tz)) {
			continue
		}
		if err := a.alertLog.Append(alert); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) backfillDayBars(ctx context.Context, symbols []string, from, to time.Time) []bars.Bar {
	limit := max(120, int(to.Sub(from)/time.Minute)+5)
	sem := make(chan struct{}, 4)
	results := make(chan []bars.Bar, len(symbols))

	var wg sync.WaitGroup
	for _, symbol := range symbols {
		symbol := symbol
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			barsList, err := a.massive.BackfillBars(ctx, symbol, from, to, limit)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				a.log.Warn("replay day backfill failed", "symbol", symbol, "error", err)
				return
			}
			results <- barsList
		}()
	}

	wg.Wait()
	close(results)

	allBars := make([]bars.Bar, 0, len(symbols)*limit)
	for barsList := range results {
		for _, bar := range barsList {
			if bar.End.Before(from) || bar.End.After(to) {
				continue
			}
			allBars = append(allBars, bar)
		}
	}
	sortBarsChronological(allBars)
	return allBars
}

func sortBarsChronological(allBars []bars.Bar) {
	sort.Slice(allBars, func(i, j int) bool {
		iEnd := allBars[i].End.UnixMilli()
		jEnd := allBars[j].End.UnixMilli()
		if iEnd != jEnd {
			return iEnd < jEnd
		}
		if allBars[i].Symbol != allBars[j].Symbol {
			return allBars[i].Symbol < allBars[j].Symbol
		}
		return allBars[i].Start.UnixMilli() < allBars[j].Start.UnixMilli()
	})
}

func symbolMeta(item watchlist.Symbol) flush.SymbolMeta {
	return flush.SymbolMeta{
		Symbol:  item.Symbol,
		Name:    item.Name,
		Sources: item.Sources,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

func max(values ...int) int {
	best := 0
	for _, v := range values {
		if v > best {
			best = v
		}
	}
	return best
}

func synthBeepWAV(freq float64, dur time.Duration) []byte {
	const sampleRate = 44100
	const bitsPerSample = 16
	const channels = 1

	samples := int(float64(sampleRate) * dur.Seconds())
	dataSize := samples * channels * bitsPerSample / 8
	buf := &bytes.Buffer{}

	writeString := func(v string) {
		_, _ = buf.WriteString(v)
	}
	writeU32 := func(v uint32) {
		_ = binary.Write(buf, binary.LittleEndian, v)
	}
	writeU16 := func(v uint16) {
		_ = binary.Write(buf, binary.LittleEndian, v)
	}

	writeString("RIFF")
	writeU32(uint32(36 + dataSize))
	writeString("WAVE")
	writeString("fmt ")
	writeU32(16)
	writeU16(1)
	writeU16(channels)
	writeU32(sampleRate)
	writeU32(sampleRate * channels * bitsPerSample / 8)
	writeU16(channels * bitsPerSample / 8)
	writeU16(bitsPerSample)
	writeString("data")
	writeU32(uint32(dataSize))

	for i := 0; i < samples; i++ {
		t := float64(i) / sampleRate
		amp := math.Sin(2*math.Pi*freq*t) * 0.28
		env := math.Min(1, float64(i)/300.0) * math.Min(1, float64(samples-i)/500.0)
		sample := int16(amp * env * math.MaxInt16)
		_ = binary.Write(buf, binary.LittleEndian, sample)
	}
	return buf.Bytes()
}
