package flush

import (
	"testing"
	"time"

	"flush-detector/internal/bars"
	"flush-detector/internal/config"
)

func TestRegressionSlope(t *testing.T) {
	t.Parallel()
	got := RegressionSlope([]float64{10, 9, 8, 7, 6})
	if got >= 0 {
		t.Fatalf("RegressionSlope() = %f, want negative", got)
	}
}

func TestVWAPAccumulator(t *testing.T) {
	t.Parallel()
	var v VWAPAccumulator
	v.Add(bars.Bar{High: 11, Low: 9, Close: 10, Volume: 100})
	v.Add(bars.Bar{High: 21, Low: 19, Close: 20, Volume: 100})
	if got := v.Value(); got != 15 {
		t.Fatalf("VWAP Value() = %f, want 15", got)
	}
}

func TestRangeAndVolumeExpansion(t *testing.T) {
	t.Parallel()
	window := make([]bars.Bar, 0, 13)
	for i := 0; i < 10; i++ {
		window = append(window, bars.Bar{High: 10.5, Low: 10, Volume: 100})
	}
	for i := 0; i < 3; i++ {
		window = append(window, bars.Bar{High: 12, Low: 10, Volume: 400})
	}
	if got := RangeExpansion(window); got <= 1 {
		t.Fatalf("RangeExpansion() = %f, want > 1", got)
	}
	if got := VolumeExpansion(window); got <= 1 {
		t.Fatalf("VolumeExpansion() = %f, want > 1", got)
	}
}

func TestComputeMetricsFlushScore(t *testing.T) {
	t.Parallel()
	history := sampleBars(20)
	metrics := ComputeMetrics(history, 99)
	if metrics.FlushScore <= 0 {
		t.Fatalf("FlushScore = %.1f, want > 0", metrics.FlushScore)
	}
	if metrics.DropFromPrior30mHighPct <= 0 {
		t.Fatalf("DropFromPrior30mHighPct = %.1f, want > 0", metrics.DropFromPrior30mHighPct)
	}
}

func TestDetectorThresholdLogic(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("ET", -4*3600)
	cfg := config.Default().Flush
	cfg.MinAlertScore = 0
	cfg.MinVolumeSince4AM = 0
	cfg.MinBarsBeforeAlerts = 10
	cfg.StartTime = "09:40"
	cfg.EndTime = "15:30"
	cfg.RequireBelowVWAP = false
	cfg.RequireDropFromRecentHigh = false
	d := NewDetector(cfg, 0, loc)
	meta := SymbolMeta{Symbol: "AAPL"}

	base := time.Date(2026, 4, 2, 9, 30, 0, 0, loc)
	var alert *Alert
	found := false
	for _, bar := range sampleBarsAt(base, 20) {
		alert = d.Process(meta, bar)
		if alert != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("expected alert, got nil")
	}
	if alert != nil && alert.Symbol != "AAPL" {
		t.Fatalf("alert.Symbol = %s, want AAPL", alert.Symbol)
	}
}

func TestDetectorRequiresVolumeSince4AM(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("ET", -4*3600)
	cfg := config.Default().Flush
	cfg.MinAlertScore = 0
	cfg.MinVolumeSince4AM = 500000
	cfg.MinBarsBeforeAlerts = 10
	cfg.StartTime = "09:40"
	cfg.EndTime = "15:30"
	cfg.RequireBelowVWAP = false
	cfg.RequireDropFromRecentHigh = false

	d := NewDetector(cfg, 0, loc)
	meta := SymbolMeta{Symbol: "AAPL"}

	premarketStart := time.Date(2026, 4, 2, 4, 0, 0, 0, loc)
	for _, bar := range sampleBarsAt(premarketStart, 20) {
		bar.Volume = 25000
		if alert := d.Process(meta, bar); alert != nil {
			t.Fatal("unexpected premarket alert")
		}
	}

	rthStart := time.Date(2026, 4, 2, 9, 30, 0, 0, loc)
	var alert *Alert
	found := false
	for _, bar := range sampleBarsAt(rthStart, 20) {
		if next := d.Process(meta, bar); next != nil {
			alert = next
			found = true
		}
	}

	if !found || alert == nil {
		t.Fatal("expected alert once cumulative volume threshold was reached")
	}
	if alert.VolumeSince4AM < cfg.MinVolumeSince4AM {
		t.Fatalf("VolumeSince4AM = %.0f, want at least %.0f", alert.VolumeSince4AM, cfg.MinVolumeSince4AM)
	}
}

func sampleBars(n int) []bars.Bar {
	loc := time.UTC
	return sampleBarsAt(time.Date(2026, 4, 2, 9, 30, 0, 0, loc), n)
}

func sampleBarsAt(start time.Time, n int) []bars.Bar {
	out := make([]bars.Bar, 0, n)
	price := 100.0
	for i := 0; i < n; i++ {
		open := price
		close := price - 0.55
		high := open + 0.25
		low := close - 0.85
		volume := 1000.0 + float64(i)*40
		if i >= n-3 {
			low = close - 1.8
			high = open + 0.4
			volume = 5000 + float64(i)*400
		}
		bar := bars.Bar{
			Symbol: "AAPL",
			Open:   open,
			High:   high,
			Low:    low,
			Close:  close,
			Volume: volume,
			Start:  start.Add(time.Duration(i) * time.Minute),
			End:    start.Add(time.Duration(i+1) * time.Minute),
		}
		out = append(out, bar)
		price = close
	}
	return out
}
