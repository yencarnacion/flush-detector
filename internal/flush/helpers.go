package flush

import (
	"fmt"
	"math"
	"time"

	"flush-detector/internal/bars"
)

const tiny = 1e-9

type VWAPAccumulator struct {
	sumPV  float64
	sumVol float64
}

func (v *VWAPAccumulator) Add(bar bars.Bar) {
	if bar.Volume <= 0 {
		return
	}
	v.sumPV += bar.TypicalPrice() * bar.Volume
	v.sumVol += bar.Volume
}

func (v *VWAPAccumulator) Reset() {
	v.sumPV = 0
	v.sumVol = 0
}

func (v VWAPAccumulator) Value() float64 {
	if v.sumVol <= 0 {
		return 0
	}
	return v.sumPV / v.sumVol
}

func Clip(x, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, x))
}

func RegressionSlope(values []float64) float64 {
	n := float64(len(values))
	if len(values) < 2 {
		return 0
	}
	var sumX, sumY, sumXY, sumXX float64
	for i, v := range values {
		x := float64(i)
		sumX += x
		sumY += v
		sumXY += x * v
		sumXX += x * x
	}
	denom := n*sumXX - sumX*sumX
	if math.Abs(denom) < tiny {
		return 0
	}
	return (n*sumXY - sumX*sumY) / denom
}

func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func RangeExpansion(window []bars.Bar) float64 {
	if len(window) < 13 {
		return 0
	}
	shortBars := window[len(window)-3:]
	baseBars := window[len(window)-13 : len(window)-3]
	shortRanges := make([]float64, 0, len(shortBars))
	baseRanges := make([]float64, 0, len(baseBars))
	for _, bar := range shortBars {
		shortRanges = append(shortRanges, bar.Range())
	}
	for _, bar := range baseBars {
		baseRanges = append(baseRanges, bar.Range())
	}
	return Mean(shortRanges) / math.Max(Mean(baseRanges), tiny)
}

func VolumeExpansion(window []bars.Bar) float64 {
	if len(window) < 13 {
		return 0
	}
	shortBars := window[len(window)-3:]
	baseBars := window[len(window)-13 : len(window)-3]
	shortVolumes := make([]float64, 0, len(shortBars))
	baseVolumes := make([]float64, 0, len(baseBars))
	for _, bar := range shortBars {
		shortVolumes = append(shortVolumes, bar.Volume)
	}
	for _, bar := range baseBars {
		baseVolumes = append(baseVolumes, bar.Volume)
	}
	return Mean(shortVolumes) / math.Max(Mean(baseVolumes), tiny)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func TierForScore(score float64) string {
	switch {
	case score >= 90:
		return "Extreme"
	case score >= 75:
		return "Strong"
	case score >= 60:
		return "Candidate"
	case score >= 40:
		return "Notable"
	default:
		return "Low"
	}
}

func Summary(metrics Metrics) string {
	return fmt.Sprintf(
		"%.1f%% below prior 30m high, %.1f%% below VWAP, 5m ROC -%.1f%%, range x%.1f, volume x%.1f",
		round1(metrics.DropFromPrior30mHighPct),
		round1(metrics.DistanceBelowVWAPPct),
		round1(metrics.ROC5mPct),
		round1(metrics.RangeExpansion),
		round1(metrics.VolumeExpansion),
	)
}

func VolumeWindowStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 4, 0, 0, 0, t.Location())
}

func SessionWindow(session string, t time.Time) (start time.Time) {
	switch session {
	case "pre":
		return VolumeWindowStart(t)
	case "pm":
		return time.Date(t.Year(), t.Month(), t.Day(), 16, 0, 0, 0, t.Location())
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 9, 30, 0, 0, t.Location())
	}
}
