package main

import (
	"fmt"
	"time"

	"github.com/tanrikuluozlem/burn/internal/analyzer"
	"github.com/tanrikuluozlem/burn/internal/budget"
)

// buildBudgetStore returns the configured store, or nil when no snapshot path
// is set. When nil, the budget feature is entirely inactive and no file is
// created.
func buildBudgetStore(path string) (*budget.FileStore, bool) {
	if path == "" {
		return nil, false
	}
	return budget.NewFileStore(path), true
}

// budgetTuning carries CLI-configurable knobs for the budget evaluation.
type budgetTuning struct {
	GapMinutes  int
	TrendDays   int
	WindowDays  int
	MinSamples  int
	MinCoverage float64
}

func (t budgetTuning) apply(opts *budget.Options) {
	if t.GapMinutes > 0 {
		opts.GapThreshold = time.Duration(t.GapMinutes) * time.Minute
	}
	if t.TrendDays > 0 {
		opts.TrendDays = t.TrendDays
	}
	if t.WindowDays > 0 {
		opts.WindowDays = t.WindowDays
	}
	if t.MinSamples > 0 {
		opts.MinTrendSamples = t.MinSamples
	}
	if t.MinCoverage > 0 {
		opts.MinCoverage = t.MinCoverage
	}
}

// runBudgetEvaluation evaluates month-to-date accrual, forecasting, trend, and
// budgets from a report. It returns nil when no snapshot path and no budgets
// are configured, so legacy output is unchanged.
//
// liveHourly is the report's current hourly cost; it seeds the observation that
// is appended (unless noSave). With no snapshot path but a budgets file, it
// evaluates against an empty in-memory history, which reports insufficient data
// rather than fabricating a month-to-date total.
func runBudgetEvaluation(report *analyzer.CostReport, path string, budgets []budget.Budget, tuning budgetTuning, noSave bool, now time.Time) (*budget.Result, error) {
	if path == "" && len(budgets) == 0 {
		return nil, nil
	}

	var store budget.Store
	if path != "" {
		store = budget.NewFileStore(path)
	} else {
		store = budget.NewMemoryStore()
	}

	opts := budget.DefaultOptions(now)
	opts.Budgets = budgets
	opts.Observe = &budget.Record{HourlyCost: report.HourlyCost}
	opts.Namespaces = namespaceHourlyCosts(report)
	tuning.apply(&opts)

	res, err := budget.Evaluate(store, opts)
	if err != nil {
		return nil, err
	}
	if !noSave {
		if err := budget.Save(store, res); err != nil {
			return nil, fmt.Errorf("save budget snapshot: %w", err)
		}
	}
	return res, nil
}

// namespaceHourlyCosts converts monthly namespace compute costs to hourly. It
// is an approximation of the current namespace share and is recorded only as
// optional detail.
func namespaceHourlyCosts(report *analyzer.CostReport) map[string]float64 {
	if len(report.Namespaces) == 0 {
		return nil
	}
	out := make(map[string]float64, len(report.Namespaces))
	for _, ns := range report.Namespaces {
		out[ns.Name] = ns.MonthlyCost / 730.0
	}
	return out
}
