package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseReplayDateRejectsFuture(t *testing.T) {
	tz := time.FixedZone("ET", -4*60*60)
	now := time.Date(2026, 4, 7, 15, 0, 0, 0, tz)

	_, err := parseReplayDate("2026-04-08", tz, now)
	if err == nil {
		t.Fatal("expected future replay date error")
	}
}

func TestReplayDayRangeForHistoricalDateUsesFullSession(t *testing.T) {
	tz := time.FixedZone("ET", -4*60*60)
	now := time.Date(2026, 4, 7, 15, 0, 0, 0, tz)
	day := time.Date(2026, 4, 3, 0, 0, 0, 0, tz)

	from, to := replayDayRange(day, now, tz)
	if got := from.Format(time.RFC3339); got != "2026-04-03T04:00:00-04:00" {
		t.Fatalf("from = %s", got)
	}
	if got := to.Format(time.RFC3339); got != "2026-04-03T20:00:00-04:00" {
		t.Fatalf("to = %s", got)
	}
}

func TestReplayDayRangeForTodayCapsAtNow(t *testing.T) {
	tz := time.FixedZone("ET", -4*60*60)
	now := time.Date(2026, 4, 7, 10, 17, 0, 0, tz)
	day := time.Date(2026, 4, 7, 0, 0, 0, 0, tz)

	from, to := replayDayRange(day, now, tz)
	if got := from.Format(time.RFC3339); got != "2026-04-07T04:00:00-04:00" {
		t.Fatalf("from = %s", got)
	}
	if !to.Equal(now) {
		t.Fatalf("to = %s, want %s", to.Format(time.RFC3339), now.Format(time.RFC3339))
	}
}

func TestReplayLivePauseTimeSurvivesMultipleReplayDays(t *testing.T) {
	tz := time.FixedZone("ET", -4*60*60)
	app := &App{tz: tz}
	day1 := time.Date(2026, 4, 6, 0, 0, 0, 0, tz)
	day2 := time.Date(2026, 4, 7, 0, 0, 0, 0, tz)

	if !app.beginHistoricalReplay(day1) {
		t.Fatal("beginHistoricalReplay(day1) = false")
	}
	firstPause := app.snapshotReplayState().livePausedAt
	if firstPause.IsZero() {
		t.Fatal("live pause time was not recorded")
	}
	app.finishHistoricalReplay(day1)

	if !app.beginHistoricalReplay(day2) {
		t.Fatal("beginHistoricalReplay(day2) = false")
	}
	secondPause := app.snapshotReplayState().livePausedAt
	if !secondPause.Equal(firstPause) {
		t.Fatalf("pause time changed across replay days: got %s want %s", secondPause, firstPause)
	}

	app.finishResumeLive()
	if pause := app.snapshotReplayState().livePausedAt; !pause.IsZero() {
		t.Fatalf("pause time after finishResumeLive = %s, want zero", pause)
	}
}

func TestResetCacheDirRecreatesEmptyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "stale.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := resetCacheDir(dir); err != nil {
		t.Fatalf("resetCacheDir error = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cache dir stat error = %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale cache file removed, stat err = %v", err)
	}
}
