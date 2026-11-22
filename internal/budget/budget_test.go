package budget

import (
	"testing"
	"time"
)

func TestValidateBudgetRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		b    Budget
	}{
		{"zero amount", Budget{Name: "a", Amount: 0, Thresholds: []float64{0.8}}},
		{"negative amount", Budget{Name: "b", Amount: -10, Thresholds: []float64{0.8}}},
		{"no thresholds", Budget{Name: "c", Amount: 100}},
		{"zero threshold", Budget{Name: "d", Amount: 100, Thresholds: []float64{0}}},
		{"negative threshold", Budget{Name: "e", Amount: 100, Thresholds: []float64{-0.5}}},
		{"descending thresholds", Budget{Name: "f", Amount: 100, Thresholds: []float64{1.0, 0.8}}},
		{"duplicate thresholds", Budget{Name: "g", Amount: 100, Thresholds: []float64{0.8, 0.8}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateBudget(tc.b); err == nil {
				t.Errorf("ValidateBudget(%+v) = nil, want error", tc.b)
			}
		})
	}
	if err := ValidateBudget(Budget{Name: "ok", Amount: 100, Thresholds: []float64{0.8, 1.0}}); err != nil {
		t.Errorf("valid budget rejected: %v", err)
	}
}

func TestThresholdEqualityReaches(t *testing.T) {
	// Spend exactly 80% of a $100 budget must reach the 80% threshold (>=).
	acc := Accrual{KnownCost: 80, Coverage: 1}
	forecast := Forecast{ProjectedTotal: 90, RemainingHours: 10, Rate: 1}
	b := Budget{Name: "team", Amount: 100, Thresholds: []float64{0.8, 1.0}}

	st := EvaluateBudget(b, acc, forecast, 0.9)
	if !st.ThresholdReached {
		t.Error("ThresholdReached = false at exactly 80%, want true")
	}
	approx(t, "ReachedThreshold", st.ReachedThreshold, 0.8, 1e-9)
	if st.State != StateNearBudget {
		t.Errorf("State = %q, want %q", st.State, StateNearBudget)
	}
	approx(t, "SpendPercent", st.SpendPercent, 80, 1e-9)
}

func TestBudgetStatePriority(t *testing.T) {
	forecast := func(v float64) Forecast { return Forecast{ProjectedTotal: v, RemainingHours: 0, Rate: 0} }
	b := Budget{Name: "x", Amount: 100, Thresholds: []float64{0.8, 1.0}}

	// Already over the amount: overspent even with a small projection delta.
	st := EvaluateBudget(b, Accrual{KnownCost: 101, Coverage: 1}, forecast(101), 0.9)
	if st.State != StateOverspent {
		t.Errorf("State = %q, want %q", st.State, StateOverspent)
	}

	// Spent 50 but forecast over 100: forecast overspend.
	st = EvaluateBudget(b, Accrual{KnownCost: 50, Coverage: 1}, forecast(120), 0.9)
	if st.State != StateForecastOverspent {
		t.Errorf("State = %q, want %q", st.State, StateForecastOverspent)
	}

	// Normal.
	st = EvaluateBudget(b, Accrual{KnownCost: 40, Coverage: 1}, forecast(70), 0.9)
	if st.State != StateNormal {
		t.Errorf("State = %q, want %q", st.State, StateNormal)
	}
}

func TestLowCoverageNeverConcludesNormalOrCertainOverspend(t *testing.T) {
	b := Budget{Name: "x", Amount: 100, Thresholds: []float64{0.8, 1.0}}

	// Good projection but coverage too low: must not say normal.
	st := EvaluateBudget(b, Accrual{KnownCost: 10, Coverage: 0.2}, Forecast{ProjectedTotal: 40}, 0.9)
	if st.State != StateInsufficient {
		t.Errorf("State = %q, want %q", st.State, StateInsufficient)
	}
	if st.CredibleHigh <= st.CredibleLow && st.CredibleHigh != st.CredibleLow {
		t.Errorf("credible range inverted: %+v", st)
	}

	// Forecast over budget under low coverage: cannot publish certain overspend.
	st = EvaluateBudget(b, Accrual{KnownCost: 10, Coverage: 0.2}, Forecast{ProjectedTotal: 200}, 0.9)
	if st.State != StateInsufficient {
		t.Errorf("State = %q, want %q (insufficient, not forecast_overspent)", st.State, StateInsufficient)
	}
	if st.NotificationLevel == LevelForecastOverspend {
		t.Error("low-coverage forecast must not raise a forecast-overspend notification level")
	}

	// Actual spend over budget is definitive regardless of coverage.
	st = EvaluateBudget(b, Accrual{KnownCost: 150, Coverage: 0.2}, Forecast{ProjectedTotal: 200}, 0.9)
	if st.State != StateOverspent {
		t.Errorf("State = %q, want %q (actual overspend is definitive)", st.State, StateOverspent)
	}
}

func TestNamespaceBudgetUsesNamespaceAccrual(t *testing.T) {
	acc := Accrual{
		KnownCost: 300,
		Coverage:  1,
		Namespaces: map[string]float64{
			"app":   120,
			"infra": 180,
		},
	}
	forecast := Forecast{
		ProjectedTotal:     600,
		RemainingHours:     100,
		Rate:               1,
		NamespaceProjected: map[string]float64{"app": 220, "infra": 380},
	}
	b := Budget{Name: "app", Namespace: "app", Amount: 200, Thresholds: []float64{0.5}}

	st := EvaluateBudget(b, acc, forecast, 0.9)
	approx(t, "Spend", st.Spend, 120, 1e-9)
	// A namespace budget projects that namespace, not the cluster-wide total.
	approx(t, "Forecast", st.Forecast, 220, 1e-9)
	approx(t, "SpendPercent", st.SpendPercent, 60, 1e-9)
	if !st.ThresholdReached {
		t.Error("ThresholdReached = false at 60% >= 50%, want true")
	}
}

func TestNamespaceBudgetWithoutProjectionFallsBackToAccrual(t *testing.T) {
	acc := Accrual{KnownCost: 300, Coverage: 1, Namespaces: map[string]float64{"app": 120}}
	forecast := Forecast{ProjectedTotal: 900, RemainingHours: 100, Rate: 3} // no NamespaceProjected
	b := Budget{Name: "app", Namespace: "app", Amount: 200, Thresholds: []float64{0.5}}

	st := EvaluateBudget(b, acc, forecast, 0.9)
	// Without a namespace projection, use the namespace accrual — never the
	// cluster-wide forecast of $900.
	approx(t, "Forecast", st.Forecast, 120, 1e-9)
}

func TestNotificationDedupeAcrossRuns(t *testing.T) {
	now := utc(2026, 3, 10, 0, 0)
	monthKey := MonthKey(now)
	state := NotificationState{}

	// Normal: no notification.
	d := DecideNotification(state, monthKey, LevelNormal, false, now)
	if d.Notify {
		t.Fatal("normal level notified")
	}
	state = d.State

	// Rise to near budget: notify.
	d = DecideNotification(state, monthKey, LevelNearBudget, false, now)
	if !d.Notify {
		t.Fatal("rise to near_budget did not notify")
	}
	state = d.State

	// Same level again: no repeat.
	d = DecideNotification(state, monthKey, LevelNearBudget, false, now)
	if d.Notify {
		t.Fatal("same level notified twice")
	}
	state = d.State

	// Rise to overspent: notify.
	d = DecideNotification(state, monthKey, LevelOverspent, false, now)
	if !d.Notify {
		t.Fatal("rise to overspent did not notify")
	}
	state = d.State

	// Decrease to normal: silent but recorded.
	d = DecideNotification(state, monthKey, LevelNormal, false, now)
	if d.Notify {
		t.Fatal("decrease notified")
	}
	if d.NewLevel != LevelNormal {
		t.Fatalf("decrease not recorded: %+v", d)
	}
	state = d.State

	// Rise again after a decrease: notify again.
	d = DecideNotification(state, monthKey, LevelNearBudget, false, now)
	if !d.Notify {
		t.Fatal("re-rise after decrease did not notify")
	}
}

func TestNotificationInsufficientDataHoldsLevel(t *testing.T) {
	now := utc(2026, 3, 10, 0, 0)
	state := NotificationState{Month: MonthKey(now), Level: LevelOverspent}

	d := DecideNotification(state, MonthKey(now), LevelNormal, true, now)
	if d.Notify {
		t.Error("insufficient data must not clear an alert via a notify")
	}
	if d.NewLevel != LevelOverspent || !d.Hold {
		t.Errorf("expected hold at previous level, got %+v", d)
	}
}

func TestNotificationResetsAcrossMonth(t *testing.T) {
	prev := NotificationState{Month: "2026-03", Level: LevelOverspent}
	now := utc(2026, 4, 1, 0, 5)
	d := DecideNotification(prev, MonthKey(now), LevelNormal, false, now)
	if !d.MonthReset {
		t.Error("MonthReset = false, want true")
	}
	if d.NewLevel != LevelNormal {
		t.Errorf("NewLevel = %d, want normal", d.NewLevel)
	}
	if d.Notify {
		t.Error("month reset to normal should not notify")
	}

	// A budget already over at the start of a new month still notifies.
	d = DecideNotification(prev, MonthKey(now), LevelOverspent, false, now)
	if !d.Notify {
		t.Error("new month at overspent should notify once")
	}
}

func TestEvaluateFullResultWithMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	// Seed two observations so accrual has something to integrate.
	base := utc(2026, 3, 1, 0, 0)
	for h := 0; h < 24; h++ {
		_ = store.Append(Record{Timestamp: base.Add(time.Duration(h) * time.Hour), HourlyCost: 1}, NotificationState{})
	}

	asOf := utc(2026, 3, 2, 0, 0)
	opts := DefaultOptions(asOf)
	// Hourly snapshots need a gap threshold above one hour, or every interval
	// looks like a gap. 2h admits the cadence and still flags a missing hour.
	opts.GapThreshold = 2 * time.Hour
	opts.Budgets = []Budget{{Name: "cluster", Amount: 1000, Thresholds: []float64{0.8, 1.0}}}

	res, err := Evaluate(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	// 24 observations 00:00→23:00 cover the whole day at $1/hr.
	approx(t, "KnownCost", res.Accrual.KnownCost, 24, 1e-6)
	approx(t, "Coverage", res.Accrual.Coverage, 1, 1e-9)
	if len(res.Budgets) != 1 {
		t.Fatalf("budgets = %d, want 1", len(res.Budgets))
	}
	if res.Month != "2026-03" {
		t.Errorf("Month = %q, want 2026-03", res.Month)
	}

	// Record + save advances the store so the next run integrates further.
	if err := Save(store, res); err != nil {
		t.Fatal(err)
	}
	snap, _ := store.Load()
	if len(snap.Records) != 25 {
		t.Errorf("records = %d, want 25 after save", len(snap.Records))
	}
}

func TestEvaluateEmptyStoreWarnsNotClaimsZero(t *testing.T) {
	res, err := Evaluate(NewMemoryStore(), DefaultOptions(utc(2026, 3, 15, 12, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Accrual.KnownCost != 0 {
		t.Errorf("KnownCost = %v, want 0", res.Accrual.KnownCost)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a warning for an empty snapshot store")
	}
	if res.DataQuality.Sufficient {
		t.Error("empty store must be insufficient")
	}
}

func TestNamespaceBudgetCredibleRangeScaledNotCluster(t *testing.T) {
	acc := Accrual{KnownCost: 1000, Coverage: 0.5, Namespaces: map[string]float64{"app": 100}}
	forecast := Forecast{
		ProjectedTotal:     3000,
		RemainingHours:     100,
		Rate:               20,
		GapEstimatedCost:   500, // cluster-wide gap estimate
		NamespaceProjected: map[string]float64{"app": 400},
	}
	b := Budget{Name: "app", Namespace: "app", Amount: 1000, Thresholds: []float64{0.8}}

	st := EvaluateBudget(b, acc, forecast, 0.9)
	approx(t, "Forecast", st.Forecast, 400, 1e-9)
	// Gap estimate scales with the namespace's share (100/1000 = 10% → $50),
	// not the full $500 cluster estimate.
	approx(t, "CredibleLow", st.CredibleLow, 400, 1e-9)
	approx(t, "CredibleHigh", st.CredibleHigh, 450, 1e-9)
}
