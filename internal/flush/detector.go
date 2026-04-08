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
	mu       sync.Mutex
	cfg      config.FlushConfig
	cooldown time.Duration
	tz       *time.Location
	states   map[string]*symbolState
}

type symbolState struct {
	dayKey         string
	sessionKey     string
	bars           []bars.Bar
	vwap           VWAPAccumulator
	volumeSince4AM float64
	lastAlertTime  time.Time
	alertsToday    int
	lastBarEndMS   int64
}

func NewDetector(cfg config.FlushConfig, cooldownSeconds int, tz *time.Location) *Detector {
	return &Detector{
		cfg:      cfg,
		cooldown: time.Duration(cooldownSeconds) * time.Second,
		tz:       tz,
		states:   make(map[string]*symbolState),
	}
}

func (d *Detector) UpdateConfig(cfg config.FlushConfig, cooldownSeconds int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg
	d.cooldown = time.Duration(cooldownSeconds) * time.Second
}

func (d *Detector) Reset(cfg config.FlushConfig, cooldownSeconds int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg
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
	return d.process(meta, bar, true)
}

func (d *Detector) process(meta SymbolMeta, bar bars.Bar, allowAlert bool) *Alert {
	d.mu.Lock()
	defer d.mu.Unlock()

	cfg := d.cfg
	etEnd := bar.End.In(d.tz)
	dayKey := etEnd.Format("2006-01-02")
	volumeStart := VolumeWindowStart(etEnd)
	sessionStart := SessionWindow(strings.ToLower(cfg.Session), etEnd)

	st := d.states[meta.Symbol]
	if st == nil {
		st = &symbolState{}
		d.states[meta.Symbol] = st
	}

	sessionKey := fmt.Sprintf("%s|%s", etEnd.Format("2006-01-02"), strings.ToLower(cfg.Session))
	if st.dayKey != dayKey {
		*st = symbolState{
			dayKey:     dayKey,
			sessionKey: sessionKey,
		}
	} else if st.sessionKey != sessionKey {
		st.sessionKey = sessionKey
		st.bars = nil
		st.vwap.Reset()
		st.lastAlertTime = time.Time{}
		st.alertsToday = 0
		st.lastBarEndMS = 0
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

	metrics := ComputeMetrics(st.bars, st.vwap.Value())
	if cfg.RequireBelowVWAP && metrics.DistanceBelowVWAPPct <= 0 {
		return nil
	}
	if cfg.RequireDropFromRecentHigh && metrics.DropFromPrior30mHighPct <= 0 {
		return nil
	}
	if metrics.FlushScore < cfg.MinAlertScore {
		return nil
	}
	if st.volumeSince4AM < cfg.MinVolumeSince4AM {
		return nil
	}
	if d.cooldown > 0 && !st.lastAlertTime.IsZero() && etEnd.Before(st.lastAlertTime.Add(d.cooldown)) {
		return nil
	}
	if cfg.MaxAlertsPerSymbolPerDay > 0 && st.alertsToday >= cfg.MaxAlertsPerSymbolPerDay {
		return nil
	}

	st.lastAlertTime = etEnd
	st.alertsToday++

	return &Alert{
		ID:             fmt.Sprintf("%s-%d", meta.Symbol, bar.End.UnixMilli()),
		Symbol:         meta.Symbol,
		Name:           meta.Name,
		Sources:        append([]string(nil), meta.Sources...),
		AlertTime:      etEnd,
		SessionDate:    etEnd.Format("2006-01-02"),
		Price:          round1(bar.Close),
		FlushScore:     metrics.FlushScore,
		Tier:           TierForScore(metrics.FlushScore),
		VolumeSince4AM: round1(st.volumeSince4AM),
		Summary:        Summary(metrics),
		Metrics:        metrics,
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
