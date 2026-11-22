package budget

import (
	"strings"
	"testing"
)

func TestParseBudgets(t *testing.T) {
	bs, err := ParseBudgets(`[{"name":"cluster","amount":1000,"thresholds":[0.8,1.0]},
		{"name":"app","namespace":"app","amount":200,"thresholds":[0.5]}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("budgets = %d, want 2", len(bs))
	}
	if bs[1].Namespace != "app" || bs[1].Amount != 200 {
		t.Errorf("second budget wrong: %+v", bs[1])
	}

	// Empty input yields no budgets.
	if bs, err := ParseBudgets("  "); err != nil || bs != nil {
		t.Errorf("empty parse = %v, %v; want nil, nil", bs, err)
	}

	// Invalid threshold order is rejected, not silently accepted.
	if _, err := ParseBudgets(`[{"name":"x","amount":100,"thresholds":[1.0,0.8]}]`); err == nil {
		t.Error("descending thresholds accepted; want error")
	}

	// Unknown fields are rejected so a typo cannot silently disable a budget.
	if _, err := ParseBudgets(`[{"name":"x","amout":100,"thresholds":[0.8]}]`); err == nil {
		t.Error("unknown field accepted; want error")
	}

	// Zero amount rejected.
	if _, err := ParseBudgets(`[{"name":"x","amount":0,"thresholds":[0.8]}]`); err == nil {
		t.Error("zero amount accepted; want error")
	}
}

func TestLoadBudgetsMissingFile(t *testing.T) {
	if bs, err := LoadBudgets(""); err != nil || bs != nil {
		t.Errorf("empty path = %v, %v; want nil, nil", bs, err)
	}
	if _, err := LoadBudgets("/nonexistent/path/budgets.json"); err == nil {
		t.Error("missing file accepted; want error")
	}
}

func TestFormatTextSurfacesLimitations(t *testing.T) {
	r := &Result{
		Month:   "2026-03",
		AsOf:    utc(2026, 3, 20, 0, 0),
		Accrual: Accrual{KnownCost: 50, Coverage: 0.4, GapHours: 100, PartialStart: true},
		Rate:    RateEstimate{Rate: 1, Source: "current"},
		Forecast: Forecast{
			ProjectedTotal:   400,
			MonthDays:        31,
			GapEstimatedCost: 100,
			Limitation:       "month-to-date data starts after the month began",
		},
		DataQuality: DataQuality{Coverage: 0.4},
		Budgets: []BudgetStatus{{
			Name: "cluster", Amount: 1000, Spend: 50, SpendPercent: 5,
			Forecast: 400, ForecastPercent: 40,
			State: StateInsufficient, CredibleLow: 400, CredibleHigh: 500,
		}},
		Trend:    WindowComparison{WindowDays: 7, Comparable: false, Current: WindowStats{Cost: 10}},
		Warnings: []string{"the month began before the first snapshot"},
	}
	out := FormatText(r)
	for _, want := range []string{
		"Month-to-date", "coverage: 40%", "gaps: 100.0h",
		"not comparable", "insufficient_data", "credible range",
		"Note:", "Warnings",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatText missing %q\n%s", want, out)
		}
	}
}

func TestFormatTextComparableTrend(t *testing.T) {
	r := &Result{
		Month:       "2026-03",
		AsOf:        utc(2026, 3, 20, 0, 0),
		Trend:       WindowComparison{WindowDays: 7, Comparable: true, ChangeAmount: 5, ChangePercent: 10, Current: WindowStats{Cost: 55}, Previous: WindowStats{Cost: 50}},
		DataQuality: DataQuality{Coverage: 1},
	}
	out := FormatText(r)
	if !strings.Contains(out, "+5.00 (+10.0%)") {
		t.Errorf("comparable trend not rendered:\n%s", out)
	}
}
