package budget

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// EstimateRate chooses the forecast rate from normalized observations that lie
// at or before asOf.
//
// The current rate is the most recent observation's hourly cost. When at least
// minSamples distinct UTC days fall inside the trailing trendDays window, a
// least-squares line is fitted to the per-day mean hourly cost and evaluated at
// the last sample day. A negative or non-finite fit is clamped to zero. When
// there are too few samples the current rate is returned with an explanation.
//
// trendDays and minSamples are provisional configuration values.
func EstimateRate(records []NormRecord, asOf time.Time, current float64, trendDays, minSamples int) RateEstimate {
	asOf = asOf.UTC()
	if trendDays <= 0 {
		trendDays = DefaultTrendWindowDays
	}
	if minSamples <= 0 {
		minSamples = DefaultMinTrendSamples
	}

	re := RateEstimate{Current: current, Rate: current, Source: "current"}

	// Per-day mean of observations in [asOf-trendDays, asOf].
	windowStart := asOf.AddDate(0, 0, -trendDays)
	type dayAgg struct {
		day   time.Time
		sum   float64
		count int
	}
	var days []dayAgg
	for _, r := range records {
		if r.Timestamp.Before(windowStart) || r.Timestamp.After(asOf) {
			continue
		}
		d := time.Date(r.Timestamp.Year(), r.Timestamp.Month(), r.Timestamp.Day(), 0, 0, 0, 0, time.UTC)
		if n := len(days); n > 0 && days[n-1].day.Equal(d) {
			days[n-1].sum += r.HourlyCost
			days[n-1].count++
			continue
		}
		days = append(days, dayAgg{day: d, sum: r.HourlyCost, count: 1})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].day.Before(days[j].day) })
	re.SampleDays = len(days)

	if len(days) < minSamples {
		re.Explanation = fmt.Sprintf("not enough daily samples (%d < %d); using current rate", len(days), minSamples)
		return re
	}

	base := days[0].day
	n := float64(len(days))
	var sumX, sumY, sumXY, sumXX float64
	var meanY float64
	for _, d := range days {
		x := d.day.Sub(base).Hours() / 24
		y := d.sum / float64(d.count)
		meanY += y
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	meanY /= n
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		re.Explanation = "daily samples share a single day; using current rate"
		return re
	}
	slope := (n*sumXY - sumX*sumY) / denom
	intercept := (sumY - slope*sumX) / n

	lastX := days[len(days)-1].day.Sub(base).Hours() / 24
	fitted := slope*lastX + intercept
	if math.IsNaN(fitted) || math.IsInf(fitted, 0) {
		re.Explanation = "trend fit was not finite; using current rate"
		return re
	}
	re.SlopePerDay = slope
	re.Source = "trend"
	re.SampleDays = len(days)
	if fitted <= 0 {
		re.Rate = 0
		re.Explanation = fmt.Sprintf("trend slope %.4f/day extrapolated to %.4f; clamped to zero", slope, fitted)
		return re
	}
	re.Rate = fitted
	re.Explanation = fmt.Sprintf("linear trend over %d days: %.4f/day, rate %.4f", len(days), slope, fitted)
	return re
}

// ForecastMonth projects the whole UTC calendar month that contains asOf, using
// the known accrued cost plus the remaining calendar hours times the forecast
// rate. Gap hours are estimated separately and are not added to the known
// cumulative or the projected total.
func ForecastMonth(asOf time.Time, accrual Accrual, rate RateEstimate) Forecast {
	asOf = asOf.UTC()
	monthStart, monthEnd := MonthBounds(asOf)
	f := Forecast{
		KnownCumulative: accrual.KnownCost,
		Rate:            rate.Rate,
		RateSource:      rate.Source,
		MonthDays:       int(monthEnd.Sub(monthStart).Hours() / 24),
		MonthEnd:        monthEnd,
	}
	if asOf.Before(monthEnd) {
		f.RemainingHours = monthEnd.Sub(asOf).Hours()
	}
	f.ProjectedTotal = f.KnownCumulative + f.RemainingHours*f.Rate
	if accrual.GapHours > 0 {
		f.GapEstimatedCost = accrual.GapHours * rate.Rate
	}
	if accrual.PartialStart {
		f.Limitation = "month-to-date data starts after the month began; the omitted part is treated as unknown, not zero, so the projected total is a lower-bound-flavoured estimate"
	}
	return f
}
