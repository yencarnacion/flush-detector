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
