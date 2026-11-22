package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store persists snapshots. Implementations must be safe for concurrent use.
type Store interface {
	// Load returns the stored snapshot. A missing store returns an empty
	// snapshot and no error. A corrupt store returns an error and must not be
	// silently replaced.
	Load() (*Snapshot, error)
	// Append adds a record and updates the notification state atomically.
	// Concurrent appends must not lose records.
	Append(rec Record, notif NotificationState) error
}

// MemoryStore is an in-memory Store for tests and for callers who do not
// configure a snapshot path.
type MemoryStore struct {
	mu   sync.Mutex
	snap Snapshot
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{snap: Snapshot{Version: CurrentVersion}}
}

// Load implements Store.
func (m *MemoryStore) Load() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSnapshot(&m.snap), nil
}

// Append implements Store.
func (m *MemoryStore) Append(rec Record, notif NotificationState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Version = CurrentVersion
	m.snap.Records = append(m.snap.Records, rec)
	m.snap.Notification = notif
	return nil
}

func cloneSnapshot(s *Snapshot) *Snapshot {
	out := &Snapshot{Version: s.Version, Notification: s.Notification}
	out.Records = make([]Record, len(s.Records))
	for i, r := range s.Records {
		out.Records[i] = Record{Timestamp: r.Timestamp, HourlyCost: r.HourlyCost, Namespaces: copyMap(r.Namespaces)}
	}
	return out
}

// CorruptSnapshotError reports a snapshot file that could not be parsed. The
// original file is left untouched so an operator can inspect it.
type CorruptSnapshotError struct {
	Path string
	Err  error
}

func (e *CorruptSnapshotError) Error() string {
	return fmt.Sprintf("snapshot file %s is corrupt or truncated; left in place (%v)", e.Path, e.Err)
}

func (e *CorruptSnapshotError) Unwrap() error { return e.Err }

// FileStore is a JSON-file-backed Store. Writes are serialized with an advisory
// lock, written to a temporary file, and renamed into place, so a partial write
// never replaces a good file and concurrent writers never lose records.
type FileStore struct {
	path string
	mu   sync.Mutex // in-process serialization
}

// NewFileStore returns a FileStore at path. The file is not created until the
// first Append.
func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

// Path returns the snapshot path.
func (f *FileStore) Path() string { return f.path }

// Load implements Store.
func (f *FileStore) Load() (*Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loadUnlocked()
}

func (f *FileStore) loadUnlocked() (*Snapshot, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Snapshot{Version: CurrentVersion}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, &CorruptSnapshotError{Path: f.path, Err: errors.New("empty file")}
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, &CorruptSnapshotError{Path: f.path, Err: err}
	}
	if snap.Version == 0 {
		snap.Version = CurrentVersion
	}
	return &snap, nil
}

// Append implements Store.
func (f *FileStore) Append(rec Record, notif NotificationState) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	release, err := lockFile(f.path + ".lock")
	if err != nil {
		return err
	}
	defer release()

	snap, err := f.loadUnlocked()
	if err != nil {
		return err
	}
	snap.Version = CurrentVersion
	snap.Records = append(snap.Records, rec)
	// Records are kept sorted; a duplicate timestamp replaces the earlier value
	// (last writer wins) so re-running at the same instant is idempotent.
	sort.SliceStable(snap.Records, func(i, j int) bool {
		return snap.Records[i].Timestamp.Before(snap.Records[j].Timestamp)
	})
	deduped := snap.Records[:0]
	for _, r := range snap.Records {
		if n := len(deduped); n > 0 && deduped[n-1].Timestamp.Equal(r.Timestamp) {
			deduped[n-1] = r
			continue
		}
		deduped = append(deduped, r)
	}
	snap.Records = deduped
	snap.Notification = notif

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(f.path, data)
}

// writeAtomic writes data to a temporary file in the same directory and renames
// it over path so readers never observe a partial file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
