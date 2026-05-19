package steps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestYoctoFSBLCleanInvokesWrapperWithCleansstateFlag(t *testing.T) {
	repo := t.TempDir()
	toolsDir := filepath.Join(repo, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(repo, "trace.log")
	stub := "#!/bin/bash\nenv | grep -E '^CLEANSSTATE_FSBL=' >> " + trace + "\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "build-yocto.sh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RepoRoot: repo}
	s, err := NewYoctoFSBLCleanStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "yocto-fsbl-clean" {
		t.Errorf("Name() = %s", s.Name())
	}
	deps := s.DependsOn()
	if len(deps) != 1 || deps[0] != "vivado" {
		t.Errorf("DependsOn = %v", deps)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(trace)
	if !strings.Contains(string(body), "CLEANSSTATE_FSBL=1") {
		t.Errorf("trace did not show CLEANSSTATE_FSBL=1 in env: %q", body)
	}
}
