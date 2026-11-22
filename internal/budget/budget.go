package budget

import (
	"fmt"
	"math"
	"time"
)

// Options configures an evaluation.
type Options struct {
	Now             time.Time // evaluation instant (UTC); required
	GapThreshold    time.Duration
	TrendDays       int
	MinTrendSamples int
	WindowDays      int
	MinCoverage     float64
	Budgets         []Budget

	// Observe, when set, adds a live observation at Now to the snapshot used
	// for evaluation (in memory; persistence still goes through Save). It lets
	// a first run in the middle of a month seed data without pretending the
	// month began at zero. The evaluation itself integrates only stored
	// records, so a single observation still yields near-zero known cost.
	Observe *Record

	// Namespaces maps a namespace to its hourly cost. It is used only together
	// with Observe.
	Namespaces map[string]float64
}

// DefaultOptions returns Options with the package defaults applied.
func DefaultOptions(now time.Time) Options {
	return Options{
		Now:             now,
		GapThreshold:    DefaultGapThreshold,
		TrendDays:       DefaultTrendWindowDays,
		MinTrendSamples: DefaultMinTrendSamples,
		WindowDays:      DefaultWindowDays,
		MinCoverage:     DefaultMinCoverage,
	}
}

func (o *Options) normalize() error {
	if o.Now.IsZero() {
		return fmt.Errorf("budget: Now is required")
	}
	o.Now = o.Now.UTC()
	if o.GapThreshold <= 0 {
		o.GapThreshold = DefaultGapThreshold
	}
	if o.TrendDays <= 0 {
		o.TrendDays = DefaultTrendWindowDays
	}
	if o.MinTrendSamples <= 0 {
		o.MinTrendSamples = DefaultMinTrendSamples
	}
	if o.WindowDays <= 0 {
		o.WindowDays = DefaultWindowDays
	}
	if o.MinCoverage < 0 || o.MinCoverage > 1 || math.IsNaN(o.MinCoverage) {
		return fmt.Errorf("budget: MinCoverage must be between 0 and 1")
	}
	for _, b := range o.Budgets {
		if err := ValidateBudget(b); err != nil {
			return err
		}
	}
	return nil
}

// EvaluateAt behaves like Evaluate but takes the evaluation instant from clock,
// leaving opts.Now unset. Callers that already have a time use Evaluate.
func EvaluateAt(store Store, clock Clock, opts Options) (*Result, error) {
	if clock == nil {
		clock = SystemClock()
	}
	opts.Now = clock.Now()
	return Evaluate(store, opts)
}

// Evaluate loads the snapshot from store, accrues the month to date, forecasts
// the calendar month, compares adjacent windows, evaluates configured budgets,
// and decides whether to notify. It does not write anything; callers use
// Store.Append (via Save) with the returned record and decision state.
//
// The current instant comes from opts.Now, never from time.Now directly, so
// results are reproducible.
func Evaluate(store Store, opts Options) (*Result, error) {
	if err := opts.normalize(); err != nil {
		return nil, err
	}
	snap, err := store.Load()
	if err != nil {
		return nil, err
	}
	recs := snap.Records
	if opts.Observe != nil {
		obs := *opts.Observe
		if obs.Timestamp.IsZero() {
			obs.Timestamp = opts.Now
		}
		obs.Namespaces = copyMap(opts.Namespaces)
		recs = append(append([]Record(nil), recs...), obs)
	}
	return evaluateSnapshot(snap.Notification, recs, opts), nil
}

func evaluateSnapshot(notifState NotificationState, rawRecords []Record, opts Options) *Result {
	asOf := opts.Now
	monthStart, monthEnd := MonthBounds(asOf)

	records, stats := Normalize(rawRecords, asOf)

	var current float64
	if n := len(records); n > 0 {
		current = records[n-1].HourlyCost
	}

	accrual := Integrate(records, monthStart, asOf, opts.GapThreshold)
	accrual.Duplicates = stats.Duplicates
	accrual.OutOfOrder = stats.OutOfOrder
	accrual.Invalid = stats.InvalidIgnored
	accrual.AfterHorizon = stats.FutureIgnored

	rate := EstimateRate(records, asOf, current, opts.TrendDays, opts.MinTrendSamples)
	forecast := ForecastMonth(asOf, accrual, rate)
	forecast.NamespaceProjected = namespaceForecasts(records, asOf, accrual, forecast.RemainingHours)
	trend := CompareWindows(records, asOf, opts.WindowDays, opts.GapThreshold)

	result := &Result{
		GeneratedAt: asOf,
		Month:       MonthKey(asOf),
		MonthStart:  monthStart,
		MonthEnd:    monthEnd,
		AsOf:        asOf,
		Accrual:     accrual,
		Rate:        rate,
		Forecast:    forecast,
		Trend:       trend,
		DataQuality: DataQuality{
			Coverage:       accrual.Coverage,
			Sufficient:     accrual.Coverage >= opts.MinCoverage,
			MinCoverage:    opts.MinCoverage,
			PartialStart:   accrual.PartialStart,
			Duplicates:     stats.Duplicates,
			OutOfOrder:     stats.OutOfOrder,
			FutureIgnored:  stats.FutureIgnored,
			InvalidIgnored: stats.InvalidIgnored,
			GapHours:       accrual.GapHours,
		},
	}

	for _, b := range opts.Budgets {
		result.Budgets = append(result.Budgets, EvaluateBudget(b, accrual, forecast, opts.MinCoverage))
	}

	if len(records) == 0 {
		result.Warnings = append(result.Warnings, "no snapshots yet; accrued cost and forecast are unavailable until the first snapshot is recorded")
	}
	if accrual.PartialStart && len(records) > 0 {
		result.Warnings = append(result.Warnings, "the month began before the first snapshot; missing early-month data is unknown, not zero")
	}
	if accrual.GapHours > 0 && len(records) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%.1f hours of gaps are excluded from known cumulative; they are estimated separately", accrual.GapHours))
	}
	if stats.Duplicates > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d duplicate timestamps collapsed to the last value", stats.Duplicates))
	}
	if stats.OutOfOrder > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d out-of-order records were sorted before integration", stats.OutOfOrder))
	}
	if stats.FutureIgnored > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d future-dated records were ignored", stats.FutureIgnored))
	}
	if stats.InvalidIgnored > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d invalid records were ignored", stats.InvalidIgnored))
	}

	level, cause := HighestLevel(result.Budgets)
	result.Notification = DecideNotification(notifState, result.Month, level, !result.DataQuality.Sufficient, asOf)
	result.Notification.Cause = cause

	// Record the observation that produced this evaluation so the next run
	// integrates from here. The caller decides whether to persist. When a live
	// observation was supplied, it is recorded verbatim; otherwise the last
	// known hourly cost is carried forward as the observation for this instant.
	result.observe = Record{
		Timestamp:  asOf,
		HourlyCost: current,
		Namespaces: namespaceHourly(records, asOf),
	}
	if opts.Observe != nil {
		result.observe.HourlyCost = opts.Observe.HourlyCost
		if opts.Namespaces != nil {
			result.observe.Namespaces = copyMap(opts.Namespaces)
		}
	}
	return result
}

// namespaceHourly returns the namespace cost detail of the most recent
// observation, if the snapshot carries it.
func namespaceHourly(records []NormRecord, asOf time.Time) map[string]float64 {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Timestamp.Equal(asOf) || !records[i].Timestamp.After(asOf) {
			return copyMap(records[i].Namespaces)
		}
	}
	return nil
}

// namespaceForecasts projects each namespace's month total as its known accrual
// plus the remaining hours at its current hourly share. The current share comes
// from the most recent observation carrying namespace detail. Namespaces with
// no detail are simply absent; a namespace budget then falls back to its known
// accrual rather than borrowing the cluster-wide rate.
func namespaceForecasts(records []NormRecord, asOf time.Time, accrual Accrual, remainingHours float64) map[string]float64 {
	latest := namespaceHourly(records, asOf)
	if len(latest) == 0 && len(accrual.Namespaces) == 0 {
		return nil
	}
	out := make(map[string]float64, len(latest)+len(accrual.Namespaces))
	for ns, known := range accrual.Namespaces {
		out[ns] = known
	}
	for ns, hourly := range latest {
		out[ns] += remainingHours * hourly
	}
	return out
}

// Record returns the observation this evaluation would append.
func (r *Result) Record() Record { return r.observe }

// Save records the observation for this evaluation and persists the
// notification decision. Calling Save on a result with no prior snapshot still
// appends the current observation, so the next run has a starting point.
func Save(store Store, result *Result) error {
	if store == nil {
		return nil
	}
	return store.Append(result.observe, result.Notification.State)
}
