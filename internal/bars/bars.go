package bars

import "time"

type Bar struct {
	Symbol string    `json:"symbol"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume float64   `json:"volume"`
	VWAP   float64   `json:"vwap,omitempty"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

func (b Bar) TypicalPrice() float64 {
	return (b.High + b.Low + b.Close) / 3
}

func (b Bar) Range() float64 {
	return b.High - b.Low
}
