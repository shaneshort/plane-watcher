package steps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestPSStageWritesManifest(t *testing.T) {
	stage := t.TempDir()
	for _, name := range []string{"plane-feeder", "dump-capture"} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(name+"-body"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		StagingDir: stage,
		Commands: []config.CommandMeta{
			{Name: "plane-feeder", Targets: []string{"arm"}},
			{Name: "dump-capture", Targets: []string{"arm"}},
		},
	}
	s, err := NewPSStageStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "ps-stage" {
		t.Errorf("Name() = %s", s.Name())
	}
	deps := s.DependsOn()
	if len(deps) != 1 || deps[0] != "ps-build" {
		t.Errorf("DependsOn = %v", deps)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(stage, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Binaries map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"binaries"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Binaries["plane-feeder"].SHA256 == "" || m.Binaries["dump-capture"].SHA256 == "" {
		t.Errorf("manifest missing sha256: %+v", m)
	}
}
