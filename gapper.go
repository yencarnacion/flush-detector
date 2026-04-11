package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"flush-detector/internal/bars"
	"flush-detector/internal/config"
	"flush-detector/internal/gappers"
	"flush-detector/internal/watchlist"
)

const gapperAnalysisTimeout = 15 * time.Minute

type gapperSnapshot struct {
	Enabled    bool
	Pending    bool
	GapPercent float64
	TargetDate string
	UpdatedAt  time.Time
	Note       string
	Failed     bool
	Results    []gappers.Result
	Skips      []gappers.Skip
	Records    map[string]gappers.Result
}

type gapperPayload struct {
	Enabled    bool             `json:"enabled"`
	Pending    bool             `json:"pending"`
	GapPercent float64          `json:"gap_percent"`
	TargetDate string           `json:"target_date"`
	UpdatedAt  string           `json:"updated_at,omitempty"`
	Note       string           `json:"note,omitempty"`
	Failed     bool             `json:"failed,omitempty"`
	Count      int              `json:"count"`
	Results    []gappers.Result `json:"results"`
	Skips      []gappers.Skip   `json:"skips,omitempty"`
}

func (a *App) handleGappers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.currentGapperPayload())
}

func (a *App) startLiveGapperAnalysis(reason string) {
	cfg := a.currentConfig()
	if !cfg.Gapper.Enabled {
		a.clearGapperAnalysis()
		return
	}
	if a.massive == nil {
		return
	}

	now := time.Now().In(a.tz)
	day := normalizeReplayDay(now, a.tz)
	a.setGapperPending(day, reason)

	go func(cfg config.Config, day, asOf time.Time) {
		ctx, cancel := context.WithTimeout(context.Background(), gapperAnalysisTimeout)
		defer cancel()

		records, results, skips, err := a.scanGappers(ctx, day, asOf, cfg)
		if err != nil {
			a.log.Warn("gapper analysis failed", "reason", reason, "error", err)
			a.setGapperError(day, err)
			return
		}
		a.replaceGapperAnalysis(cfg, day, records, results, skips, reason)
	}(cfg, day, now)
}

func (a *App) ensureGapperAnalysisForDay(ctx context.Context, day, asOf time.Time, cfg config.Config) error {
	if !cfg.Gapper.Enabled {
		return nil
	}

	dayKey := replayDateKey(day, a.tz)
	a.gapperMu.RLock()
	current := a.gapperState
	ready := !current.Pending && !current.Failed && current.TargetDate == dayKey && current.GapPercent == cfg.Gapper.GapPercent
	a.gapperMu.RUnlock()
	if ready {
		return nil
	}

	a.setGapperPending(day, "gapper scan")
	records, results, skips, err := a.scanGappers(ctx, day, asOf, cfg)
	if err != nil {
		a.setGapperError(day, err)
		return err
	}
	a.replaceGapperAnalysis(cfg, day, records, results, skips, "gapper scan")
	return nil
}

func (a *App) scanGappers(ctx context.Context, day, asOf time.Time, cfg config.Config) (map[string]gappers.Result, []gappers.Result, []gappers.Skip, error) {
	items := a.currentWatchlist()
	records := make(map[string]gappers.Result, len(items))
	if len(items) == 0 {
		return records, nil, nil, nil
	}

	from := normalizeReplayDay(day, a.tz).AddDate(0, 0, -cfg.Gapper.LookbackDays)
	if asOf.IsZero() {
		asOf = time.Date(day.In(a.tz).Year(), day.In(a.tz).Month(), day.In(a.tz).Day(), 20, 0, 0, 0, a.tz)
	}
	limit := 50000

	type result struct {
		record  gappers.Result
		include bool
		skip    gappers.Skip
	}

	sem := make(chan struct{}, 4)
	out := make(chan result, len(items))
	var wg sync.WaitGroup
	for _, item := range items {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			barsList, err := a.massive.BackfillBars(ctx, item.Symbol, from, asOf, limit)
			if err != nil {
				out <- result{skip: gappers.Skip{Symbol: item.Symbol, Reason: err.Error()}}
				return
			}
			gapperBars := make([]gappers.Bar, 0, len(barsList))
			for _, bar := range barsList {
				gapperBars = append(gapperBars, gappers.Bar{
					Time:   bar.Start,
					Open:   bar.Open,
					High:   bar.High,
					Low:    bar.Low,
					Close:  bar.Close,
					Volume: bar.Volume,
				})
			}
			record, include, skip := gappers.EvaluateSymbolAsOf(item.Symbol, item.Name, gapperBars, day, asOf, a.tz, cfg.Gapper.GapPercent)
			out <- result{record: record, include: include, skip: skip}
		}()
	}

	wg.Wait()
	close(out)

	results := make([]gappers.Result, 0, len(items))
	skips := make([]gappers.Skip, 0)
	for row := range out {
		if row.skip.Reason != "" {
			skips = append(skips, row.skip)
			continue
		}
		if row.record.Symbol == "" {
			continue
		}
		records[row.record.Symbol] = row.record
		if row.include && row.record.HasPrice {
			results = append(results, row.record)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}

	gappers.SortByGapDesc(results)
	slices.SortFunc(skips, func(a, b gappers.Skip) int { return strings.Compare(a.Symbol, b.Symbol) })
	return records, results, skips, nil
}

func (a *App) observeLiveGapperBar(meta watchlist.Symbol, bar bars.Bar) {
	cfg := a.currentConfig()
	if !cfg.Gapper.Enabled {
		return
	}

	updated := false
	a.gapperMu.Lock()
	if a.gapperState.TargetDate == bar.Start.In(a.tz).Format("2006-01-02") && !a.gapperState.Pending && a.gapperState.Records != nil {
		record, ok := a.gapperState.Records[meta.Symbol]
		if ok {
			next, _, changed := gappers.UpdateResultWithLiveBar(record, gappers.Bar{
				Time:   bar.Start,
				Open:   bar.Open,
				High:   bar.High,
				Low:    bar.Low,
				Close:  bar.Close,
				Volume: bar.Volume,
			}, a.tz, cfg.Gapper.GapPercent)
			if changed {
				if next.Name == "" {
					next.Name = meta.Name
				}
				a.gapperState.Records[meta.Symbol] = next
				a.gapperState.Results = includedGappers(a.gapperState.Records, cfg.Gapper.GapPercent)
				a.gapperState.UpdatedAt = time.Now().In(a.tz)
				a.gapperState.Note = "live"
				updated = true
			}
		}
	}
	payload := a.gapperPayloadLocked()
	a.gapperMu.Unlock()

	if updated {
		a.hub.SetGappers(payload)
	}
}

func includedGappers(records map[string]gappers.Result, threshold float64) []gappers.Result {
	out := make([]gappers.Result, 0, len(records))
	for _, record := range records {
		if record.HasPrice && gappers.PassesThreshold(record.GapPercent, threshold) {
			out = append(out, record)
		}
	}
	gappers.SortByGapDesc(out)
	return out
}

func (a *App) gapperAllowsSignal(symbol string, at time.Time) bool {
	cfg := a.currentConfig()
	if !cfg.Gapper.Enabled {
		return true
	}

	dayKey := at.In(a.tz).Format("2006-01-02")
	a.gapperMu.RLock()
	defer a.gapperMu.RUnlock()
	if a.gapperState.Pending || a.gapperState.TargetDate != dayKey {
		return false
	}
	if a.gapperState.Records == nil {
		return false
	}
	record, ok := a.gapperState.Records[strings.ToUpper(strings.TrimSpace(symbol))]
	return ok && record.HasPrice && gappers.PassesThreshold(record.GapPercent, cfg.Gapper.GapPercent)
}

func (a *App) gapperGapForSignal(symbol string, at time.Time) (float64, bool) {
	cfg := a.currentConfig()
	if !cfg.Gapper.Enabled {
		return 0, false
	}

	dayKey := at.In(a.tz).Format("2006-01-02")
	a.gapperMu.RLock()
	defer a.gapperMu.RUnlock()
	if a.gapperState.Pending || a.gapperState.TargetDate != dayKey || a.gapperState.Records == nil {
		return 0, false
	}
	record, ok := a.gapperState.Records[strings.ToUpper(strings.TrimSpace(symbol))]
	if !ok || !record.HasPrice || !gappers.PassesThreshold(record.GapPercent, cfg.Gapper.GapPercent) {
		return 0, false
	}
	return record.GapPercent, true
}

func (a *App) gapperSymbolSetForDay(day time.Time) map[string]struct{} {
	dayKey := replayDateKey(day, a.tz)
	out := make(map[string]struct{})
	a.gapperMu.RLock()
	defer a.gapperMu.RUnlock()
	if a.gapperState.TargetDate != dayKey {
		return out
	}
	for _, result := range a.gapperState.Results {
		out[result.Symbol] = struct{}{}
	}
	return out
}

func (a *App) clearGapperAnalysis() {
	cfg := a.currentConfig()
	a.gapperMu.Lock()
	a.gapperState = gapperSnapshot{
		Enabled:    false,
		Pending:    false,
		GapPercent: cfg.Gapper.GapPercent,
		Records:    map[string]gappers.Result{},
		Results:    []gappers.Result{},
		Skips:      []gappers.Skip{},
		Note:       "gapper mode off",
	}
	payload := a.gapperPayloadLocked()
	a.gapperMu.Unlock()
	a.hub.SetGappers(payload)
}

func (a *App) setGapperPending(day time.Time, note string) {
	cfg := a.currentConfig()
	a.gapperMu.Lock()
	a.gapperState = gapperSnapshot{
		Enabled:    cfg.Gapper.Enabled,
		Pending:    cfg.Gapper.Enabled,
		GapPercent: cfg.Gapper.GapPercent,
		TargetDate: replayDateKey(day, a.tz),
		UpdatedAt:  time.Now().In(a.tz),
		Note:       note,
		Records:    map[string]gappers.Result{},
		Results:    []gappers.Result{},
		Skips:      []gappers.Skip{},
	}
	payload := a.gapperPayloadLocked()
	a.gapperMu.Unlock()
	a.hub.SetGappers(payload)
}

func (a *App) setGapperError(day time.Time, err error) {
	cfg := a.currentConfig()
	a.gapperMu.Lock()
	a.gapperState = gapperSnapshot{
		Enabled:    cfg.Gapper.Enabled,
		Pending:    false,
		GapPercent: cfg.Gapper.GapPercent,
		TargetDate: replayDateKey(day, a.tz),
		UpdatedAt:  time.Now().In(a.tz),
		Note:       err.Error(),
		Failed:     true,
		Records:    map[string]gappers.Result{},
		Results:    []gappers.Result{},
		Skips:      []gappers.Skip{},
	}
	payload := a.gapperPayloadLocked()
	a.gapperMu.Unlock()
	a.hub.SetGappers(payload)
}

func (a *App) replaceGapperAnalysis(cfg config.Config, day time.Time, records map[string]gappers.Result, results []gappers.Result, skips []gappers.Skip, note string) {
	a.gapperMu.Lock()
	a.gapperState = gapperSnapshot{
		Enabled:    cfg.Gapper.Enabled,
		Pending:    false,
		GapPercent: cfg.Gapper.GapPercent,
		TargetDate: replayDateKey(day, a.tz),
		UpdatedAt:  time.Now().In(a.tz),
		Note:       note,
		Results:    append([]gappers.Result(nil), results...),
		Skips:      append([]gappers.Skip(nil), skips...),
		Records:    records,
	}
	payload := a.gapperPayloadLocked()
	a.gapperMu.Unlock()
	a.hub.SetGappers(payload)
}

func (a *App) currentGapperPayload() gapperPayload {
	a.gapperMu.RLock()
	defer a.gapperMu.RUnlock()
	return a.gapperPayloadLocked()
}

func (a *App) gapperPayloadLocked() gapperPayload {
	updatedAt := ""
	if !a.gapperState.UpdatedAt.IsZero() {
		updatedAt = a.gapperState.UpdatedAt.In(a.tz).Format(time.RFC3339)
	}
	results := append([]gappers.Result(nil), a.gapperState.Results...)
	skips := append([]gappers.Skip(nil), a.gapperState.Skips...)
	return gapperPayload{
		Enabled:    a.gapperState.Enabled,
		Pending:    a.gapperState.Pending,
		GapPercent: a.gapperState.GapPercent,
		TargetDate: a.gapperState.TargetDate,
		UpdatedAt:  updatedAt,
		Note:       a.gapperState.Note,
		Failed:     a.gapperState.Failed,
		Count:      len(results),
		Results:    results,
		Skips:      skips,
	}
}

func filterAlertCSVBySymbols(inputPath, outputPath string, symbols map[string]struct{}) (int, error) {
	if len(symbols) == 0 {
		return 0, fmt.Errorf("no gapper symbols available for dashboard filtering")
	}

	in, err := os.Open(inputPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	reader := csv.NewReader(in)
	reader.FieldsPerRecord = -1
	writer := csv.NewWriter(out)
	defer writer.Flush()

	header, err := reader.Read()
	if err != nil {
		return 0, err
	}
	symbolIndex := -1
	for i, field := range header {
		if strings.TrimSpace(field) == "symbol" {
			symbolIndex = i
			break
		}
	}
	if symbolIndex < 0 {
		return 0, fmt.Errorf("missing required column %q", "symbol")
	}
	if err := writer.Write(header); err != nil {
		return 0, err
	}

	count := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		if symbolIndex >= len(record) {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(record[symbolIndex]))
		if _, ok := symbols[symbol]; !ok {
			continue
		}
		if err := writer.Write(record); err != nil {
			return count, err
		}
		count++
	}
	writer.Flush()
	return count, writer.Error()
}
