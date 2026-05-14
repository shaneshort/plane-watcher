package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type petalinuxFSBLCleanStep struct {
	cfg *config.Config
}

func NewPetalinuxFSBLCleanStep(cfg *config.Config) (build.Step, error) {
	if cfg.RepoRoot == "" {
		return nil, fmt.Errorf("petalinux-fsbl-clean: cfg.RepoRoot empty")
	}
	return &petalinuxFSBLCleanStep{cfg: cfg}, nil
}

func (s *petalinuxFSBLCleanStep) Name() string        { return "petalinux-fsbl-clean" }
func (s *petalinuxFSBLCleanStep) DependsOn() []string { return []string{"vivado"} }

func (s *petalinuxFSBLCleanStep) Run(ctx context.Context, emit func(build.Event)) error {
	script := filepath.Join(s.cfg.RepoRoot, "firmware", "scripts", "run-build.sh")
	for _, target := range []string{"fsbl-firmware", "fsbl"} {
		emit(build.Event{Kind: build.EventProgress, Payload: build.ProgressPayload{
			Message: "cleansstate " + target,
		}})
		if err := Run(ctx, RunOptions{
			Argv: []string{script, "petalinux-build", "-c", target, "-x", "cleansstate"},
			Dir:  s.cfg.RepoRoot,
			Env:  envForStep(s.cfg),
			Emit: emit,
		}); err != nil {
			return err
		}
	}
	return nil
}
