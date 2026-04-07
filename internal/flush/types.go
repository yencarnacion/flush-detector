package flush

import "time"

type Metrics struct {
	DropFromPrior30mHighPct float64 `json:"drop_from_prior_30m_high_pct"`
	DistanceBelowVWAPPct    float64 `json:"distance_below_vwap_pct"`
	ROC5mPct                float64 `json:"roc_5m_pct"`
	ROC10mPct               float64 `json:"roc_10m_pct"`
	DownSlope20mPctPerBar   float64 `json:"down_slope_20m_pct_per_bar"`
	RangeExpansion          float64 `json:"range_expansion"`
	VolumeExpansion         float64 `json:"volume_expansion"`
	FlushScore              float64 `json:"flush_score"`
}

type Alert struct {
	ID             string    `json:"id"`
	Symbol         string    `json:"symbol"`
	Name           string    `json:"name,omitempty"`
	Sources        []string  `json:"sources,omitempty"`
	AlertTime      time.Time `json:"alert_time"`
	SessionDate    string    `json:"session_date"`
	Price          float64   `json:"price"`
	FlushScore     float64   `json:"flush_score"`
	Tier           string    `json:"tier"`
	VolumeSince4AM float64   `json:"volume_since_4am"`
	Summary        string    `json:"summary"`
	Metrics        Metrics   `json:"metrics"`
}

type SymbolMeta struct {
	Symbol  string
	Name    string
	Sources []string
}
