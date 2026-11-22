package budget

import (
	"math"
	"sort"
	"time"
)

// CompareWindows compares the trailing window [asOf-windowDays, asOf) with the
// immediately preceding equal-length window. A window with a previous cost of
// zero is not comparable (percent is undefined). Differing coverage is only a
// note: the windows are never backfilled to match. Namespaces present only in
// the current window are marked new.
func CompareWindows(records []NormRecord, asOf time.Time, windowDays int, gapThreshold time.Duration) WindowComparison {
	asOf = asOf.UTC()
	if windowDays <= 0 {
		windowDays = DefaultWindowDays
	}
	span := time.Duration(windowDays) * 24 * time.Hour

	cur := Integrate(records, asOf.Add(-span), asOf, gapThreshold)
	prev := Integrate(records, asOf.Add(-2*span), asOf.Add(-span), gapThreshold)

	stats := func(a Accrual) WindowStats {
		return WindowStats{
			Start:      a.Start,
			End:        a.End,
			Cost:       a.KnownCost,
			Coverage:   a.Coverage,
			KnownHours: a.KnownHours,
			GapHours:   a.GapHours,
			Namespaces: a.Namespaces,
		}
	}

	cmp := WindowComparison{
		WindowDays:   windowDays,
		Current:      stats(cur),
		Previous:     stats(prev),
		ChangeAmount: cur.KnownCost - prev.KnownCost,
		Comparable:   prev.KnownCost > 0,
	}
	if cmp.Comparable {
		cmp.ChangePercent = (cur.KnownCost - prev.KnownCost) / prev.KnownCost * 100
	}
	if math.Abs(cur.Coverage-prev.Coverage) > 0.02 {
		cmp.CoverageDiffer = true
		cmp.CoverageNote = "coverage differs between windows; values are reported as measured and were not backfilled"
	}

	names := map[string]bool{}
	for k := range cur.Namespaces {
		names[k] = true
	}
	for k := range prev.Namespaces {
		names[k] = true
	}
	// A namespace seen only in one window is still compared; "new" means absent
	// from the previous window entirely.
	for name := range names {
		c := cur.Namespaces[name]
		p := prev.Namespaces[name]
		nc := NamespaceChange{Namespace: name, Current: c, Previous: p, ChangeAmount: c - p}
		if p > 0 {
			nc.ChangePercent = (c - p) / p * 100
			nc.Comparable = true
		} else {
			nc.Comparable = false
			if _, ok := cur.Namespaces[name]; ok {
				nc.New = true
			}
			nc.Note = "previous window has zero cost; percent change is not comparable"
		}
		cmp.Namespaces = append(cmp.Namespaces, nc)
	}
	sort.Slice(cmp.Namespaces, func(i, j int) bool {
		return cmp.Namespaces[i].Current > cmp.Namespaces[j].Current
	})
	return cmp
}

// ValidateBudget enforces positive amounts and strictly ascending positive
// thresholds. It returns a descriptive error rather than silently fixing input.
func ValidateBudget(b Budget) error {
	if b.Amount <= 0 || math.IsNaN(b.Amount) || math.IsInf(b.Amount, 0) {
		return &InvalidBudgetError{Budget: b, Reason: "amount must be a positive finite number"}
	}
	if len(b.Thresholds) == 0 {
		return &InvalidBudgetError{Budget: b, Reason: "at least one threshold is required"}
	}
	prev := 0.0
	for i, t := range b.Thresholds {
		if t <= 0 || math.IsNaN(t) || math.IsInf(t, 0) {
			return &InvalidBudgetError{Budget: b, Reason: "thresholds must be positive finite fractions"}
		}
		if i > 0 && t <= prev {
			return &InvalidBudgetError{Budget: b, Reason: "thresholds must be strictly ascending"}
		}
		prev = t
	}
	return nil
}

// InvalidBudgetError reports a malformed budget configuration.
type InvalidBudgetError struct {
	Budget Budget
	Reason string
}

func (e *InvalidBudgetError) Error() string {
	return "invalid budget " + quote(e.Budget.Name) + ": " + e.Reason
}

func quote(s string) string {
	if s == "" {
		return "(unnamed)"
	}
	return "\"" + s + "\""
}

// EvaluateBudget applies a validated budget to the accrual and forecast and
// returns the structured status. Threshold comparison is >= (equality reaches).
//
// spend/forecast are selected by budget scope: a namespace budget uses that
// namespace's accrual, otherwise the cluster total is used.
func EvaluateBudget(b Budget, accrual Accrual, forecast Forecast, minCoverage float64) BudgetStatus {
	spend := accrual.KnownCost
	if b.Namespace != "" {
		spend = accrual.Namespaces[b.Namespace]
	}
	fc := forecast.ProjectedTotal
	if b.Namespace != "" {
		// A namespace budget projects that namespace, not the cluster. When no
		// per-namespace estimate exists, fall back to its known accrual rather
		// than borrowing the cluster-wide rate.
		if v, ok := forecast.NamespaceProjected[b.Namespace]; ok {
			fc = v
		} else {
			fc = spend
		}
	}

	st := BudgetStatus{
		Name:             b.Name,
		Namespace:        b.Namespace,
		Amount:           b.Amount,
		Spend:            spend,
		Forecast:         fc,
		Coverage:         accrual.Coverage,
		Sufficient:       accrual.Coverage >= minCoverage,
		ThresholdReached: false,
	}

	if b.Amount > 0 {
		st.SpendPercent = spend / b.Amount * 100
		st.ForecastPercent = fc / b.Amount * 100
	}

	// >= reaches. Thresholds ascending.
	for _, t := range b.Thresholds {
		if spend >= t*b.Amount {
			st.ReachedThreshold = t
			st.ThresholdReached = true
		}
	}
	// Forecast overspend uses the top threshold and a total above the amount.
	if fc > b.Amount {
		st.ForecastOverspend = true
		st.ForecastThreshold = b.Thresholds[len(b.Thresholds)-1]
	}

	// Credible range brackets the forecast with and without estimated gap cost,
	// scaled to the budget's scope so a namespace budget does not inherit the
	// cluster-wide gap estimate.
	gapEstimate := forecast.GapEstimatedCost
	if b.Namespace != "" {
		gapEstimate = 0
		if accrual.KnownCost > 0 {
			gapEstimate = forecast.GapEstimatedCost * (spend / accrual.KnownCost)
		}
	}
	st.CredibleLow = fc
	st.CredibleHigh = fc + gapEstimate
	if st.CredibleHigh < st.CredibleLow {
		st.CredibleLow, st.CredibleHigh = st.CredibleHigh, st.CredibleLow
	}

	switch {
	case spend > b.Amount:
		st.State = StateOverspent
		st.ReachedThreshold = 1
		st.ThresholdReached = true
		st.Explanation = "spend already exceeds the budget amount"
	case st.ForecastOverspend && st.Sufficient:
		st.State = StateForecastOverspent
		st.Explanation = "projected calendar-month total exceeds the budget amount"
	case st.ForecastOverspend && !st.Sufficient:
		st.State = StateInsufficient
		st.Explanation = "projection suggests overspend but coverage is too low to confirm it"
	case st.ThresholdReached && st.Sufficient:
		st.State = StateNearBudget
		st.Explanation = "spend reached a configured budget threshold"
	case st.ThresholdReached && !st.Sufficient:
		st.State = StateInsufficient
		st.Explanation = "a threshold was reached but coverage is too low to trust the conclusion"
	case !st.Sufficient:
		st.State = StateInsufficient
		st.Explanation = "coverage below minimum; cannot conclude the budget is on track"
	default:
		st.State = StateNormal
		st.Explanation = "spend and forecast are within budget"
	}

	st.NotificationLevel = stateLevel(st.State)
	return st
}

func stateLevel(state string) int {
	switch state {
	case StateOverspent:
		return LevelOverspent
	case StateForecastOverspent:
		return LevelForecastOverspend
	case StateNearBudget:
		return LevelNearBudget
	default:
		return LevelNormal
	}
}

// HighestLevel returns the maximum notification level across statuses and the
// cause (budget name) at that level. equalState prefers a higher state when
// levels tie.
func HighestLevel(statuses []BudgetStatus) (int, string) {
	best := LevelNormal
	cause := ""
	for _, s := range statuses {
		lvl := s.NotificationLevel
		if lvl > best {
			best = lvl
			cause = s.Name
		}
	}
	return best, cause
}
