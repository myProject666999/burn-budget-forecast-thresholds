package slack

import (
	"fmt"
	"strings"

	"github.com/tanrikuluozlem/burn/internal/budget"
)

// FormatBudgetAlert returns a message when the evaluation warrants a new
// notification (a level increase, or the first evaluation of a new month at a
// non-normal level), and nil otherwise. It is separate from the recurring cost
// report so a same-level run does not re-alert.
func FormatBudgetAlert(r *budget.Result) *Message {
	if r == nil || !r.Notification.Notify {
		return nil
	}
	header := fmt.Sprintf(":warning: *Budget alert: %s*", levelTitle(r.Notification.NewLevel))
	blocks := []Block{{
		Type: "header",
		Text: &TextObject{Type: "plain_text", Text: "Budget alert"},
	}}
	var lines []string
	lines = append(lines, header)
	if r.Notification.Cause != "" {
		lines = append(lines, fmt.Sprintf("Triggered by: *%s*", r.Notification.Cause))
	}
	for _, b := range r.Budgets {
		label := b.Name
		if label == "" {
			label = "budget"
		}
		lines = append(lines, fmt.Sprintf("• *%s*: %s — $%.2f of $%.2f (%.0f%%), forecast $%.2f (%.0f%%)",
			label, stateLabel(b.State), b.Spend, b.Amount, b.SpendPercent, b.Forecast, b.ForecastPercent))
	}
	blocks = append(blocks, splitSectionBlocks(strings.Join(lines, "\n"))...)
	return &Message{Blocks: blocks}
}

func levelTitle(level int) string {
	switch level {
	case budget.LevelOverspent:
		return "overspent"
	case budget.LevelForecastOverspend:
		return "forecast overspend"
	case budget.LevelNearBudget:
		return "near budget"
	default:
		return "normal"
	}
}

// formatBudgetBlocks renders the month-to-date / forecast / trend / budget
// status section. It never claims certainty that the data cannot support: low
// coverage is surfaced explicitly.
func formatBudgetBlocks(r *budget.Result) []Block {
	var lines []string

	header := fmt.Sprintf("*Month-to-date (%s)*", r.Month)
	lines = append(lines, header)

	coverage := r.DataQuality.Coverage * 100
	lines = append(lines, fmt.Sprintf("Known spend: $%.2f (%.0f%% coverage, %.1fh gaps excluded)",
		r.Accrual.KnownCost, coverage, r.Accrual.GapHours))
	lines = append(lines, fmt.Sprintf("Rate: $%.4f/hr (%s) → forecast: $%.2f this month",
		r.Rate.Rate, r.Rate.Source, r.Forecast.ProjectedTotal))
	if r.DataQuality.PartialStart {
		lines = append(lines, "_Month started before the first snapshot; earlier data is unknown, not zero._")
	}

	for _, b := range r.Budgets {
		label := b.Name
		if label == "" {
			label = "budget"
		}
		line := fmt.Sprintf("• *%s*: %s — $%.2f of $%.2f (%.0f%%), forecast $%.2f (%.0f%%)",
			label, stateLabel(b.State), b.Spend, b.Amount, b.SpendPercent, b.Forecast, b.ForecastPercent)
		if b.State == budget.StateInsufficient {
			line += fmt.Sprintf(" — credible $%.2f–$%.2f", b.CredibleLow, b.CredibleHigh)
		}
		lines = append(lines, line)
	}

	// Adjacent-window trend.
	c := r.Trend
	if c.Comparable {
		lines = append(lines, fmt.Sprintf("\n*Last %dd vs previous %dd:* $%.2f → $%.2f (%+.2f, %+.1f%%)",
			c.WindowDays, c.WindowDays, c.Previous.Cost, c.Current.Cost, c.ChangeAmount, c.ChangePercent))
	} else {
		lines = append(lines, fmt.Sprintf("\n*Last %dd:* $%.2f — previous window $0, change not comparable",
			c.WindowDays, c.Current.Cost))
	}
	if c.CoverageDiffer {
		lines = append(lines, fmt.Sprintf("_Coverage differs (%.0f%% vs %.0f%%); values not backfilled._",
			c.Current.Coverage*100, c.Previous.Coverage*100))
	}
	var fresh []string
	for _, ns := range c.Namespaces {
		if ns.New {
			fresh = append(fresh, ns.Namespace)
		}
	}
	if len(fresh) > 0 {
		lines = append(lines, "_New in window: "+strings.Join(fresh, ", ")+"_")
	}

	if len(r.Warnings) > 0 {
		lines = append(lines, "\n"+strings.Join(r.Warnings, "\n"))
	}

	return splitSectionBlocks(strings.Join(lines, "\n"))
}

func stateLabel(state string) string {
	switch state {
	case budget.StateOverspent:
		return ":red_circle: overspent"
	case budget.StateForecastOverspent:
		return ":large_orange_circle: forecast overspend"
	case budget.StateNearBudget:
		return ":large_yellow_circle: near budget"
	case budget.StateInsufficient:
		return ":grey_question: insufficient data"
	default:
		return ":white_check_mark: normal"
	}
}
