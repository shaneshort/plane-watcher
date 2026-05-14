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

func writePetalinuxStub(t *testing.T, repo string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(repo, "petalinux.trace")
	body := "#!/bin/bash\nenv | grep -E '^SKIP_(BUILD|IMPORT)=' > " + trace + " || true\nexit 0\n"
	if err := os.WriteFile(filepath.Join(repo, "tools", "build-petalinux.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return trace
}

func TestPetalinuxFullBuild(t *testing.T) {
	repo := t.TempDir()
	trace := writePetalinuxStub(t, repo)
	cfg := &config.Config{RepoRoot: repo}
	sel := build.Selections{Petalinux: true}
	s, err := NewPetalinuxStep(cfg, sel)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "petalinux" {
		t.Errorf("Name() = %s", s.Name())
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(trace)
	if strings.Contains(string(body), "SKIP_BUILD=1") {
		t.Errorf("full build should not set SKIP_BUILD=1; trace=%q", body)
	}
}

func TestPetalinuxBitstreamOnly(t *testing.T) {
	repo := t.TempDir()
	trace := writePetalinuxStub(t, repo)
	cfg := &config.Config{RepoRoot: repo}
	sel := build.Selections{Bitstream: true} // Petalinux=false
	s, err := NewPetalinuxStep(cfg, sel)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(trace)
	if !strings.Contains(string(body), "SKIP_BUILD=1") {
		t.Errorf("bitstream-only must set SKIP_BUILD=1; trace=%q", body)
	}
}
