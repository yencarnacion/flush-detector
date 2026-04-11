package gappers

import (
	"math"
	"sort"
	"strings"
	"time"
)

type Bar struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

type Result struct {
	Symbol          string    `json:"symbol"`
	Name            string    `json:"name,omitempty"`
	TargetDate      string    `json:"target_date"`
	PreviousDate    string    `json:"previous_date"`
	PreviousCloseAt time.Time `json:"previous_close_at"`
	PreviousClose   float64   `json:"previous_close"`
	OpenAt          time.Time `json:"open_at"`
	Open            float64   `json:"open"`
	GapPercent      float64   `json:"gap_percent"`
	VolumeSince4AM  float64   `json:"volume_since_4am"`
	HasPrice        bool      `json:"has_price"`
	Provisional     bool      `json:"provisional"`
}

type Skip struct {
	Symbol string `json:"symbol"`
	Reason string `json:"reason"`
}

func EvaluateSymbol(symbol, name string, bars []Bar, targetDate time.Time, loc *time.Location, gapThreshold float64) (Result, bool, Skip) {
	result, include, skip := EvaluateSymbolAsOf(symbol, name, bars, targetDate, time.Time{}, loc, gapThreshold)
	if skip.Reason != "" {
		return Result{}, false, skip
	}
	if !result.HasPrice || result.Provisional {
		return Result{}, false, Skip{Symbol: normalizeSymbol(symbol), Reason: "missing 09:30 ET opening minute for target date"}
	}
	return result, include, Skip{}
}

func EvaluateSymbolAsOf(symbol, name string, bars []Bar, targetDate, asOf time.Time, loc *time.Location, gapThreshold float64) (Result, bool, Skip) {
	symbol = normalizeSymbol(symbol)
	if loc == nil {
		loc = time.UTC
	}
	if len(bars) == 0 {
		return Result{}, false, Skip{Symbol: symbol, Reason: "no aggregate bars returned"}
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Time.Before(bars[j].Time) })

	targetKey := targetDate.In(loc).Format("2006-01-02")
	closeBar, ok := findPreviousSessionClose(bars, targetKey, loc)
	if !ok {
		return Result{}, false, Skip{Symbol: symbol, Reason: "missing prior regular-session close"}
	}
	if closeBar.Close <= 0 {
		return Result{}, false, Skip{Symbol: symbol, Reason: "prior close is not positive"}
	}

	result := Result{
		Symbol:          symbol,
		Name:            strings.TrimSpace(name),
		TargetDate:      targetKey,
		PreviousDate:    closeBar.Time.In(loc).Format("2006-01-02"),
		PreviousCloseAt: closeBar.Time.In(loc).Add(time.Minute),
		PreviousClose:   closeBar.Close,
	}

	openBar, ok := findOpeningBar(bars, targetKey, loc)
	if ok {
		result.OpenAt = openBar.Time.In(loc)
		result.Open = openBar.Open
		result.GapPercent = round(gapPercent(openBar.Open, closeBar.Close), 4)
		result.VolumeSince4AM = round(volumeSince4AMThrough(bars, targetKey, openBar.Time.In(loc), loc), 0)
		result.HasPrice = true
		return result, PassesThreshold(result.GapPercent, gapThreshold), Skip{}
	}

	latest, ok := findLatestTargetBar(bars, targetKey, asOf, loc)
	if !ok {
		return result, false, Skip{}
	}
	result.OpenAt = latest.Time.In(loc)
	result.Open = latest.Close
	result.GapPercent = round(gapPercent(latest.Close, closeBar.Close), 4)
	result.VolumeSince4AM = round(volumeSince4AMThrough(bars, targetKey, latest.Time.In(loc), loc), 0)
	result.HasPrice = true
	result.Provisional = true
	return result, PassesThreshold(result.GapPercent, gapThreshold), Skip{}
}

func UpdateResultWithLiveBar(result Result, bar Bar, loc *time.Location, gapThreshold float64) (Result, bool, bool) {
	if loc == nil {
		loc = time.UTC
	}
	if !sameTargetDate(result.TargetDate, bar.Time, loc) || result.PreviousClose <= 0 {
		return result, false, false
	}

	et := bar.Time.In(loc)
	updated := false
	if et.Hour() == 9 && et.Minute() == 30 {
		result.OpenAt = et
		result.Open = bar.Open
		result.GapPercent = round(gapPercent(bar.Open, result.PreviousClose), 4)
		result.VolumeSince4AM = round(result.VolumeSince4AM+volumeSince4AMContribution(bar, loc), 0)
		result.HasPrice = true
		result.Provisional = false
		updated = true
	} else if et.Hour() < 9 || (et.Hour() == 9 && et.Minute() < 30) {
		result.OpenAt = et
		result.Open = bar.Close
		result.GapPercent = round(gapPercent(bar.Close, result.PreviousClose), 4)
		result.VolumeSince4AM = round(result.VolumeSince4AM+volumeSince4AMContribution(bar, loc), 0)
		result.HasPrice = true
		result.Provisional = true
		updated = true
	}
	if !updated {
		return result, false, false
	}
	return result, PassesThreshold(result.GapPercent, gapThreshold), true
}

func PassesThreshold(gapPercent, threshold float64) bool {
	if threshold < 0 {
		return gapPercent <= threshold
	}
	return gapPercent >= threshold
}

func SortByGapDesc(results []Result) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].GapPercent != results[j].GapPercent {
			return results[i].GapPercent > results[j].GapPercent
		}
		return results[i].Symbol < results[j].Symbol
	})
}

func SortBySymbol(results []Result) {
	sort.Slice(results, func(i, j int) bool { return results[i].Symbol < results[j].Symbol })
}

func findOpeningBar(bars []Bar, targetKey string, loc *time.Location) (Bar, bool) {
	for _, b := range bars {
		et := b.Time.In(loc)
		if et.Format("2006-01-02") == targetKey && et.Hour() == 9 && et.Minute() == 30 {
			return b, true
		}
	}
	return Bar{}, false
}

func findLatestTargetBar(bars []Bar, targetKey string, asOf time.Time, loc *time.Location) (Bar, bool) {
	var best Bar
	var found bool
	for _, b := range bars {
		et := b.Time.In(loc)
		if et.Format("2006-01-02") != targetKey {
			continue
		}
		if et.Hour() > 9 || (et.Hour() == 9 && et.Minute() >= 30) {
			continue
		}
		if !asOf.IsZero() && et.After(asOf.In(loc)) {
			continue
		}
		if !found || et.After(best.Time.In(loc)) {
			best = b
			found = true
		}
	}
	return best, found
}

func findPreviousSessionClose(bars []Bar, targetKey string, loc *time.Location) (Bar, bool) {
	var best Bar
	var found bool
	for _, b := range bars {
		et := b.Time.In(loc)
		day := et.Format("2006-01-02")
		if day >= targetKey || !isRegularSessionMinute(et) {
			continue
		}
		if !found || et.After(best.Time.In(loc)) {
			best = b
			found = true
		}
	}
	return best, found
}

func isRegularSessionMinute(t time.Time) bool {
	hour, min, _ := t.Clock()
	afterOpen := hour > 9 || (hour == 9 && min >= 30)
	beforeClose := hour < 16
	return afterOpen && beforeClose
}

func volumeSince4AMThrough(bars []Bar, targetKey string, through time.Time, loc *time.Location) float64 {
	var total float64
	for _, b := range bars {
		et := b.Time.In(loc)
		if et.Format("2006-01-02") != targetKey || et.Before(volumeWindowStart(et)) || et.After(through) {
			continue
		}
		total += b.Volume
	}
	return total
}

func volumeSince4AMContribution(bar Bar, loc *time.Location) float64 {
	et := bar.Time.In(loc)
	if et.Before(volumeWindowStart(et)) || et.Hour() > 9 || (et.Hour() == 9 && et.Minute() > 30) {
		return 0
	}
	return bar.Volume
}

func volumeWindowStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 4, 0, 0, 0, t.Location())
}

func sameTargetDate(targetDate string, value time.Time, loc *time.Location) bool {
	return strings.TrimSpace(targetDate) != "" && value.In(loc).Format("2006-01-02") == targetDate
}

func gapPercent(price, previousClose float64) float64 {
	return (price - previousClose) / previousClose * 100
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}
