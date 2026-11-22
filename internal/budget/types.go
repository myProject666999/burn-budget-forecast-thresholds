// Package budget adds historical hourly-cost snapshots, month-to-date accrual,
// calendar-month forecasting, adjacent-window trends, and budget threshold
// status to burn.
//
// The package is deliberately storage-agnostic: it depends only on the local
// filesystem (FileStore) or memory (MemoryStore), and never on an external
// service. It also never reads the wall clock internally: the evaluation instant
// is supplied via Options.Now or an injected Clock (EvaluateAt).
//
// # Semantics
//
// Accrual: snapshots are point observations of the total hourly cost. Between
// two adjacent snapshots the earlier observation is held constant and
// integrated over the interval (piecewise-constant). Intervals longer than the
// configured gap threshold are NOT integrated; they are reported as gaps and
// reduce coverage. Time before the first snapshot in the window is always an
// unknown gap, and trailing time after the last snapshot is integrated only
// when it is no longer than the gap threshold. A first run in the middle of a
// month therefore never pretends the month started at zero.
//
// Forecast: known month-to-date accrual (gaps excluded) plus remaining calendar
// hours in the UTC month multiplied by the forecast rate. Month length comes
// from the calendar (28-31 days, 29 in a leap February); 730 is never used.
//
// Thresholds: a configured fractional threshold T is reached when
// spendPercent >= T. Equality counts. Budgets and thresholds must be positive
// and thresholds strictly ascending.
//
// Budget state priority: overspent (actual) > forecast overspent > near budget
// > normal. When coverage is below the configured minimum, a result that is not
// already definitively overspent is reported as insufficient_data with a
// credible range instead of "normal"; low-coverage data must never publish a
// confident under-budget conclusion.
//
// Notifications: the stored level is the last observed level. A higher level
// notifies once; the same level does not repeat; a decrease is recorded
// silently, so a later increase notifies again. A new UTC month resets the
// stored level. Hysteresis is intentionally not implemented yet (the decision
// is still open); level decreases are recorded immediately.
package budget

import "time"

// CurrentVersion is the snapshot file format version.
const CurrentVersion = 1

// Default configuration values. Trend window and minimum sample count are
// provisional (still to be confirmed) and are exposed as configuration.
const (
	DefaultGapThreshold    = 30 * time.Minute
	DefaultTrendWindowDays = 7
	DefaultMinTrendSamples = 3
	DefaultWindowDays      = 7
	DefaultMinCoverage     = 0.9
)

// Record is one point observation of the cluster's total hourly cost.
type Record struct {
	Timestamp  time.Time          `json:"timestamp"`
	HourlyCost float64            `json:"hourly_cost"`
	Namespaces map[string]float64 `json:"namespaces,omitempty"`
}

// Snapshot is the persisted state: all records plus the last notification level.
type Snapshot struct {
	Version      int               `json:"version"`
	Records      []Record          `json:"records"`
	Notification NotificationState `json:"notification"`
}

// NotificationState is persisted with the snapshots.
type NotificationState struct {
	Month     string    `json:"month"` // UTC calendar month, "2006-01"
	Level     int       `json:"level"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Gap is an interval without a usable observation.
type Gap struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Hours float64   `json:"hours"`
}

// Accrual is the piecewise-constant integration result over a window.
type Accrual struct {
	Start        time.Time          `json:"start"`
	End          time.Time          `json:"end"`
	KnownCost    float64            `json:"known_cost"`
	KnownHours   float64            `json:"known_hours"`
	ElapsedHours float64            `json:"elapsed_hours"`
	Coverage     float64            `json:"coverage"`
	GapHours     float64            `json:"gap_hours"`
	Gaps         []Gap              `json:"gaps,omitempty"`
	Namespaces   map[string]float64 `json:"namespaces,omitempty"`
	PartialStart bool               `json:"partial_start"`
	ObservedFrom time.Time          `json:"observed_from"`
	Duplicates   int                `json:"duplicates,omitempty"`
	OutOfOrder   int                `json:"out_of_order,omitempty"`
	Invalid      int                `json:"invalid_ignored,omitempty"`
	AfterHorizon int                `json:"after_horizon_ignored,omitempty"`
}

// RateEstimate describes how the forecast rate was chosen.
type RateEstimate struct {
	Current     float64 `json:"current"`
	Rate        float64 `json:"rate"`
	SlopePerDay float64 `json:"slope_per_day"`
	Source      string  `json:"source"` // "current" or "trend"
	SampleDays  int     `json:"sample_days"`
	Explanation string  `json:"explanation"`
}

// Forecast is the calendar-month projection.
type Forecast struct {
	KnownCumulative  float64   `json:"known_cumulative"`
	Rate             float64   `json:"rate"`
	RateSource       string    `json:"rate_source"`
	RemainingHours   float64   `json:"remaining_hours"`
	ProjectedTotal   float64   `json:"projected_total"`
	MonthDays        int       `json:"month_days"`
	MonthEnd         time.Time `json:"month_end"`
	GapEstimatedCost float64   `json:"gap_estimated_cost"` // separate; NOT part of known cumulative
	Limitation       string    `json:"limitation,omitempty"`

	// NamespaceProjected maps a namespace to its projected month total, used
	// by namespace-scoped budgets. Namespaces without an estimate fall back to
	// their known accrual.
	NamespaceProjected map[string]float64 `json:"namespace_projected,omitempty"`
}

// WindowStats summarizes one comparison window.
type WindowStats struct {
	Start      time.Time          `json:"start"`
	End        time.Time          `json:"end"`
	Cost       float64            `json:"cost"`
	Coverage   float64            `json:"coverage"`
	KnownHours float64            `json:"known_hours"`
	GapHours   float64            `json:"gap_hours"`
	Namespaces map[string]float64 `json:"-"`
	Seen       map[string]bool    `json:"-"`
}

// NamespaceChange is a per-namespace window comparison entry.
type NamespaceChange struct {
	Namespace     string  `json:"namespace"`
	Current       float64 `json:"current"`
	Previous      float64 `json:"previous"`
	ChangeAmount  float64 `json:"change_amount"`
	ChangePercent float64 `json:"change_percent"`
	Comparable    bool    `json:"comparable"`
	New           bool    `json:"new"`
	Note          string  `json:"note,omitempty"`
}

// WindowComparison compares two adjacent equal-length windows.
type WindowComparison struct {
	WindowDays     int               `json:"window_days"`
	Current        WindowStats       `json:"current"`
	Previous       WindowStats       `json:"previous"`
	ChangeAmount   float64           `json:"change_amount"`
	ChangePercent  float64           `json:"change_percent"`
	Comparable     bool              `json:"comparable"`
	CoverageDiffer bool              `json:"coverage_differ"`
	CoverageNote   string            `json:"coverage_note,omitempty"`
	Namespaces     []NamespaceChange `json:"namespaces,omitempty"`
}

// Budget is one configured monthly budget. Namespace empty means cluster-wide.
type Budget struct {
	Name       string    `json:"name,omitempty"`
	Namespace  string    `json:"namespace,omitempty"`
	Amount     float64   `json:"amount"`
	Thresholds []float64 `json:"thresholds"`
}

// Budget states, in priority order.
const (
	StateOverspent         = "overspent"
	StateForecastOverspent = "forecast_overspent"
	StateNearBudget        = "near_budget"
	StateNormal            = "normal"
	StateInsufficient      = "insufficient_data"
)

// BudgetStatus is the structured decision for one budget.
type BudgetStatus struct {
	Name              string  `json:"name"`
	Namespace         string  `json:"namespace,omitempty"`
	Amount            float64 `json:"amount"`
	Spend             float64 `json:"spend"`
	Forecast          float64 `json:"forecast"`
	SpendPercent      float64 `json:"spend_percent"`
	ForecastPercent   float64 `json:"forecast_percent"`
	State             string  `json:"state"`
	ReachedThreshold  float64 `json:"reached_threshold,omitempty"`
	ThresholdReached  bool    `json:"threshold_reached"`
	ForecastThreshold float64 `json:"forecast_threshold,omitempty"`
	ForecastOverspend bool    `json:"forecast_overspend"`
	Coverage          float64 `json:"coverage"`
	Sufficient        bool    `json:"sufficient"`
	CredibleLow       float64 `json:"credible_low"`
	CredibleHigh      float64 `json:"credible_high"`
	Explanation       string  `json:"explanation"`
	NotificationLevel int     `json:"notification_level"`
}

// NotificationDecision is the dedupe/hysteresis decision for this evaluation.
type NotificationDecision struct {
	PreviousLevel int               `json:"previous_level"`
	NewLevel      int               `json:"new_level"`
	MonthReset    bool              `json:"month_reset"`
	Notify        bool              `json:"notify"`
	Hold          bool              `json:"hold"` // insufficient data at normal level: keep prior
	LevelName     string            `json:"level_name"`
	Cause         string            `json:"cause,omitempty"`
	Reason        string            `json:"reason"`
	State         NotificationState `json:"-"`
}

// DataQuality summarizes how trustworthy this evaluation is.
type DataQuality struct {
	Coverage       float64 `json:"coverage"`
	Sufficient     bool    `json:"sufficient"`
	MinCoverage    float64 `json:"min_coverage"`
	PartialStart   bool    `json:"partial_start"`
	Duplicates     int     `json:"duplicates,omitempty"`
	OutOfOrder     int     `json:"out_of_order,omitempty"`
	FutureIgnored  int     `json:"future_ignored,omitempty"`
	InvalidIgnored int     `json:"invalid_ignored,omitempty"`
	GapHours       float64 `json:"gap_hours"`
}

// Result is the complete structured output of one evaluation.
type Result struct {
	GeneratedAt  time.Time            `json:"generated_at"`
	Month        string               `json:"month"`
	MonthStart   time.Time            `json:"month_start"`
	MonthEnd     time.Time            `json:"month_end"`
	AsOf         time.Time            `json:"as_of"`
	Accrual      Accrual              `json:"accrual"`
	Rate         RateEstimate         `json:"rate"`
	Forecast     Forecast             `json:"forecast"`
	Trend        WindowComparison     `json:"trend"`
	Budgets      []BudgetStatus       `json:"budgets,omitempty"`
	Notification NotificationDecision `json:"notification"`
	DataQuality  DataQuality          `json:"data_quality"`
	Warnings     []string             `json:"warnings,omitempty"`

	// observe is the observation this evaluation produced. It is persisted by
	// Save and is not part of the JSON result.
	observe Record `json:"-"`
}

// Notification levels.
const (
	LevelNormal            = 0
	LevelNearBudget        = 1
	LevelForecastOverspend = 2
	LevelOverspent         = 3
)

// LevelName returns a stable label for a notification level.
func LevelName(level int) string {
	switch level {
	case LevelNearBudget:
		return "near_budget"
	case LevelForecastOverspend:
		return "forecast_overspent"
	case LevelOverspent:
		return "overspent"
	default:
		return "normal"
	}
}

// MonthBounds returns the UTC calendar month containing t.
func MonthBounds(t time.Time) (start, end time.Time) {
	t = t.UTC()
	start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	return start, end
}

// MonthKey returns the UTC month key used for notification reset.
func MonthKey(t time.Time) string {
	return t.UTC().Format("2006-01")
}
