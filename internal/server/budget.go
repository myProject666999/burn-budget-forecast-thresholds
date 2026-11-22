package server

import (
	"context"
	"time"

	"github.com/tanrikuluozlem/burn/internal/analyzer"
	"github.com/tanrikuluozlem/burn/internal/budget"
)

// budgetStore returns the configured store, or nil when no snapshot path is set.
func (s *Server) budgetStore() budget.Store {
	if s.config.SnapshotPath == "" {
		return nil
	}
	return budget.NewFileStore(s.config.SnapshotPath)
}

// handleBudget reports month-to-date accrual, calendar-month forecast, the
// adjacent-window trend, and per-budget status. It reuses the same collector /
// analyzer path as /burn and appends a snapshot so successive runs accrue.
func (s *Server) handleBudget(ctx context.Context) (string, error) {
	report, _, err := s.getReport(ctx)
	if err != nil {
		return "", err
	}
	res, err := s.evaluateBudget(report)
	if err != nil {
		return "", err
	}
	out := budget.FormatText(res)
	if s.config.SnapshotPath == "" {
		out = "_No snapshot path configured; month-to-date accrual and forecasting need one to accumulate history. Configure --snapshot-path / BURN_SNAPSHOT_PATH._\n\n" + out
	}
	if s.config.BudgetFile == "" {
		out += "\n_No budgets configured; set --budget / BURN_BUDGET_FILE to add thresholds._"
	}
	return out, nil
}

func (s *Server) evaluateBudget(report *analyzer.CostReport) (*budget.Result, error) {
	budgets, err := budget.LoadBudgets(s.config.BudgetFile)
	if err != nil {
		return nil, err
	}

	store := s.budgetStore()
	save := store != nil
	if store == nil {
		// No snapshot path: evaluate against empty in-memory history so budgets
		// still render, but nothing is persisted and the result says data is
		// insufficient rather than fabricating month-to-date spend.
		store = budget.NewMemoryStore()
	}

	opts := budget.DefaultOptions(time.Now().UTC())
	opts.Budgets = budgets
	opts.Observe = &budget.Record{HourlyCost: report.HourlyCost}
	opts.Namespaces = reportNamespaceHourly(report)
	if s.config.GapThreshold > 0 {
		opts.GapThreshold = s.config.GapThreshold
	}
	if s.config.WindowDays > 0 {
		opts.WindowDays = s.config.WindowDays
	}
	if s.config.TrendDays > 0 {
		opts.TrendDays = s.config.TrendDays
	}

	res, err := budget.Evaluate(store, opts)
	if err != nil {
		return nil, err
	}
	if save {
		// The Slack response is the notification channel; only the observe/save
		// path writes, and only when a snapshot path is configured.
		if err := budget.Save(store, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func reportNamespaceHourly(report *analyzer.CostReport) map[string]float64 {
	if len(report.Namespaces) == 0 {
		return nil
	}
	out := make(map[string]float64, len(report.Namespaces))
	for _, ns := range report.Namespaces {
		out[ns.Name] = ns.MonthlyCost / 730.0
	}
	return out
}
