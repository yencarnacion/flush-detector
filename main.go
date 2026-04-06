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
	"strings"
	"sync"
	"time"

	"flush-detector/internal/bars"
	"flush-detector/internal/config"
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
	Text      string `json:"text"`
	UpdatedAt string `json:"updated_at"`
	Symbols   int    `json:"symbols"`
	Alerts    int    `json:"alerts"`
}

type extraPayload struct {
	Ticker  string         `json:"ticker"`
	Date    string         `json:"date"`
	News    []news.Item    `json:"news"`
	Filings []filings.Item `json:"filings"`
}

type liveApplyRequest struct {
	Flush config.FlushConfig `json:"flush"`
	Alert config.AlertConfig `json:"alert"`
}

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
	tz := config.MustLocation(cfg.Timezone)
	watchlistPaths := watchlist.ParsePaths(watchlistsRaw)
	items, err := watchlist.Load(watchlistPaths)
	if err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	app := &App{
		log:            logger,
		tz:             tz,
		watchlistPaths: watchlistPaths,
		cfg:            cfg,
		watchlist:      items,
		watchIndex:     watchlist.ByTicker(items),
		barCh:          make(chan bars.Bar, 4096),
		hub:            webui.NewHub(logger, 200),
		store:          persistence.New(cfg.Persistence.StateFile),
		alertLog:       persistence.NewAlertCSVLogger("log"),
		detector:       flush.NewDetector(cfg.Flush, cfg.Alert.CooldownSeconds, tz),
	}

	if state, err := app.store.Load(); err == nil {
		app.hub.SetHistory(state.Alerts)
	} else {
		logger.Warn("load state", "error", err)
	}

	app.hub.SetConfig(cfg)
	app.hub.SetWatchlist(items)
	app.setStatus("starting")

	massiveClient, err := massive.New(apiKey, logger, app.setStatus)
	if err != nil {
		panic(err)
	}
	app.massive = massiveClient
	app.news = news.New(massiveClient.REST(), cfg.News, logger, 4)
	app.filings = filings.New(massiveClient.REST(), cfg.Filings, logger, 4)

	go app.runDetector(ctx)

	app.setStatus("warming up")
	app.warmup(ctx, watchlist.Symbols(items))

	if err := app.massive.Connect(ctx, watchlist.Symbols(items), app.barCh); err != nil {
		panic(err)
	}
	app.setStatus("live")

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
	mux.HandleFunc("GET /ws", a.hub.HandleWS)

	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/config", a.handleConfig)
	mux.HandleFunc("POST /api/config/apply", a.handleApplyLive)
	mux.HandleFunc("GET /api/watchlist", a.handleWatchlist)
	mux.HandleFunc("POST /api/watchlist/reload", a.handleWatchlistReload)
	mux.HandleFunc("GET /api/history", a.handleHistory)
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
			meta, ok := a.lookupSymbol(bar.Symbol)
			if !ok {
				continue
			}
			alert := a.detector.Process(flush.SymbolMeta{
				Symbol:  meta.Symbol,
				Name:    meta.Name,
				Sources: meta.Sources,
			}, bar)
			if alert == nil {
				continue
			}
			a.hub.AddAlert(*alert)
			if err := a.alertLog.Append(*alert); err != nil {
				a.log.Warn("append alert csv", "error", err, "symbol", alert.Symbol, "alert_time", alert.AlertTime.Format(time.RFC3339))
			}
			if err := a.store.SaveAlerts(a.hub.History()); err != nil {
				a.log.Warn("save state", "error", err)
			}
			a.setStatus("live")
		}
	}
}

func (a *App) warmup(ctx context.Context, symbols []string) {
	cfg := a.currentConfig()
	nowET := time.Now().In(a.tz)
	sessionStart := flush.SessionWindow(strings.ToLower(cfg.Flush.Session), nowET)
	var from time.Time
	var to time.Time
	if nowET.Before(sessionStart) {
		day := nowET.AddDate(0, 0, -1)
		from = flush.SessionWindow(strings.ToLower(cfg.Flush.Session), day)
		to = from.Add(12 * time.Hour)
	} else {
		from = sessionStart
		to = nowET
	}
	limit := max(120, cfg.Flush.BackfillBars+cfg.Flush.WarmupLookbackBars+cfg.Flush.MinBarsBeforeAlerts+60)

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
	go a.warmup(context.Background(), watchlist.Symbols(items))

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
	a.hub.SetStatus(statusPayload{
		Text:      text,
		UpdatedAt: time.Now().In(a.tz).Format(time.RFC3339),
		Symbols:   len(a.currentWatchlist()),
		Alerts:    len(a.hub.History()),
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
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
