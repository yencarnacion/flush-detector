package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flush-detector/internal/config"
	"flush-detector/internal/watchlist"
)

const replayCalendarCacheTTL = 10 * time.Minute

type replayDayRequest struct {
	Date string `json:"date"`
}

type replayCalendarPayload struct {
	Month          string         `json:"month"`
	MonthLabel     string         `json:"month_label"`
	StartDate      string         `json:"start_date"`
	EndDate        string         `json:"end_date"`
	Today          string         `json:"today"`
	AvailableDates []string       `json:"available_dates"`
	Coverage       map[string]int `json:"coverage"`
}

type replayCalendarCacheEntry struct {
	payload   replayCalendarPayload
	expiresAt time.Time
}

type replayState struct {
	replaying      bool
	historicalMode bool
	replayDate     string
	livePausedAt   time.Time
}

func parseReplayDate(raw string, tz *time.Location, now time.Time) (time.Time, error) {
	today := normalizeReplayDay(now, tz)
	if strings.TrimSpace(raw) == "" {
		return today, nil
	}

	day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid replay date: %w", err)
	}
	if day.After(today) {
		return time.Time{}, errors.New("replay date cannot be in the future")
	}
	return day, nil
}

func parseReplayMonth(raw string, tz *time.Location, now time.Time) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		today := now.In(tz)
		return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, tz), nil
	}

	month, err := time.ParseInLocation("2006-01", strings.TrimSpace(raw), tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid month: %w", err)
	}
	return time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, tz), nil
}

func normalizeReplayDay(t time.Time, tz *time.Location) time.Time {
	local := t.In(tz)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
}

func replayMonthBounds(month time.Time, tz *time.Location) (time.Time, time.Time) {
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, tz)
	end := start.AddDate(0, 1, 0).Add(-24 * time.Hour)
	return start, end
}

func replayDayRange(day, now time.Time, tz *time.Location) (time.Time, time.Time) {
	sessionDay := normalizeReplayDay(day, tz)
	from := time.Date(sessionDay.Year(), sessionDay.Month(), sessionDay.Day(), 4, 0, 0, 0, tz)
	endOfExtendedHours := time.Date(sessionDay.Year(), sessionDay.Month(), sessionDay.Day(), 20, 0, 0, 0, tz)
	today := normalizeReplayDay(now, tz)
	if sessionDay.Before(today) {
		return from, endOfExtendedHours
	}

	to := now.In(tz)
	if to.Before(from) {
		return from, from
	}
	if to.After(endOfExtendedHours) {
		return from, endOfExtendedHours
	}
	return from, to
}

func replayDateKey(t time.Time, tz *time.Location) string {
	return t.In(tz).Format("2006-01-02")
}

func replayMonthKey(t time.Time, tz *time.Location) string {
	return t.In(tz).Format("2006-01")
}

func (a *App) snapshotReplayState() replayState {
	a.replayStateMu.Lock()
	defer a.replayStateMu.Unlock()
	return replayState{
		replaying:      a.replaying,
		historicalMode: a.historicalMode,
		replayDate:     a.replayDate,
		livePausedAt:   a.livePausedAt,
	}
}

func (a *App) beginHistoricalReplay(day time.Time) bool {
	a.replayStateMu.Lock()
	defer a.replayStateMu.Unlock()
	if a.replaying {
		return false
	}
	if !a.historicalMode && a.livePausedAt.IsZero() {
		a.livePausedAt = time.Now().In(a.tz)
	}
	a.replaying = true
	a.historicalMode = true
	a.replayDate = replayDateKey(day, a.tz)
	return true
}

func (a *App) finishHistoricalReplay(day time.Time) {
	a.replayStateMu.Lock()
	a.replaying = false
	a.historicalMode = true
	a.replayDate = replayDateKey(day, a.tz)
	a.replayStateMu.Unlock()
}

func (a *App) failHistoricalReplay() {
	a.replayStateMu.Lock()
	a.replaying = false
	a.historicalMode = false
	a.replayDate = ""
	a.livePausedAt = time.Time{}
	a.replayStateMu.Unlock()
}

func (a *App) beginResumeLive() bool {
	a.replayStateMu.Lock()
	defer a.replayStateMu.Unlock()
	if a.replaying {
		return false
	}
	a.replaying = true
	return true
}

func (a *App) finishResumeLive() {
	a.replayStateMu.Lock()
	a.replaying = false
	a.historicalMode = false
	a.replayDate = ""
	a.livePausedAt = time.Time{}
	a.replayStateMu.Unlock()
}

func (a *App) suppressLiveProcessing() bool {
	state := a.snapshotReplayState()
	return state.replaying || state.historicalMode
}

func (a *App) replayCalendarCacheKey(month time.Time) string {
	symbols := watchlist.Symbols(a.currentWatchlist())
	slices.Sort(symbols)
	return fmt.Sprintf("%s|%s", replayMonthKey(month, a.tz), strings.Join(symbols, ","))
}

func (a *App) invalidateReplayCalendarCache() {
	a.calendarMu.Lock()
	a.replayCalendarCache = make(map[string]replayCalendarCacheEntry)
	a.calendarMu.Unlock()
}

func (a *App) replayCalendar(ctx context.Context, month time.Time) (replayCalendarPayload, error) {
	now := time.Now().In(a.tz)
	key := a.replayCalendarCacheKey(month)

	a.calendarMu.Lock()
	if entry, ok := a.replayCalendarCache[key]; ok && now.Before(entry.expiresAt) {
		a.calendarMu.Unlock()
		return entry.payload, nil
	}
	a.calendarMu.Unlock()

	start, end := replayMonthBounds(month, a.tz)
	today := normalizeReplayDay(now, a.tz)

	payload := replayCalendarPayload{
		Month:          replayMonthKey(month, a.tz),
		MonthLabel:     start.Format("January 2006"),
		StartDate:      replayDateKey(start, a.tz),
		EndDate:        replayDateKey(end, a.tz),
		Today:          replayDateKey(today, a.tz),
		AvailableDates: []string{},
		Coverage:       map[string]int{},
	}

	if start.After(today) {
		return payload, nil
	}
	if end.After(today) {
		end = today
		payload.EndDate = replayDateKey(end, a.tz)
	}

	symbols := watchlist.Symbols(a.currentWatchlist())
	if len(symbols) == 0 {
		return payload, nil
	}

	type result struct {
		dates []string
		err   error
	}

	sem := make(chan struct{}, 6)
	results := make(chan result, len(symbols))
	for _, symbol := range symbols {
		symbol := symbol
		go func() {
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			dates, err := a.massive.AvailableDates(ctx, symbol, start, end)
			select {
			case <-ctx.Done():
			case results <- result{dates: dates, err: err}:
			}
		}()
	}

	for range symbols {
		select {
		case <-ctx.Done():
			return replayCalendarPayload{}, ctx.Err()
		case res := <-results:
			if res.err != nil {
				return replayCalendarPayload{}, res.err
			}
			for _, date := range res.dates {
				payload.Coverage[date]++
			}
		}
	}

	for date := range payload.Coverage {
		payload.AvailableDates = append(payload.AvailableDates, date)
	}
	slices.Sort(payload.AvailableDates)

	a.calendarMu.Lock()
	if a.replayCalendarCache == nil {
		a.replayCalendarCache = make(map[string]replayCalendarCacheEntry)
	}
	a.replayCalendarCache[key] = replayCalendarCacheEntry{
		payload:   payload,
		expiresAt: now.Add(replayCalendarCacheTTL),
	}
	a.calendarMu.Unlock()

	return payload, nil
}

func (a *App) resetHistoricalReplayLocked(day time.Time, cfg config.Config) error {
	a.detector.Reset(cfg.Flush, cfg.Alert.CooldownSeconds)
	a.hub.ReplaceHistory(nil)
	return a.alertLog.DeleteDay(day)
}
