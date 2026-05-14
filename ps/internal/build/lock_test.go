package build

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockAcquireRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".builder.lock")
	l, err := AcquireLock(p)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("lock file gone after release: %v", err)
	}
}

func TestLockContention(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".builder.lock")
	first, err := AcquireLock(p)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer first.Release()

	_, err = AcquireLock(p)
	if err == nil {
		t.Fatal("expected lock contention error")
	}
	var inUse *LockInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *LockInUseError, got %T: %v", err, err)
	}
	if !strings.Contains(inUse.Diagnostic, "pid=") {
		t.Errorf("expected diagnostic to mention pid, got %q", inUse.Diagnostic)
	}
}
