package build

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// Lock is a flock-based re-entrancy guard for builder runs. The kernel
// releases the lock automatically on process exit, so no PID-liveness
// check is needed. File contents are diagnostic only.
type Lock struct {
	f *os.File
}

type LockInUseError struct {
	Path       string
	Diagnostic string
}

func (e *LockInUseError) Error() string {
	return fmt.Sprintf("another builder run in progress (%s)", e.Diagnostic)
}

// AcquireLock acquires an exclusive non-blocking flock on path. Returns
// *LockInUseError if another process holds the lock; the error includes
// the diagnostic body from the existing lock file (PID, start time).
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		body, _ := os.ReadFile(path)
		diag := string(body)
		if diag == "" {
			diag = "no diagnostic available"
		}
		f.Close()
		return nil, &LockInUseError{Path: path, Diagnostic: diag}
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, fmt.Errorf("truncate lock: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return nil, err
	}
	body := fmt.Sprintf("pid=%d start=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
