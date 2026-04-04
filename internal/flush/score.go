package flush

import (
	"math"

	"flush-detector/internal/bars"
)

func ComputeMetrics(history []bars.Bar, sessionVWAP float64) Metrics {
	m := Metrics{}
	if len(history) == 0 {
		return m
	}

	current := history[len(history)-1]
	priceRef := current.Low
	closeT := current.Close

	if len(history) > 1 {
		start := 0
		if len(history)-1 > 30 {
			start = len(history) - 31
		}
		recentHigh := 0.0
		for _, bar := range history[start : len(history)-1] {
			if bar.High > recentHigh {
				recentHigh = bar.High
			}
		}
		if recentHigh > 0 {
			m.DropFromPrior30mHighPct = math.Max(0, (recentHigh-priceRef)/recentHigh*100)
		}
	}

	if sessionVWAP > 0 {
		m.DistanceBelowVWAPPct = math.Max(0, (sessionVWAP-priceRef)/sessionVWAP*100)
	}

	if len(history) >= 6 {
		close5 := history[len(history)-6].Close
		if close5 > 0 {
			m.ROC5mPct = math.Max(0, (close5-closeT)/close5*100)
		}
	}

	if len(history) >= 11 {
		close10 := history[len(history)-11].Close
		if close10 > 0 {
			m.ROC10mPct = math.Max(0, (close10-closeT)/close10*100)
		}
	}

	if len(history) >= 10 {
		start := 0
		if len(history) > 20 {
			start = len(history) - 20
		}
		window := history[start:]
		closes := make([]float64, 0, len(window))
		for _, bar := range window {
			closes = append(closes, bar.Close)
		}
		meanClose := Mean(closes)
		if meanClose > 0 {
			slopePctPerBar := RegressionSlope(closes) / meanClose * 100
			m.DownSlope20mPctPerBar = math.Max(0, -slopePctPerBar)
		}
	}

	m.RangeExpansion = RangeExpansion(history)
	m.VolumeExpansion = VolumeExpansion(history)

	score := 0.0
	score += 25 * Clip(m.DropFromPrior30mHighPct/4.0, 0, 1)
	score += 20 * Clip(m.DistanceBelowVWAPPct/2.0, 0, 1)
	score += 15 * Clip(m.ROC5mPct/1.5, 0, 1)
	score += 10 * Clip(m.ROC10mPct/2.5, 0, 1)
	score += 10 * Clip(m.DownSlope20mPctPerBar/0.15, 0, 1)
	score += 10 * Clip((m.RangeExpansion-1.0)/1.5, 0, 1)
	score += 10 * Clip((m.VolumeExpansion-1.0)/2.0, 0, 1)
	m.FlushScore = round1(Clip(score, 0, 100))

	m.DropFromPrior30mHighPct = round1(m.DropFromPrior30mHighPct)
	m.DistanceBelowVWAPPct = round1(m.DistanceBelowVWAPPct)
	m.ROC5mPct = round1(m.ROC5mPct)
	m.ROC10mPct = round1(m.ROC10mPct)
	m.DownSlope20mPctPerBar = round1(m.DownSlope20mPctPerBar)
	m.RangeExpansion = round1(m.RangeExpansion)
	m.VolumeExpansion = round1(m.VolumeExpansion)

	return m
}
