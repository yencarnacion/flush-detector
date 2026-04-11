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
	logger := NewAlertCSVLogger(dir, newYorkLocation(t))

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
	if rows[1][16] != "512300" {
		t.Fatalf("volume_since_4am = %q, want 512300", rows[1][16])
	}
	if rows[1][17] == "" {
		t.Fatal("summary should not be empty")
	}
	if rows[1][18] != "4.25" {
		t.Fatalf("gap_percent = %q, want 4.25", rows[1][18])
	}
	if rows[2][0] != "TSLA-2" {
		t.Fatalf("second alert id = %q, want TSLA-2", rows[2][0])
	}
}

func TestAlertCSVLoggerAppendSplitsFilesByDay(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "log")
	logger := NewAlertCSVLogger(dir, newYorkLocation(t))

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

func TestAlertCSVLoggerAppendMigratesExistingHeader(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "alerts_20260406.csv")
	oldHeader := alertCSVHeader[:len(alertCSVHeader)-1]
	oldRecord := []string{"old-id", "2026-04-06 09:31:00", "2026-04-06", "OLD", "", "", "10.0", "60.0", "Candidate", "1.0", "1.0", "1.0", "1.0", "0.100", "1.0", "1.0", "1000", "old summary"}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	if err := w.Write(oldHeader); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(oldRecord); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	logger := NewAlertCSVLogger(dir, newYorkLocation(t))
	alert := sampleAlert(time.Date(2026, 4, 6, 9, 41, 0, 0, time.FixedZone("ET", -4*3600)), "AAPL-1", "AAPL", 62.3)
	if err := logger.Append(alert); err != nil {
		t.Fatalf("Append(alert) error = %v", err)
	}

	rows := readCSVRows(t, path)
	if !reflect.DeepEqual(rows[0], alertCSVHeader) {
		t.Fatalf("header = %v, want %v", rows[0], alertCSVHeader)
	}
	if len(rows[1]) != len(alertCSVHeader) || rows[1][18] != "" {
		t.Fatalf("old row was not padded with empty gap field: %v", rows[1])
	}
	if rows[2][18] != "4.25" {
		t.Fatalf("new row gap = %q, want 4.25", rows[2][18])
	}
}

func TestAlertCSVLoggerAppendNormalizesToNewYorkDST(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation error = %v", err)
	}

	dir := filepath.Join(t.TempDir(), "log")
	logger := NewAlertCSVLogger(dir, loc)

	winter := sampleAlert(time.Date(2026, 1, 6, 14, 41, 0, 0, time.UTC), "AAPL-1", "AAPL", 62.3)
	summer := sampleAlert(time.Date(2026, 7, 6, 13, 41, 0, 0, time.UTC), "AAPL-2", "AAPL", 66.1)

	if err := logger.Append(winter); err != nil {
		t.Fatalf("Append(winter) error = %v", err)
	}
	if err := logger.Append(summer); err != nil {
		t.Fatalf("Append(summer) error = %v", err)
	}

	winterDay := readCSVRows(t, filepath.Join(dir, "alerts_20260106.csv"))
	summerDay := readCSVRows(t, filepath.Join(dir, "alerts_20260706.csv"))

	if winterDay[1][1] != "2026-01-06 09:41:00" {
		t.Fatalf("winter alert time = %q, want 2026-01-06 09:41:00", winterDay[1][1])
	}
	if summerDay[1][1] != "2026-07-06 09:41:00" {
		t.Fatalf("summer alert time = %q, want 2026-07-06 09:41:00", summerDay[1][1])
	}
}

func TestAlertCSVLoggerDeleteDayRemovesDailyCSV(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "log")
	logger := NewAlertCSVLogger(dir, newYorkLocation(t))
	alert := sampleAlert(time.Date(2026, 4, 6, 9, 41, 0, 0, time.FixedZone("ET", -4*3600)), "AAPL-1", "AAPL", 62.3)

	if err := logger.Append(alert); err != nil {
		t.Fatalf("Append(alert) error = %v", err)
	}
	if err := logger.DeleteDay(alert.AlertTime); err != nil {
		t.Fatalf("DeleteDay(alert.AlertTime) error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "alerts_20260406.csv")); !os.IsNotExist(err) {
		t.Fatalf("expected alerts_20260406.csv to be deleted, stat err = %v", err)
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
		ID:             id,
		Symbol:         symbol,
		Name:           symbol + " Inc.",
		Sources:        []string{"watchlist", "earnings"},
		AlertTime:      ts,
		SessionDate:    ts.Format("2006-01-02"),
		Price:          97.4,
		FlushScore:     score,
		GapPercent:     4.25,
		Tier:           flush.TierForScore(score),
		VolumeSince4AM: 512300,
		Summary:        flush.Summary(metrics),
		Metrics:        metrics,
	}
}

func newYorkLocation(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation error = %v", err)
	}
	return loc
}
