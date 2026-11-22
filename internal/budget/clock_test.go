package budget

import (
	"testing"
	"time"
)

func TestEvaluateAtUsesInjectedClock(t *testing.T) {
	fixed := utc(2026, 2, 10, 6, 0) // February, leap-year check independent
	res, err := EvaluateAt(NewMemoryStore(), FixedClock{T: fixed}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AsOf.Equal(fixed) {
		t.Errorf("AsOf = %v, want %v", res.AsOf, fixed)
	}
	if res.Month != "2026-02" {
		t.Errorf("Month = %q, want 2026-02", res.Month)
	}
	if res.Forecast.MonthDays != 28 {
		t.Errorf("MonthDays = %d, want 28 for Feb 2026", res.Forecast.MonthDays)
	}
}

func TestEvaluateAtNilClockUsesSystem(t *testing.T) {
	before := time.Now().UTC()
	res, err := EvaluateAt(NewMemoryStore(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.AsOf.Before(before.Add(-time.Second)) {
		t.Errorf("AsOf = %v, want it set from system clock", res.AsOf)
	}
}

func TestEvaluateRequiresNow(t *testing.T) {
	if _, err := Evaluate(NewMemoryStore(), Options{}); err == nil {
		t.Error("Evaluate with zero Now = nil error, want error")
	}
}
