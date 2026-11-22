package slack

import (
	"strings"
	"testing"
	"time"

	"github.com/tanrikuluozlem/burn/internal/analyzer"
	"github.com/tanrikuluozlem/burn/internal/budget"
)

func TestFormatCostReportWithNilBudgetUnchanged(t *testing.T) {
	report := &analyzer.CostReport{TotalNodes: 1, TotalPods: 2, MonthlyCost: 100, TotalMonthlyCost: 120}
	base := FormatCostReport(report, false)
	withNil := FormatCostReportWithBudget(report, nil, false)
	if len(base.Blocks) != len(withNil.Blocks) {
		t.Errorf("nil budget changed block count: %d vs %d", len(base.Blocks), len(withNil.Blocks))
	}
}

func TestFormatCostReportWithBudgetAddsBlocks(t *testing.T) {
	report := &analyzer.CostReport{TotalNodes: 1, TotalPods: 2, MonthlyCost: 100, TotalMonthlyCost: 120}
	res := &budget.Result{
		Month: "2026-03",
		AsOf:  time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
		Accrual: budget.Accrual{
			KnownCost: 500, Coverage: 0.8, GapHours: 12, PartialStart: true,
		},
		Rate:     budget.RateEstimate{Rate: 1.5, Source: "trend"},
		Forecast: budget.Forecast{ProjectedTotal: 900},
		Budgets: []budget.BudgetStatus{{
			Name: "cluster", Amount: 1000, Spend: 500, SpendPercent: 50,
			Forecast: 900, ForecastPercent: 90, State: budget.StateNormal,
		}},
		Trend: budget.WindowComparison{
			WindowDays: 7, Comparable: true, ChangeAmount: 10, ChangePercent: 5,
			Current:        budget.WindowStats{Cost: 210, Coverage: 1},
			Previous:       budget.WindowStats{Cost: 200, Coverage: 0.5},
			CoverageDiffer: true,
		},
		DataQuality: budget.DataQuality{Coverage: 0.8},
	}
	msg := FormatCostReportWithBudget(report, res, false)
	var text strings.Builder
	for _, b := range msg.Blocks {
		if b.Text != nil {
			text.WriteString(b.Text.Text)
			text.WriteString("\n")
		}
	}
	out := text.String()
	for _, want := range []string{
		"Month-to-date", "Known spend: $500.00", "forecast: $900.00",
		"cluster", "$500.00 of $1000.00", "Last 7d vs previous 7d",
		"not backfilled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("budget blocks missing %q\n%s", want, out)
		}
	}
}

func TestFormatCostReportWithBudgetInsufficientShowsRange(t *testing.T) {
	report := &analyzer.CostReport{MonthlyCost: 1}
	res := &budget.Result{
		Month:   "2026-03",
		AsOf:    time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
		Accrual: budget.Accrual{KnownCost: 5, Coverage: 0.1},
		Budgets: []budget.BudgetStatus{{
			Name: "x", Amount: 100, State: budget.StateInsufficient,
			CredibleLow: 90, CredibleHigh: 150,
		}},
		Trend: budget.WindowComparison{WindowDays: 7, Comparable: false, Current: budget.WindowStats{Cost: 5}},
	}
	msg := FormatCostReportWithBudget(report, res, false)
	var out strings.Builder
	for _, b := range msg.Blocks {
		if b.Text != nil {
			out.WriteString(b.Text.Text)
		}
	}
	if !strings.Contains(out.String(), "credible $90.00–$150.00") {
		t.Errorf("insufficient budget missing credible range:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "not comparable") {
		t.Errorf("missing not-comparable trend note:\n%s", out.String())
	}
}

func TestFormatBudgetAlertOnlyOnNotify(t *testing.T) {
	res := &budget.Result{
		Month: "2026-03",
		Budgets: []budget.BudgetStatus{{
			Name: "cluster", Amount: 100, Spend: 110, SpendPercent: 110,
			Forecast: 120, ForecastPercent: 120, State: budget.StateOverspent,
		}},
		Notification: budget.NotificationDecision{Notify: false, NewLevel: budget.LevelNormal},
	}
	if msg := FormatBudgetAlert(res); msg != nil {
		t.Fatal("FormatBudgetAlert returned a message when Notify is false")
	}

	res.Notification = budget.NotificationDecision{Notify: true, NewLevel: budget.LevelOverspent, Cause: "cluster"}
	msg := FormatBudgetAlert(res)
	if msg == nil {
		t.Fatal("FormatBudgetAlert returned nil when Notify is true")
	}
	var out string
	for _, b := range msg.Blocks {
		if b.Text != nil {
			out += b.Text.Text
		}
	}
	if !strings.Contains(out, "overspent") || !strings.Contains(out, "cluster") {
		t.Errorf("alert missing expected content:\n%s", out)
	}

	if FormatBudgetAlert(nil) != nil {
		t.Error("FormatBudgetAlert(nil) should be nil")
	}
}
