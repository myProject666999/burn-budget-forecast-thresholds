package budget

import (
	"path/filepath"
	"testing"
	"time"
)

// seedHourly writes one observation per hour in [start, end).
func seedHourly(t *testing.T, store Store, start, end time.Time, hourly float64) {
	t.Helper()
	for ts := start; ts.Before(end); ts = ts.Add(time.Hour) {
		if err := store.Append(Record{Timestamp: ts, HourlyCost: hourly}, NotificationState{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNotificationPersistsAndDedupesAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.json")
	store := NewFileStore(path)
	monthStart := utc(2026, 3, 1, 0, 0)
	asOf := utc(2026, 3, 5, 0, 0)
	seedHourly(t, store, monthStart, asOf, 1) // 96 known hours at $1 = $96

	opts := DefaultOptions(asOf)
	opts.GapThreshold = 2 * time.Hour
	opts.Budgets = []Budget{{Name: "cluster", Amount: 50, Thresholds: []float64{0.8, 1.0}}}

	// First run: spend 96 > 50 → overspent → notify.
	res, err := Evaluate(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Budgets[0].State != StateOverspent {
		t.Fatalf("State = %q, want overspent", res.Budgets[0].State)
	}
	if !res.Notification.Notify {
		t.Fatalf("first overspend did not notify: %+v", res.Notification)
	}
	if err := Save(store, res); err != nil {
		t.Fatal(err)
	}

	// Re-evaluate at the same instant: level unchanged, no repeat notification.
	opts2 := DefaultOptions(asOf)
	opts2.GapThreshold = 2 * time.Hour
	opts2.Budgets = opts.Budgets
	res2, err := Evaluate(store, opts2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Budgets[0].State != StateOverspent {
		t.Fatalf("State = %q, want overspent", res2.Budgets[0].State)
	}
	if res2.Notification.PreviousLevel != LevelOverspent {
		t.Errorf("PreviousLevel = %d, want persisted overspent", res2.Notification.PreviousLevel)
	}
	if res2.Notification.Notify {
		t.Error("same level notified twice")
	}
}

func TestNotificationResetsOnNewMonthWithFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.json")
	store := NewFileStore(path)
	// Seed March with overspend, persisted notification at overspent.
	monthStart := utc(2026, 3, 1, 0, 0)
	asOf := utc(2026, 3, 5, 0, 0)
	seedHourly(t, store, monthStart, asOf, 1)
	opts := DefaultOptions(asOf)
	opts.GapThreshold = 2 * time.Hour
	opts.Budgets = []Budget{{Name: "cluster", Amount: 50, Thresholds: []float64{1.0}}}
	res, err := Evaluate(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(store, res); err != nil {
		t.Fatal(err)
	}

	// April 1: a new month with fresh data; the level resets.
	apr := utc(2026, 4, 1, 2, 0)
	seedHourly(t, store, utc(2026, 4, 1, 0, 0), apr, 1)
	opts2 := DefaultOptions(apr)
	opts2.GapThreshold = 2 * time.Hour
	opts2.Budgets = opts.Budgets
	res2, err := Evaluate(store, opts2)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Notification.MonthReset {
		t.Errorf("MonthReset = false, want true across months")
	}
	if res2.Notification.PreviousLevel != LevelNormal {
		t.Errorf("PreviousLevel = %d, want reset to normal", res2.Notification.PreviousLevel)
	}
}
