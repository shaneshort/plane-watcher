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
	// Lay out the minimum repo structure: ps/cmd/plane-feeder/ with a
	// canonical defaults file, plus a recipe dir whose files/ holds an
	// identical copy. Both must match for the verify step to pass.
	repo := t.TempDir()
	srcDir := filepath.Join(repo, "ps", "cmd", "plane-feeder")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defaultsBody := []byte("# canonical defaults\nDAEMON_ARGS=\"\"\n")
	if err := os.WriteFile(filepath.Join(srcDir, "plane-feeder.default"), defaultsBody, 0o644); err != nil {
		t.Fatal(err)
	}
	recipeDir := filepath.Join(repo, "firmware", "recipes-pwtools", "plane-watcher-tools")
	if err := os.MkdirAll(filepath.Join(recipeDir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "files", "plane-feeder.default"), defaultsBody, 0o644); err != nil {
		t.Fatal(err)
	}

	inc := filepath.Join(recipeDir, "binaries.inc")
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
	}
	if err := recipe.Write(inc, cmds); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Commands:          cmds,
		RecipeIncludePath: inc,
		RepoRoot:          repo,
	}
	s, _ := NewPSSlipstreamVerifyStep(cfg)
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestSlipstreamVerifyDetectsDefaultsDrift(t *testing.T) {
	repo := t.TempDir()
	srcDir := filepath.Join(repo, "ps", "cmd", "plane-feeder")
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "plane-feeder.default"), []byte("source\n"), 0o644)

	recipeDir := filepath.Join(repo, "firmware", "recipes-pwtools", "plane-watcher-tools")
	_ = os.MkdirAll(filepath.Join(recipeDir, "files"), 0o755)
	_ = os.WriteFile(filepath.Join(recipeDir, "files", "plane-feeder.default"), []byte("STALE\n"), 0o644)

	inc := filepath.Join(recipeDir, "binaries.inc")
	cmds := []config.CommandMeta{{Name: "plane-feeder", Targets: []string{"arm"}}}
	if err := recipe.Write(inc, cmds); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Commands:          cmds,
		RecipeIncludePath: inc,
		RepoRoot:          repo,
	}
	s, _ := NewPSSlipstreamVerifyStep(cfg)
	err := s.Run(context.Background(), func(build.Event) {})
	if err == nil {
		t.Fatal("expected defaults drift error")
	}
}
