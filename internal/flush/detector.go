package flush

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"flush-detector/internal/bars"
	"flush-detector/internal/config"
)

type Detector struct {
	mu            sync.Mutex
	cfg           config.FlushConfig
	operatingMode string
	cooldown      time.Duration
	tz            *time.Location
	states        map[string]*symbolState
}

type symbolState struct {
	dayKey              string
	sessionKey          string
	bars                []bars.Bar
	vwap                VWAPAccumulator
	volumeSince4AM      float64
	lastAlertTimeByMode map[string]time.Time
	alertsTodayByMode   map[string]int
	lastBarEndMS        int64
}

func NewDetector(cfg config.FlushConfig, operatingMode string, cooldownSeconds int, tz *time.Location) *Detector {
	return &Detector{
		cfg:           cfg,
		operatingMode: normalizeOperatingMode(operatingMode),
		cooldown:      time.Duration(cooldownSeconds) * time.Second,
		tz:            tz,
		states:        make(map[string]*symbolState),
	}
}

func (d *Detector) UpdateConfig(cfg config.FlushConfig, operatingMode string, cooldownSeconds int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg
	d.operatingMode = normalizeOperatingMode(operatingMode)
	d.cooldown = time.Duration(cooldownSeconds) * time.Second
}

func (d *Detector) Reset(cfg config.FlushConfig, operatingMode string, cooldownSeconds int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg
	d.operatingMode = normalizeOperatingMode(operatingMode)
	d.cooldown = time.Duration(cooldownSeconds) * time.Second
	d.states = make(map[string]*symbolState)
}

func (d *Detector) ResetUnknownSymbols(valid map[string]struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for symbol := range d.states {
		if _, ok := valid[symbol]; !ok {
			delete(d.states, symbol)
		}
	}
}

func (d *Detector) Seed(meta SymbolMeta, bar bars.Bar) {
	d.process(meta, bar, false)
}

func (d *Detector) Process(meta SymbolMeta, bar bars.Bar) *Alert {
	alerts := d.ProcessAll(meta, bar)
	if len(alerts) == 0 {
		return nil
	}
	return &alerts[0]
}

func (d *Detector) ProcessAll(meta SymbolMeta, bar bars.Bar) []Alert {
	return d.process(meta, bar, true)
}

func (d *Detector) process(meta SymbolMeta, bar bars.Bar, allowAlert bool) []Alert {
	d.mu.Lock()
	defer d.mu.Unlock()

	cfg := d.cfg
	operatingMode := d.operatingMode
	etEnd := bar.End.In(d.tz)
	dayKey := etEnd.Format("2006-01-02")
	volumeStart := VolumeWindowStart(etEnd)
	sessionStart := SessionWindow(strings.ToLower(cfg.Session), etEnd)

	st := d.states[meta.Symbol]
	if st == nil {
		st = newSymbolState()
		d.states[meta.Symbol] = st
	}

	sessionKey := fmt.Sprintf("%s|%s", etEnd.Format("2006-01-02"), strings.ToLower(cfg.Session))
	if st.dayKey != dayKey {
		*st = *newSymbolState()
		st.dayKey = dayKey
		st.sessionKey = sessionKey
	} else if st.sessionKey != sessionKey {
		st.sessionKey = sessionKey
		st.bars = nil
		st.vwap.Reset()
		st.lastAlertTimeByMode = make(map[string]time.Time)
		st.alertsTodayByMode = make(map[string]int)
		st.lastBarEndMS = 0
	}
	if st.lastAlertTimeByMode == nil {
		st.lastAlertTimeByMode = make(map[string]time.Time)
	}
	if st.alertsTodayByMode == nil {
		st.alertsTodayByMode = make(map[string]int)
	}

	if st.lastBarEndMS == bar.End.UnixMilli() {
		return nil
	}
	st.lastBarEndMS = bar.End.UnixMilli()

	if !etEnd.Before(volumeStart) {
		st.volumeSince4AM += bar.Volume
	}
	st.bars = append(st.bars, bar)
	if len(st.bars) > 390 {
		st.bars = st.bars[len(st.bars)-390:]
	}
	if etEnd.Before(sessionStart) {
		return nil
	}
	st.vwap.Add(bar)

	if !allowAlert || !cfg.Enabled {
		return nil
	}
	if len(st.bars) < cfg.MinBarsBeforeAlerts {
		return nil
	}
	if !withinClockWindow(etEnd, cfg.StartTime, cfg.EndTime) {
		return nil
	}

	if st.volumeSince4AM < cfg.MinVolumeSince4AM {
		return nil
	}

	alerts := make([]Alert, 0, 2)
	for _, mode := range alertModes(operatingMode) {
		metrics := ComputeMetricsForMode(st.bars, st.vwap.Value(), mode)
		if cfg.RequireBelowVWAP && metrics.DistanceBelowVWAPPct <= 0 {
			continue
		}
		if cfg.RequireDropFromRecentHigh && metrics.DropFromPrior30mHighPct <= 0 {
			continue
		}
		if metrics.FlushScore < cfg.MinAlertScore {
			continue
		}
		lastAlertTime := st.lastAlertTimeByMode[mode]
		if d.cooldown > 0 && !lastAlertTime.IsZero() && etEnd.Before(lastAlertTime.Add(d.cooldown)) {
			continue
		}
		if cfg.MaxAlertsPerSymbolPerDay > 0 && st.alertsTodayByMode[mode] >= cfg.MaxAlertsPerSymbolPerDay {
			continue
		}

		st.lastAlertTimeByMode[mode] = etEnd
		st.alertsTodayByMode[mode]++

		alerts = append(alerts, Alert{
			ID:             fmt.Sprintf("%s-%s-%d", meta.Symbol, mode, bar.End.UnixMilli()),
			OperatingMode:  mode,
			Symbol:         meta.Symbol,
			Name:           meta.Name,
			Sources:        append([]string(nil), meta.Sources...),
			AlertTime:      etEnd,
			SessionDate:    etEnd.Format("2006-01-02"),
			Price:          round1(bar.Close),
			FlushScore:     metrics.FlushScore,
			Tier:           TierForScore(metrics.FlushScore),
			VolumeSince4AM: round1(st.volumeSince4AM),
			Summary:        SummaryForMode(metrics, mode),
			Metrics:        metrics,
		})
	}
	return alerts
}

func normalizeOperatingMode(operatingMode string) string {
	switch strings.ToLower(strings.TrimSpace(operatingMode)) {
	case "rip", "up":
		return "up"
	case "both":
		return "both"
	default:
		return "down"
	}
}

func alertModes(operatingMode string) []string {
	if normalizeOperatingMode(operatingMode) == "both" {
		return []string{"up", "down"}
	}
	return []string{normalizeOperatingMode(operatingMode)}
}

func newSymbolState() *symbolState {
	return &symbolState{
		lastAlertTimeByMode: make(map[string]time.Time),
		alertsTodayByMode:   make(map[string]int),
	}
}

func withinClockWindow(t time.Time, startHHMM, endHHMM string) bool {
	start, err := time.Parse("15:04", startHHMM)
	if err != nil {
		return true
	}
	end, err := time.Parse("15:04", endHHMM)
	if err != nil {
		return true
	}
	startTime := time.Date(t.Year(), t.Month(), t.Day(), start.Hour(), start.Minute(), 0, 0, t.Location())
	endTime := time.Date(t.Year(), t.Month(), t.Day(), end.Hour(), end.Minute(), 0, 0, t.Location())
	if endTime.Before(startTime) {
		return !t.Before(startTime) || !t.After(endTime)
	}
	return !t.Before(startTime) && !t.After(endTime)
}
