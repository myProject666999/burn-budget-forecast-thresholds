package budget

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshots", "budget.json")
	fs := NewFileStore(path)

	// Missing file loads as an empty snapshot without creating anything.
	snap, err := fs.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(snap.Records) != 0 {
		t.Errorf("records = %d, want 0", len(snap.Records))
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load created the file; want it absent: %v", err)
	}

	// Append creates the file with the record and notification state.
	ts := utc(2026, 3, 1, 0, 0)
	notif := NotificationState{Month: "2026-03", Level: LevelNearBudget, UpdatedAt: ts}
	if err := fs.Append(Record{Timestamp: ts, HourlyCost: 1.5, Namespaces: map[string]float64{"app": 1.0}}, notif); err != nil {
		t.Fatal(err)
	}

	got, err := fs.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(got.Records))
	}
	if got.Records[0].HourlyCost != 1.5 || got.Records[0].Namespaces["app"] != 1.0 {
		t.Errorf("record round-trip wrong: %+v", got.Records[0])
	}
	if got.Notification.Level != LevelNearBudget || got.Notification.Month != "2026-03" {
		t.Errorf("notification round-trip wrong: %+v", got.Notification)
	}

	// A second store instance sees the same data on disk.
	fs2 := NewFileStore(path)
	got2, err := fs2.Load()
	if err != nil || len(got2.Records) != 1 {
		t.Fatalf("second store load: records=%d err=%v", len(got2.Records), err)
	}
}

func TestFileStoreDedupsSameTimestampOnAppend(t *testing.T) {
	fs := NewFileStore(filepath.Join(t.TempDir(), "s.json"))
	ts := utc(2026, 3, 1, 0, 0)
	_ = fs.Append(Record{Timestamp: ts, HourlyCost: 1}, NotificationState{})
	_ = fs.Append(Record{Timestamp: ts, HourlyCost: 2}, NotificationState{})

	snap, _ := fs.Load()
	if len(snap.Records) != 1 {
		t.Fatalf("records = %d, want 1 after same-timestamp append", len(snap.Records))
	}
	if snap.Records[0].HourlyCost != 2 {
		t.Errorf("kept cost = %v, want last-writer 2", snap.Records[0].HourlyCost)
	}
}

func TestFileStoreCorruptFilePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	truncated := []byte(`{"version":1,"records":[{"timestamp":"2026-03-01T00:00:00Z","hourly_cost":`)
	if err := os.WriteFile(path, truncated, 0o600); err != nil {
		t.Fatal(err)
	}

	fs := NewFileStore(path)
	_, err := fs.Load()
	if err == nil {
		t.Fatal("Load on a truncated file = nil, want error")
	}
	var corrupt *CorruptSnapshotError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error = %T (%v), want *CorruptSnapshotError", err, err)
	}
	// The corrupt file must remain byte-for-byte; it must not be rebuilt.
	onDisk, _ := os.ReadFile(path)
	if string(onDisk) != string(truncated) {
		t.Errorf("corrupt file was modified: %q", string(onDisk))
	}

	// Append must refuse to overwrite a corrupt file either.
	if err := fs.Append(Record{Timestamp: utc(2026, 3, 1, 0, 0), HourlyCost: 1}, NotificationState{}); err == nil {
		t.Fatal("Append over a corrupt file = nil, want error")
	}
	afterAppend, _ := os.ReadFile(path)
	if string(afterAppend) != string(truncated) {
		t.Errorf("Append destroyed the corrupt file: %q", string(afterAppend))
	}
}

func TestFileStoreEmptyFileIsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path).Load(); err == nil {
		t.Fatal("empty file loaded without error")
	}
}

func TestFileStoreConcurrentAppendsDoNotLoseRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	base := utc(2026, 3, 1, 0, 0)

	const writers = 16
	const perWriter = 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each writer uses its own FileStore to model separate processes.
			fs := NewFileStore(path)
			for i := 0; i < perWriter; i++ {
				ts := base.Add(time.Duration(w*perWriter+i) * time.Second)
				if err := fs.Append(Record{Timestamp: ts, HourlyCost: float64(i)}, NotificationState{}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append error: %v", err)
	}

	snap, err := NewFileStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	want := writers * perWriter
	if len(snap.Records) != want {
		t.Errorf("records = %d, want %d (lost updates)", len(snap.Records), want)
	}
	// Records must remain sorted and unique.
	for i := 1; i < len(snap.Records); i++ {
		if !snap.Records[i].Timestamp.After(snap.Records[i-1].Timestamp) {
			t.Fatalf("records not strictly ascending at %d", i)
		}
	}
}

func TestSaveNoOpWithoutStore(t *testing.T) {
	res := &Result{}
	if err := Save(nil, res); err != nil {
		t.Errorf("Save(nil) = %v, want nil", err)
	}
}
