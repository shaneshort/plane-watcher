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

func TestFSBLCleanInvokesRunBuildTwice(t *testing.T) {
	repo := t.TempDir()
	scriptDir := filepath.Join(repo, "firmware", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(repo, "trace.log")
	stub := "#!/bin/bash\necho \"$@\" >> " + trace + "\n"
	if err := os.WriteFile(filepath.Join(scriptDir, "run-build.sh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RepoRoot: repo}
	s, err := NewPetalinuxFSBLCleanStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "petalinux-fsbl-clean" {
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
	got := string(body)
	if !strings.Contains(got, "fsbl-firmware") || !strings.Contains(got, "fsbl") || !strings.Contains(got, "cleansstate") {
		t.Errorf("trace did not show both clean invocations: %q", got)
	}
}
