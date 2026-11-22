package budget

import (
	"fmt"
	"strings"
)

// FormatText renders a result as a plain-text block for CLI and Slack text
// responses. It shows the same facts as the structured JSON, including gaps,
// coverage, partial-start limitation, comparable/uncomparable trend, and
// per-budget status.
func FormatText(r *Result) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Month-to-date (%s, as of %s UTC)\n", r.Month, r.AsOf.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "Known spend: $%.2f | coverage: %.0f%% | gaps: %.1fh\n",
		r.Accrual.KnownCost, r.DataQuality.Coverage*100, r.Accrual.GapHours)
	fmt.Fprintf(&b, "Rate: $%.4f/hr (%s) | forecast: $%.2f (%d-day month)\n",
		r.Rate.Rate, r.Rate.Source, r.Forecast.ProjectedTotal, r.Forecast.MonthDays)
	if r.Forecast.GapEstimatedCost > 0 {
		fmt.Fprintf(&b, "Estimated cost in unobserved gaps (separate): $%.2f\n", r.Forecast.GapEstimatedCost)
	}
	if r.Forecast.Limitation != "" {
		fmt.Fprintf(&b, "Note: %s\n", r.Forecast.Limitation)
	}

	if len(r.Budgets) > 0 {
		b.WriteString("\nBudgets\n")
		for _, s := range r.Budgets {
			label := s.Name
			if label == "" {
				label = "budget"
			}
			fmt.Fprintf(&b, "  %-12s %-18s $%.2f / $%.2f (%.0f%%), forecast $%.2f (%.0f%%)\n",
				label, s.State, s.Spend, s.Amount, s.SpendPercent, s.Forecast, s.ForecastPercent)
			if s.State == StateInsufficient {
				fmt.Fprintf(&b, "               credible range $%.2f–$%.2f\n", s.CredibleLow, s.CredibleHigh)
			}
		}
	}

	c := r.Trend
	b.WriteString("\nTrend\n")
	if c.Comparable {
		fmt.Fprintf(&b, "  last %dd $%.2f vs previous %dd $%.2f: %+.2f (%+.1f%%)\n",
			c.WindowDays, c.Current.Cost, c.WindowDays, c.Previous.Cost, c.ChangeAmount, c.ChangePercent)
	} else {
		fmt.Fprintf(&b, "  last %dd $%.2f: previous window $0, change not comparable\n",
			c.WindowDays, c.Current.Cost)
	}
	if c.CoverageDiffer {
		fmt.Fprintf(&b, "  coverage differs (%.0f%% vs %.0f%%); not backfilled\n",
			c.Current.Coverage*100, c.Previous.Coverage*100)
	}
	for _, ns := range c.Namespaces {
		if ns.New {
			fmt.Fprintf(&b, "  new in window: %s\n", ns.Namespace)
		}
	}

	if len(r.Warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	return b.String()
}
