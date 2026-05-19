package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type packageBootStep struct {
	cfg          *config.Config
	requireImage bool
	deps         []string
}

func NewPackageBootStep(cfg *config.Config, sel build.Selections) (build.Step, error) {
	if cfg.BuildDeployDir == "" {
		return nil, fmt.Errorf("package-boot: BuildDeployDir empty")
	}
	deps := []string{"yocto"}
	return &packageBootStep{
		cfg:          cfg,
		requireImage: sel.Yocto,
		deps:         deps,
	}, nil
}

func (s *packageBootStep) Name() string        { return "package-boot" }
func (s *packageBootStep) DependsOn() []string { return s.deps }

func (s *packageBootStep) Run(_ context.Context, emit func(build.Event)) error {
	need := []string{"BOOT.BIN"}
	if s.requireImage {
		need = append(need, "image.ub")
	}
	for _, art := range need {
		path := filepath.Join(s.cfg.BuildDeployDir, art)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("expected artefact missing: %s: %w", path, err)
		}
		emit(build.Event{Kind: build.EventLogLine, Payload: "verified " + path})
	}
	return nil
}
