package flush

import (
	"testing"
	"time"

	"flush-detector/internal/bars"
)

func TestComputeMetricsCharacterization(t *testing.T) {
	t.Parallel()

	history := buildLinearBars(13, 100.0, -0.1, 0.1, 0.2, 100, 150)

	got := ComputeMetrics(history, 99.4)

	if got.DropFromPrior30mHighPct != 1.5 {
		t.Fatalf("DropFromPrior30mHighPct = %.1f, want 1.5", got.DropFromPrior30mHighPct)
	}
	if got.DistanceBelowVWAPPct != 0.8 {
		t.Fatalf("DistanceBelowVWAPPct = %.1f, want 0.8", got.DistanceBelowVWAPPct)
	}
	if got.ROC5mPct != 0.5 {
		t.Fatalf("ROC5mPct = %.1f, want 0.5", got.ROC5mPct)
	}
	if got.ROC10mPct != 1.0 {
		t.Fatalf("ROC10mPct = %.1f, want 1.0", got.ROC10mPct)
	}
	if got.DownSlope20mPctPerBar != 0.1 {
		t.Fatalf("DownSlope20mPctPerBar = %.1f, want 0.1", got.DownSlope20mPctPerBar)
	}
	if got.RangeExpansion != 2.0 {
		t.Fatalf("RangeExpansion = %.1f, want 2.0", got.RangeExpansion)
	}
	if got.VolumeExpansion != 1.5 {
		t.Fatalf("VolumeExpansion = %.1f, want 1.5", got.VolumeExpansion)
	}
	if got.FlushScore != 42.3 {
		t.Fatalf("FlushScore = %.1f, want 42.3", got.FlushScore)
	}
}

func TestComputeMetricsHistoryThresholds(t *testing.T) {
	t.Parallel()

	t.Run("five bars", func(t *testing.T) {
		history := buildLinearBars(5, 100.0, -0.1, 0.1, 0.2, 100, 150)
		got := ComputeMetrics(history, 99.8)

		if got.ROC5mPct != 0 {
			t.Fatalf("ROC5mPct = %.1f, want 0", got.ROC5mPct)
		}
		if got.ROC10mPct != 0 {
			t.Fatalf("ROC10mPct = %.1f, want 0", got.ROC10mPct)
		}
		if got.DownSlope20mPctPerBar != 0 {
			t.Fatalf("DownSlope20mPctPerBar = %.1f, want 0", got.DownSlope20mPctPerBar)
		}
		if got.RangeExpansion != 0 {
			t.Fatalf("RangeExpansion = %.1f, want 0", got.RangeExpansion)
		}
		if got.VolumeExpansion != 0 {
			t.Fatalf("VolumeExpansion = %.1f, want 0", got.VolumeExpansion)
		}
	})

	t.Run("ten bars", func(t *testing.T) {
		history := buildLinearBars(10, 100.0, -0.1, 0.1, 0.2, 100, 150)
		got := ComputeMetrics(history, 99.5)

		if got.ROC5mPct == 0 {
			t.Fatal("ROC5mPct = 0, want non-zero once six bars exist")
		}
		if got.ROC10mPct != 0 {
			t.Fatalf("ROC10mPct = %.1f, want 0 before eleven bars", got.ROC10mPct)
		}
		if got.DownSlope20mPctPerBar == 0 {
			t.Fatal("DownSlope20mPctPerBar = 0, want non-zero once ten bars exist")
		}
		if got.RangeExpansion != 0 {
			t.Fatalf("RangeExpansion = %.1f, want 0 before thirteen bars", got.RangeExpansion)
		}
		if got.VolumeExpansion != 0 {
			t.Fatalf("VolumeExpansion = %.1f, want 0 before thirteen bars", got.VolumeExpansion)
		}
	})
}

func TestComputeMetricsFlushScoreClamp(t *testing.T) {
	t.Parallel()

	history := buildLinearBars(13, 100.0, -1.0, 0.1, 2.0, 100, 500)
	got := ComputeMetrics(history, 100.0)

	if got.FlushScore != 100.0 {
		t.Fatalf("FlushScore = %.1f, want 100.0", got.FlushScore)
	}
}

func buildLinearBars(n int, startClose, step, baseHalfRange, expandedHalfRange, baseVolume, expandedVolume float64) []bars.Bar {
	start := time.Date(2026, 4, 2, 9, 30, 0, 0, time.UTC)
	out := make([]bars.Bar, 0, n)
	for i := 0; i < n; i++ {
		close := startClose + float64(i)*step
		halfRange := baseHalfRange
		volume := baseVolume
		if i >= n-3 {
			halfRange = expandedHalfRange
			volume = expandedVolume
		}
		out = append(out, bars.Bar{
			Symbol: "AAPL",
			Open:   close - step,
			High:   close + halfRange,
			Low:    close - halfRange,
			Close:  close,
			Volume: volume,
			Start:  start.Add(time.Duration(i) * time.Minute),
			End:    start.Add(time.Duration(i+1) * time.Minute),
		})
	}
	return out
}
