package gappers

import (
	"testing"
	"time"
)

func TestEvaluateSymbolPositiveGap(t *testing.T) {
	loc := mustLoc(t)
	bars := []Bar{
		{Time: time.Date(2026, 4, 8, 15, 59, 0, 0, loc), Close: 10.00},
		{Time: time.Date(2026, 4, 10, 4, 0, 0, 0, loc), Close: 10.20, Volume: 2000},
		{Time: time.Date(2026, 4, 10, 9, 29, 0, 0, loc), Close: 10.55, Volume: 3000},
		{Time: time.Date(2026, 4, 10, 9, 30, 0, 0, loc), Open: 10.60, Volume: 4000},
	}
	got, include, skip := EvaluateSymbol("aapl", "Apple", bars, time.Date(2026, 4, 10, 0, 0, 0, 0, loc), loc, 5)
	if skip.Reason != "" {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if !include {
		t.Fatal("expected include")
	}
	if got.Symbol != "AAPL" || got.PreviousDate != "2026-04-08" || got.GapPercent != 6.0 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.PreviousCloseAt.Format("15:04") != "16:00" {
		t.Fatalf("close timestamp = %s, want 16:00", got.PreviousCloseAt.Format("15:04"))
	}
	if got.VolumeSince4AM != 9000 {
		t.Fatalf("VolumeSince4AM = %.0f, want 9000", got.VolumeSince4AM)
	}
}

func TestEvaluateSymbolNegativeGap(t *testing.T) {
	loc := mustLoc(t)
	bars := []Bar{
		{Time: time.Date(2026, 4, 9, 15, 59, 0, 0, loc), Close: 20.00},
		{Time: time.Date(2026, 4, 10, 9, 30, 0, 0, loc), Open: 18.80},
	}
	got, include, skip := EvaluateSymbol("tsla", "", bars, time.Date(2026, 4, 10, 0, 0, 0, 0, loc), loc, -5)
	if skip.Reason != "" {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if !include || got.GapPercent != -6.0 {
		t.Fatalf("got include=%v result=%+v", include, got)
	}
	if PassesThreshold(-4.9, -5) {
		t.Fatal("-4.9 should not pass a -5 threshold")
	}
}

func TestEvaluateSymbolEarlyClose(t *testing.T) {
	loc := mustLoc(t)
	bars := []Bar{
		{Time: time.Date(2026, 7, 2, 15, 59, 0, 0, loc), Close: 99.00},
		{Time: time.Date(2026, 7, 3, 12, 59, 0, 0, loc), Close: 100.00},
		{Time: time.Date(2026, 7, 6, 9, 30, 0, 0, loc), Open: 103.00},
	}
	got, include, skip := EvaluateSymbol("nvda", "", bars, time.Date(2026, 7, 6, 0, 0, 0, 0, loc), loc, 2)
	if skip.Reason != "" {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if !include || got.PreviousDate != "2026-07-03" || got.PreviousCloseAt.Format("15:04") != "13:00" || got.GapPercent != 3.0 {
		t.Fatalf("unexpected result: include=%v %+v", include, got)
	}
}

func TestEvaluateSymbolAsOfUsesPremarketPriceUntilOpen(t *testing.T) {
	loc := mustLoc(t)
	bars := []Bar{
		{Time: time.Date(2026, 4, 9, 15, 59, 0, 0, loc), Close: 10.00},
		{Time: time.Date(2026, 4, 10, 3, 59, 0, 0, loc), Close: 10.40, Volume: 9000},
		{Time: time.Date(2026, 4, 10, 8, 15, 0, 0, loc), Close: 10.50, Volume: 1000},
		{Time: time.Date(2026, 4, 10, 8, 16, 0, 0, loc), Close: 10.60, Volume: 2000},
	}
	got, include, skip := EvaluateSymbolAsOf("amd", "", bars, time.Date(2026, 4, 10, 0, 0, 0, 0, loc), time.Date(2026, 4, 10, 8, 16, 30, 0, loc), loc, 5)
	if skip.Reason != "" {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if !include || !got.Provisional || got.GapPercent != 6.0 {
		t.Fatalf("unexpected result: include=%v %+v", include, got)
	}
	if got.VolumeSince4AM != 3000 {
		t.Fatalf("VolumeSince4AM = %.0f, want 3000", got.VolumeSince4AM)
	}
}

func TestEvaluateSymbolAsOfPrefersRegularOpen(t *testing.T) {
	loc := mustLoc(t)
	bars := []Bar{
		{Time: time.Date(2026, 4, 9, 15, 59, 0, 0, loc), Close: 10.00},
		{Time: time.Date(2026, 4, 10, 8, 15, 0, 0, loc), Close: 10.90},
		{Time: time.Date(2026, 4, 10, 9, 30, 0, 0, loc), Open: 10.40},
	}
	got, include, skip := EvaluateSymbolAsOf("amd", "", bars, time.Date(2026, 4, 10, 0, 0, 0, 0, loc), time.Date(2026, 4, 10, 9, 45, 0, 0, loc), loc, 4)
	if skip.Reason != "" {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if !include || got.Provisional || got.GapPercent != 4.0 {
		t.Fatalf("unexpected result: include=%v %+v", include, got)
	}
}

func TestEvaluateSymbolAsOfDoesNotUseRegularSessionBarsWhenOpenMissing(t *testing.T) {
	loc := mustLoc(t)
	bars := []Bar{
		{Time: time.Date(2026, 4, 9, 15, 59, 0, 0, loc), Close: 10.00},
		{Time: time.Date(2026, 4, 10, 9, 45, 0, 0, loc), Close: 10.80},
	}
	got, include, skip := EvaluateSymbolAsOf("amd", "", bars, time.Date(2026, 4, 10, 0, 0, 0, 0, loc), time.Date(2026, 4, 10, 9, 45, 30, 0, loc), loc, 5)
	if skip.Reason != "" {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if include || got.HasPrice {
		t.Fatalf("regular-session bar should not become provisional open: include=%v result=%+v", include, got)
	}
}

func TestUpdateResultWithLiveBar(t *testing.T) {
	loc := mustLoc(t)
	result := Result{
		Symbol:        "AMD",
		TargetDate:    "2026-04-10",
		PreviousClose: 10,
	}
	updated, include, changed := UpdateResultWithLiveBar(result, Bar{
		Time:   time.Date(2026, 4, 10, 8, 45, 0, 0, loc),
		Close:  10.60,
		Volume: 1000,
	}, loc, 5)
	if !changed || !include || !updated.Provisional || updated.GapPercent != 6.0 {
		t.Fatalf("unexpected premarket update: changed=%v include=%v result=%+v", changed, include, updated)
	}
	if updated.VolumeSince4AM != 1000 {
		t.Fatalf("premarket VolumeSince4AM = %.0f, want 1000", updated.VolumeSince4AM)
	}

	updated, include, changed = UpdateResultWithLiveBar(updated, Bar{
		Time:   time.Date(2026, 4, 10, 9, 30, 0, 0, loc),
		Open:   10.20,
		Volume: 3000,
	}, loc, 5)
	if !changed || include || updated.Provisional || updated.GapPercent != 2.0 {
		t.Fatalf("unexpected open update: changed=%v include=%v result=%+v", changed, include, updated)
	}
	if updated.VolumeSince4AM != 4000 {
		t.Fatalf("open VolumeSince4AM = %.0f, want 4000", updated.VolumeSince4AM)
	}

	_, include, changed = UpdateResultWithLiveBar(updated, Bar{
		Time:   time.Date(2026, 4, 10, 9, 45, 0, 0, loc),
		Close:  10.70,
		Volume: 5000,
	}, loc, 5)
	if changed || include {
		t.Fatalf("post-open bar should not update frozen 09:30 gap: changed=%v include=%v", changed, include)
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
