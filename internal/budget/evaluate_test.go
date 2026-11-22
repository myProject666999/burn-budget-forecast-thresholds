package budget

import (
	"math"
	"testing"
	"time"
)

func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func recs(pairs ...[2]float64) []Record {
	// pairs are {hoursSinceEpoch, hourlyCost}; kept for compactness in a few
	// tests that only care about ordering.
	out := make([]Record, 0, len(pairs))
	base := utc(2026, 3, 1, 0, 0)
	for _, p := range pairs {
		out = append(out, Record{Timestamp: base.Add(time.Duration(p[0] * float64(time.Hour))), HourlyCost: p[1]})
	}
	return out
}

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, want %.6f (tol %.6f)", name, got, want, tol)
	}
}

func TestIntegratePiecewiseConstant(t *testing.T) {
	// Window 00:00–03:00 with hourly observations $1, $2, $3.
	// 00→01 at $1 = 1; 01→02 at $2 = 2; 02→03 at $3 = 3. Known = 6.
	records := []Record{
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 1},
		{Timestamp: utc(2026, 3, 1, 1, 0), HourlyCost: 2},
		{Timestamp: utc(2026, 3, 1, 2, 0), HourlyCost: 3},
	}
	norm, stats := Normalize(records, utc(2026, 3, 1, 3, 0))
	acc := Integrate(norm, utc(2026, 3, 1, 0, 0), utc(2026, 3, 1, 3, 0), 90*time.Minute)

	approx(t, "KnownCost", acc.KnownCost, 6, 1e-9)
	approx(t, "KnownHours", acc.KnownHours, 3, 1e-9)
	approx(t, "ElapsedHours", acc.ElapsedHours, 3, 1e-9)
	approx(t, "Coverage", acc.Coverage, 1, 1e-9)
	approx(t, "GapHours", acc.GapHours, 0, 1e-9)
	if acc.PartialStart {
		t.Errorf("PartialStart = true, want false when first record is at window start")
	}
	if stats.Duplicates != 0 || stats.OutOfOrder != 0 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestIntegrateFlagsGapAndReducesCoverage(t *testing.T) {
	// 00→01 known ($1); the trailing 2h from 01:00 to 03:00 exceeds a 90m
	// threshold and becomes a gap.
	records := []Record{
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 1},
		{Timestamp: utc(2026, 3, 1, 1, 0), HourlyCost: 1},
	}
	norm, _ := Normalize(records, utc(2026, 3, 1, 3, 0))
	acc := Integrate(norm, utc(2026, 3, 1, 0, 0), utc(2026, 3, 1, 3, 0), 90*time.Minute)

	approx(t, "KnownCost", acc.KnownCost, 1, 1e-9) // only 00→01
	approx(t, "GapHours", acc.GapHours, 2, 1e-9)
	approx(t, "Coverage", acc.Coverage, 1.0/3.0, 1e-9)
	if len(acc.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(acc.Gaps))
	}
	if !acc.Gaps[0].Start.Equal(utc(2026, 3, 1, 1, 0)) || !acc.Gaps[0].End.Equal(utc(2026, 3, 1, 3, 0)) {
		t.Errorf("gap = %+v, want 01:00–03:00", acc.Gaps[0])
	}
}

func TestIntegrateMidMonthStartIsNotZero(t *testing.T) {
	// First run on Mar 20 with a single observation. The 19 preceding days are
	// unknown and must not be integrated as zero, so the result cannot claim a
	// complete month-to-date total.
	asOf := utc(2026, 3, 20, 0, 0)
	monthStart := utc(2026, 3, 1, 0, 0)
	records := []Record{{Timestamp: asOf, HourlyCost: 2}}
	norm, _ := Normalize(records, asOf)
	acc := Integrate(norm, monthStart, asOf, 30*time.Minute)

	approx(t, "KnownCost", acc.KnownCost, 0, 1e-9)
	if !acc.PartialStart {
		t.Error("PartialStart = false, want true")
	}
	if acc.Coverage > 0.01 {
		t.Errorf("Coverage = %.4f, want near zero; early month must not count as observed", acc.Coverage)
	}
	approx(t, "GapHours", acc.GapHours, 19*24, 1e-9)
}

func TestIntegrateRejectsNegativeDuration(t *testing.T) {
	records := []Record{{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 5}}
	norm, _ := Normalize(records, utc(2026, 3, 2, 0, 0))
	// Reversed window must not integrate a negative interval.
	acc := Integrate(norm, utc(2026, 3, 2, 0, 0), utc(2026, 3, 1, 0, 0), 30*time.Minute)
	if acc.KnownCost != 0 || acc.KnownHours != 0 || acc.GapHours != 0 {
		t.Errorf("reversed window integrated something: %+v", acc)
	}
	if acc.ElapsedHours < 0 {
		t.Errorf("ElapsedHours = %.2f, want >= 0", acc.ElapsedHours)
	}
}

func TestNormalizeHandlesDuplicatesOrderAndFuture(t *testing.T) {
	asOf := utc(2026, 3, 1, 2, 0)
	records := []Record{
		{Timestamp: utc(2026, 3, 1, 2, 0), HourlyCost: 2},  // out of order
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 1},  // dup timestamp
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 9},  // dup → last wins
		{Timestamp: utc(2026, 3, 1, 3, 0), HourlyCost: 4},  // future
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: -1}, // invalid cost
	}
	norm, stats := Normalize(records, asOf)

	if len(norm) != 2 {
		t.Fatalf("normalized %d records, want 2: %+v", len(norm), norm)
	}
	if !norm[0].Timestamp.Equal(utc(2026, 3, 1, 0, 0)) || norm[0].HourlyCost != 9 {
		t.Errorf("norm[0] = %+v, want 00:00 with last-writer cost 9", norm[0])
	}
	if stats.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1", stats.Duplicates)
	}
	if stats.FutureIgnored != 1 {
		t.Errorf("FutureIgnored = %d, want 1", stats.FutureIgnored)
	}
	if stats.InvalidIgnored != 1 {
		t.Errorf("InvalidIgnored = %d, want 1", stats.InvalidIgnored)
	}
	if stats.OutOfOrder != 1 {
		t.Errorf("OutOfOrder = %d, want 1", stats.OutOfOrder)
	}
}

func TestForecastUsesCalendarMonth(t *testing.T) {
	asOf := utc(2026, 3, 15, 12, 0) // March: 31 days
	_, monthEnd := MonthBounds(asOf)
	acc := Accrual{KnownCost: 100, KnownHours: 360, Coverage: 1}
	rate := RateEstimate{Current: 1, Rate: 1, Source: "current"}
	f := ForecastMonth(asOf, acc, rate)

	if f.MonthDays != 31 {
		t.Errorf("MonthDays = %d, want 31", f.MonthDays)
	}
	if !f.MonthEnd.Equal(monthEnd) {
		t.Errorf("MonthEnd = %v, want %v", f.MonthEnd, monthEnd)
	}
	// Mar 15 12:00 → Apr 1 00:00 = 16.5 days = 396 hours.
	approx(t, "RemainingHours", f.RemainingHours, 396, 1e-9)
	approx(t, "ProjectedTotal", f.ProjectedTotal, 100+396, 1e-9)
}

func TestForecastLeapFebruary(t *testing.T) {
	for _, tc := range []struct {
		y, want int
	}{{2026, 28}, {2028, 29}, {2000, 29}, {1900, 28}} {
		asOf := utc(tc.y, 2, 10, 0, 0)
		f := ForecastMonth(asOf, Accrual{}, RateEstimate{Rate: 0})
		if f.MonthDays != tc.want {
			t.Errorf("year %d: MonthDays = %d, want %d", tc.y, f.MonthDays, tc.want)
		}
	}
	// 730 hours is explicitly not used: a 31-day month is 744 calendar hours.
	asOf := utc(2026, 3, 1, 0, 0)
	f := ForecastMonth(asOf, Accrual{}, RateEstimate{Rate: 1})
	approx(t, "March total hours", f.RemainingHours, 744, 1e-9)
}

func TestForecastExcludesGapsFromKnownAndListsThenSeparately(t *testing.T) {
	asOf := utc(2026, 3, 15, 12, 0)
	acc := Accrual{KnownCost: 50, GapHours: 10, Coverage: 0.5, PartialStart: true}
	f := ForecastMonth(asOf, acc, RateEstimate{Rate: 2})
	approx(t, "KnownCumulative", f.KnownCumulative, 50, 1e-9)
	approx(t, "GapEstimatedCost", f.GapEstimatedCost, 20, 1e-9)
	// The gap estimate is separate, not folded into the known cumulative.
	approx(t, "ProjectedTotal", f.ProjectedTotal, 50+396*2, 1e-9)
	if f.Limitation == "" {
		t.Error("expected a limitation note for partial-start data")
	}
}

func TestEstimateRateTrendAndFallback(t *testing.T) {
	// Three daily means 10, 8, 6 → slope -2/day, last-day fit 6.
	var records []Record
	for i, v := range []float64{10, 8, 6} {
		records = append(records, Record{Timestamp: utc(2026, 3, 1+i, 0, 0), HourlyCost: v})
	}
	asOf := utc(2026, 3, 3, 0, 0)
	norm, _ := Normalize(records, asOf)
	re := EstimateRate(norm, asOf, 6, 7, 3)
	if re.Source != "trend" {
		t.Fatalf("Source = %q, want trend", re.Source)
	}
	approx(t, "SlopePerDay", re.SlopePerDay, -2, 1e-9)
	approx(t, "Rate", re.Rate, 6, 1e-6)

	// Too few samples falls back to current with an explanation.
	fewNorm, _ := Normalize(records[:1], asOf)
	few := EstimateRate(fewNorm, asOf, 7, 7, 3)
	if few.Source != "current" {
		t.Errorf("Source = %q, want current", few.Source)
	}
	approx(t, "fallback Rate", few.Rate, 7, 1e-9)
	if few.Explanation == "" {
		t.Error("expected a fallback explanation")
	}
}

func TestEstimateRateClampsNegativeTrendToZero(t *testing.T) {
	// Daily means 100, 10, 1 produce a fit that extrapolates below zero at the
	// last sample day; the rate must clamp to zero, never negative.
	var records []Record
	for i, v := range []float64{100, 10, 1} {
		records = append(records, Record{Timestamp: utc(2026, 3, 1+i, 0, 0), HourlyCost: v})
	}
	asOf := utc(2026, 3, 3, 0, 0)
	norm, _ := Normalize(records, asOf)
	re := EstimateRate(norm, asOf, 1, 7, 3)
	if re.Source != "trend" {
		t.Fatalf("Source = %q, want trend", re.Source)
	}
	if re.Rate < 0 {
		t.Errorf("Rate = %.4f, want >= 0", re.Rate)
	}
	approx(t, "Rate", re.Rate, 0, 1e-9)
}

func TestCompareWindowsZeroBaselineNotComparable(t *testing.T) {
	asOf := utc(2026, 3, 15, 0, 0)
	// Only the current 7-day window has data; the previous window is empty.
	var records []Record
	for h := 0; h < 7*24; h++ {
		records = append(records, Record{Timestamp: asOf.Add(-time.Duration(h) * time.Hour), HourlyCost: 1})
	}
	norm, _ := Normalize(records, asOf)
	cmp := CompareWindows(norm, asOf, 7, 2*time.Hour)

	if cmp.Comparable {
		t.Error("Comparable = true, want false when previous window cost is zero")
	}
	approx(t, "Previous.Cost", cmp.Previous.Cost, 0, 1e-9)
	if cmp.ChangePercent != 0 {
		t.Errorf("ChangePercent = %v, want 0 (undefined)", cmp.ChangePercent)
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	asOf := utc(2026, 3, 1, 3, 0)
	records := []Record{
		{Timestamp: utc(2026, 3, 1, 2, 0), HourlyCost: 3},
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 1},
		{Timestamp: utc(2026, 3, 1, 1, 0), HourlyCost: 2},
	}
	first := records[0].Timestamp
	_, _ = Normalize(records, asOf)
	if !records[0].Timestamp.Equal(first) {
		t.Error("Normalize reordered the caller's slice")
	}
}

func TestCompareWindowsChangeAndCoverageNote(t *testing.T) {
	asOf := utc(2026, 3, 15, 0, 0)
	var records []Record
	// Current window: $1/hr for 7 days. Previous: $0.5/hr but only half covered.
	for h := 0; h < 7*24; h++ {
		records = append(records, Record{Timestamp: asOf.Add(-time.Duration(h) * time.Hour), HourlyCost: 1})
	}
	for h := 7*24 + 12; h < 14*24; h++ {
		records = append(records, Record{Timestamp: asOf.Add(-time.Duration(h) * time.Hour), HourlyCost: 0.5})
	}
	norm, _ := Normalize(records, asOf)
	cmp := CompareWindows(norm, asOf, 7, 2*time.Hour)

	if !cmp.Comparable {
		t.Fatal("Comparable = false, want true")
	}
	// The leading hour of each window has no observation at its start, so it
	// is unknown: current knows 167 hours at $1; previous knows 155 hours at
	// $0.50 (the 12h tail is a gap).
	approx(t, "Current.Cost", cmp.Current.Cost, 167, 1e-6)
	approx(t, "Previous.Cost", cmp.Previous.Cost, 77.5, 1e-6)
	approx(t, "ChangeAmount", cmp.ChangeAmount, 89.5, 1e-6)
	if !cmp.CoverageDiffer {
		t.Error("CoverageDiffer = false, want true for unequal coverage")
	}
	if cmp.CoverageNote == "" {
		t.Error("expected a coverage note")
	}
}

func TestIntegrateDoesNotLeakPreviousMonth(t *testing.T) {
	// February observations must not count toward March accrual.
	records := []Record{
		{Timestamp: utc(2026, 2, 28, 22, 0), HourlyCost: 100},
		{Timestamp: utc(2026, 2, 28, 23, 0), HourlyCost: 100},
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 1},
		{Timestamp: utc(2026, 3, 1, 1, 0), HourlyCost: 1},
		{Timestamp: utc(2026, 3, 1, 2, 0), HourlyCost: 1},
	}
	monthStart, monthEnd := MonthBounds(utc(2026, 3, 15, 0, 0))
	norm, _ := Normalize(records, utc(2026, 3, 1, 2, 0))
	acc := Integrate(norm, monthStart, utc(2026, 3, 1, 2, 0), 90*time.Minute)
	approx(t, "KnownCost", acc.KnownCost, 2, 1e-9) // 00→01, 01→02 at $1
	approx(t, "KnownHours", acc.KnownHours, 2, 1e-9)
	// The month-end boundary is exclusive; no negative or cross-month time.
	if !monthEnd.Equal(utc(2026, 4, 1, 0, 0)) {
		t.Errorf("monthEnd = %v, want Apr 1 00:00", monthEnd)
	}
}

func TestIntegrateMonthStartAtBoundary(t *testing.T) {
	// A record exactly at month start integrates the full first interval.
	records := []Record{
		{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 2},
		{Timestamp: utc(2026, 3, 1, 1, 0), HourlyCost: 2},
	}
	monthStart, _ := MonthBounds(utc(2026, 3, 15, 0, 0))
	norm, _ := Normalize(records, utc(2026, 3, 1, 1, 0))
	acc := Integrate(norm, monthStart, utc(2026, 3, 1, 1, 0), 90*time.Minute)
	approx(t, "KnownCost", acc.KnownCost, 2, 1e-9)
	approx(t, "Coverage", acc.Coverage, 1, 1e-9)
	if acc.PartialStart {
		t.Error("PartialStart = true, want false when a record sits at month start")
	}
}
