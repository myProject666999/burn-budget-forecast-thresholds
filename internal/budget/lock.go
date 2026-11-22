package budget

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on path (creating it if needed). It
// serializes snapshot writers across processes. The returned function unlocks
// and closes the file.
//
// The project targets Linux and macOS, both of which provide flock(2).
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
