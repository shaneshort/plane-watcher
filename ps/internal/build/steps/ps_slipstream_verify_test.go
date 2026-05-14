package steps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
	"github.com/plane-watcher/plane-feeder/internal/build/recipe"
)

func TestSlipstreamVerifyDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "binaries.inc")
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
		{Name: "dump-capture", Targets: []string{"arm"}},
	}
	if err := os.WriteFile(inc, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Commands: cmds, RecipeIncludePath: inc}
	s, err := NewPSSlipstreamVerifyStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "ps-slipstream-verify" {
		t.Errorf("Name() = %s", s.Name())
	}
	err = s.Run(context.Background(), func(build.Event) {})
	if err == nil {
		t.Fatal("expected drift error")
	}
}

func TestSlipstreamVerifyPassesAfterWrite(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "binaries.inc")
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
	}
	if err := recipe.Write(inc, cmds); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Commands: cmds, RecipeIncludePath: inc}
	s, _ := NewPSSlipstreamVerifyStep(cfg)
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Errorf("Run: %v", err)
	}
}
