package steps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestPSBuildSelectsARMCommandsOnly(t *testing.T) {
	repo := t.TempDir()
	cmdDir := filepath.Join(repo, "ps", "cmd", "plane-feeder")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	psDir := filepath.Join(repo, "ps")
	if err := os.WriteFile(filepath.Join(psDir, "go.mod"), []byte("module example.com/x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RepoRoot:   repo,
		StagingDir: filepath.Join(repo, "stage"),
		Commands: []config.CommandMeta{
			{Name: "plane-feeder", Targets: []string{"arm"}},
			{Name: "collect-stats", Targets: []string{"host"}},
		},
	}
	s, err := NewPSBuildStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "ps-build" {
		t.Errorf("Name() = %s", s.Name())
	}
	err = s.Run(context.Background(), func(build.Event) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.StagingDir, "plane-feeder")); err != nil {
		t.Errorf("plane-feeder binary missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.StagingDir, "collect-stats")); err == nil {
		t.Errorf("collect-stats should not be cross-built")
	}
}
