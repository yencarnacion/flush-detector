package persistence

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"flush-detector/internal/flush"
)

type AlertCSVLogger struct {
	dir string
	mu  sync.Mutex
}

func NewAlertCSVLogger(dir string) *AlertCSVLogger {
	if strings.TrimSpace(dir) == "" {
		dir = "log"
	}
	return &AlertCSVLogger{dir: dir}
}

func (l *AlertCSVLogger) Append(alert flush.Alert) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(l.dir, fmt.Sprintf("alerts_%s.csv", alert.AlertTime.Format("20060102")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	w := csv.NewWriter(f)
	if info.Size() == 0 {
		if err := w.Write(alertCSVHeader); err != nil {
			return err
		}
	}
	if err := w.Write(alertCSVRecord(alert)); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

var alertCSVHeader = []string{
	"alert_id",
	"alert_time_et",
	"session_date",
	"symbol",
	"name",
	"sources",
	"price",
	"flush_score",
	"tier",
	"drop_from_prior_30m_high_pct",
	"distance_below_vwap_pct",
	"roc_5m_pct",
	"roc_10m_pct",
	"down_slope_20m_pct_per_bar",
	"range_expansion",
	"volume_expansion",
	"summary",
}

func alertCSVRecord(alert flush.Alert) []string {
	return []string{
		alert.ID,
		alert.AlertTime.Format("2006-01-02 15:04:05"),
		alert.SessionDate,
		alert.Symbol,
		alert.Name,
		strings.Join(alert.Sources, "|"),
		formatFloat1(alert.Price),
		formatFloat1(alert.FlushScore),
		alert.Tier,
		formatFloat1(alert.Metrics.DropFromPrior30mHighPct),
		formatFloat1(alert.Metrics.DistanceBelowVWAPPct),
		formatFloat1(alert.Metrics.ROC5mPct),
		formatFloat1(alert.Metrics.ROC10mPct),
		formatFloat1(alert.Metrics.DownSlope20mPctPerBar),
		formatFloat1(alert.Metrics.RangeExpansion),
		formatFloat1(alert.Metrics.VolumeExpansion),
		alert.Summary,
	}
}

func formatFloat1(v float64) string {
	return fmt.Sprintf("%.1f", v)
}
