package budget

import (
	"math"
	"sort"
	"time"
)

// RecordStats counts records that were dropped or reordered during
// normalization. They are reported so callers never silently lose data.
type RecordStats struct {
	InvalidIgnored int
	FutureIgnored  int
	Duplicates     int
	OutOfOrder     int
}

// NormRecord is a normalized observation used internally by the accrual and
// rate code.
type NormRecord struct {
	Timestamp  time.Time
	HourlyCost float64
	Namespaces map[string]float64
}

// Normalize filters and orders records for evaluation at horizon:
//   - records with a zero timestamp or a negative / non-finite cost are dropped
//   - records after horizon are dropped (they are "future" observations)
//   - the remaining records are sorted ascending by timestamp
//   - records with an identical timestamp collapse to the last one supplied,
//     which is deterministic for a given input order
//
// Out-of-order is counted in input order before sorting so callers can detect
// writers that are not appending chronologically.
func Normalize(records []Record, horizon time.Time) ([]NormRecord, RecordStats) {
	var stats RecordStats
	var prev time.Time
	var havePrev bool

	kept := make([]NormRecord, 0, len(records))
	for _, r := range records {
		// A NaN timestamp cannot exist; a zero time means missing data.
		if r.Timestamp.IsZero() || math.IsNaN(r.HourlyCost) || math.IsInf(r.HourlyCost, 0) || r.HourlyCost < 0 {
			stats.InvalidIgnored++
			continue
		}
		ts := r.Timestamp.UTC()
		if ts.After(horizon) {
			stats.FutureIgnored++
			continue
		}
		if havePrev && ts.Before(prev) {
			stats.OutOfOrder++
		}
		prev = ts
		havePrev = true
		kept = append(kept, NormRecord{Timestamp: ts, HourlyCost: r.HourlyCost, Namespaces: copyMap(r.Namespaces)})
	}

	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].Timestamp.Before(kept[j].Timestamp)
	})

	out := kept[:0]
	for _, r := range kept {
		if n := len(out); n > 0 && out[n-1].Timestamp.Equal(r.Timestamp) {
			// Last writer wins for an identical timestamp.
			stats.Duplicates++
			out[n-1] = r
			continue
		}
		out = append(out, r)
	}
	return out, stats
}

func copyMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Integrate accrues known cost over [start, end) using a piecewise-constant
// assumption: each observation is held until the next one. Intervals longer
// than gapThreshold, the leading interval before the first observation, and
// the trailing interval after the last observation when it exceeds the
// threshold are treated as gaps and are not integrated. Elapsed time is never
// negative, so a reversed window yields zero known cost and zero gaps.
func Integrate(records []NormRecord, start, end time.Time, gapThreshold time.Duration) Accrual {
	start = start.UTC()
	end = end.UTC()
	acc := Accrual{Start: start, End: end}
	if !end.After(start) {
		acc.Coverage = 1
		return acc
	}
	acc.ElapsedHours = end.Sub(start).Hours()

	// Select observations inside the half-open window.
	in := make([]NormRecord, 0, len(records))
	for _, r := range records {
		if !r.Timestamp.Before(start) && r.Timestamp.Before(end) {
			in = append(in, r)
		}
	}

	if len(in) == 0 {
		acc.GapHours = acc.ElapsedHours
		acc.Gaps = []Gap{{Start: start, End: end, Hours: acc.ElapsedHours}}
		acc.Coverage = 0
		acc.PartialStart = true
		return acc
	}

	acc.ObservedFrom = in[0].Timestamp
	acc.PartialStart = in[0].Timestamp.After(start)

	addGap := func(a, b time.Time) {
		h := b.Sub(a).Hours()
		if h <= 0 {
			return
		}
		acc.GapHours += h
		acc.Gaps = append(acc.Gaps, Gap{Start: a, End: b, Hours: h})
	}
	addKnown := func(rec NormRecord, hours float64) {
		if hours <= 0 {
			return
		}
		acc.KnownHours += hours
		acc.KnownCost += rec.HourlyCost * hours
		if len(rec.Namespaces) > 0 {
			if acc.Namespaces == nil {
				acc.Namespaces = make(map[string]float64, len(rec.Namespaces))
			}
			for ns, hourly := range rec.Namespaces {
				acc.Namespaces[ns] += hourly * hours
			}
		}
	}

	// Leading interval: there is no observation at or before the window start,
	// so the cost before the first observation is unknown at any length. A
	// first run in the middle of a month therefore never integrates the earlier
	// part of the month as zero.
	if lead := in[0].Timestamp.Sub(start); lead > 0 {
		addGap(start, in[0].Timestamp)
	}

	for i := 0; i+1 < len(in); i++ {
		cur, next := in[i], in[i+1]
		dt := next.Timestamp.Sub(cur.Timestamp)
		if dt <= 0 {
			continue
		}
		if dt > gapThreshold {
			addGap(cur.Timestamp, next.Timestamp)
			continue
		}
		addKnown(cur, dt.Hours())
	}

	// Trailing interval up to the end of the window.
	if tail := end.Sub(in[len(in)-1].Timestamp); tail > 0 {
		if tail > gapThreshold {
			addGap(in[len(in)-1].Timestamp, end)
		} else {
			addKnown(in[len(in)-1], tail.Hours())
		}
	}

	if acc.ElapsedHours > 0 {
		acc.Coverage = acc.KnownHours / acc.ElapsedHours
		if acc.Coverage < 0 {
			acc.Coverage = 0
		}
		if acc.Coverage > 1 {
			acc.Coverage = 1
		}
	}
	if acc.GapHours < 0 {
		acc.GapHours = 0
	}
	// A small floating-point drift is not a gap.
	if acc.GapHours < 1e-9 {
		acc.GapHours = 0
	}
	sort.Slice(acc.Gaps, func(i, j int) bool { return acc.Gaps[i].Start.Before(acc.Gaps[j].Start) })
	return acc
}
