package persistence

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"flush-detector/internal/flush"
)

func TestAlertCSVLoggerAppendCreatesDailyCSV(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "log")
	logger := NewAlertCSVLogger(dir)

	first := sampleAlert(time.Date(2026, 4, 6, 9, 41, 0, 0, time.FixedZone("ET", -4*3600)), "AAPL-1", "AAPL", 62.3)
	second := sampleAlert(time.Date(2026, 4, 6, 9, 52, 0, 0, time.FixedZone("ET", -4*3600)), "TSLA-2", "TSLA", 78.4)

	if err := logger.Append(first); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if err := logger.Append(second); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}

	path := filepath.Join(dir, "alerts_20260406.csv")
	rows := readCSVRows(t, path)

	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3", len(rows))
	}
	if !reflect.DeepEqual(rows[0], alertCSVHeader) {
		t.Fatalf("header = %v, want %v", rows[0], alertCSVHeader)
	}
	if rows[1][0] != "AAPL-1" {
		t.Fatalf("first alert id = %q, want AAPL-1", rows[1][0])
	}
	if rows[1][1] != "2026-04-06 09:41:00" {
		t.Fatalf("first alert time = %q, want 2026-04-06 09:41:00", rows[1][1])
	}
	if rows[1][2] != "2026-04-06" {
		t.Fatalf("session date = %q, want 2026-04-06", rows[1][2])
	}
	if rows[1][3] != "AAPL" {
		t.Fatalf("symbol = %q, want AAPL", rows[1][3])
	}
	if rows[1][5] != "watchlist|earnings" {
		t.Fatalf("sources = %q, want watchlist|earnings", rows[1][5])
	}
	if rows[1][7] != "62.3" {
		t.Fatalf("flush score = %q, want 62.3", rows[1][7])
	}
	if rows[1][16] == "" {
		t.Fatal("summary should not be empty")
	}
	if rows[2][0] != "TSLA-2" {
		t.Fatalf("second alert id = %q, want TSLA-2", rows[2][0])
	}
}

func TestAlertCSVLoggerAppendSplitsFilesByDay(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "log")
	logger := NewAlertCSVLogger(dir)

	first := sampleAlert(time.Date(2026, 4, 6, 15, 59, 0, 0, time.FixedZone("ET", -4*3600)), "AAPL-1", "AAPL", 62.3)
	second := sampleAlert(time.Date(2026, 4, 7, 9, 41, 0, 0, time.FixedZone("ET", -4*3600)), "AAPL-2", "AAPL", 66.1)

	if err := logger.Append(first); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if err := logger.Append(second); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}

	firstDay := readCSVRows(t, filepath.Join(dir, "alerts_20260406.csv"))
	secondDay := readCSVRows(t, filepath.Join(dir, "alerts_20260407.csv"))

	if len(firstDay) != 2 {
		t.Fatalf("first day row count = %d, want 2", len(firstDay))
	}
	if len(secondDay) != 2 {
		t.Fatalf("second day row count = %d, want 2", len(secondDay))
	}
	if firstDay[1][0] != "AAPL-1" {
		t.Fatalf("first day id = %q, want AAPL-1", firstDay[1][0])
	}
	if secondDay[1][0] != "AAPL-2" {
		t.Fatalf("second day id = %q, want AAPL-2", secondDay[1][0])
	}
}

func readCSVRows(t *testing.T, path string) [][]string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", path, err)
	}
	return rows
}

func sampleAlert(ts time.Time, id, symbol string, score float64) flush.Alert {
	metrics := flush.Metrics{
		DropFromPrior30mHighPct: 3.4,
		DistanceBelowVWAPPct:    1.2,
		ROC5mPct:                0.8,
		ROC10mPct:               1.1,
		DownSlope20mPctPerBar:   0.1,
		RangeExpansion:          2.2,
		VolumeExpansion:         1.8,
		FlushScore:              score,
	}
	return flush.Alert{
		ID:          id,
		Symbol:      symbol,
		Name:        symbol + " Inc.",
		Sources:     []string{"watchlist", "earnings"},
		AlertTime:   ts,
		SessionDate: ts.Format("2006-01-02"),
		Price:       97.4,
		FlushScore:  score,
		Tier:        flush.TierForScore(score),
		Summary:     flush.Summary(metrics),
		Metrics:     metrics,
	}
}
