package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type psBuildStep struct {
	cfg *config.Config
}

func NewPSBuildStep(cfg *config.Config) (build.Step, error) {
	if cfg.RepoRoot == "" {
		return nil, fmt.Errorf("ps-build: cfg.RepoRoot empty")
	}
	if cfg.StagingDir == "" {
		return nil, fmt.Errorf("ps-build: cfg.StagingDir empty")
	}
	return &psBuildStep{cfg: cfg}, nil
}

func (s *psBuildStep) Name() string        { return "ps-build" }
func (s *psBuildStep) DependsOn() []string { return nil }

func (s *psBuildStep) Run(ctx context.Context, emit func(build.Event)) error {
	if err := os.MkdirAll(s.cfg.StagingDir, 0o755); err != nil {
		return err
	}
	psDir := filepath.Join(s.cfg.RepoRoot, "ps")
	arm := config.ARMCommands(s.cfg.Commands)
	if len(arm) == 0 {
		emit(build.Event{Kind: build.EventWarning, Payload: "no ARM commands configured"})
		return nil
	}
	env := mergeEnv(os.Environ(), map[string]string{
		"GOOS":    "linux",
		"GOARCH":  "arm",
		"GOARM":   "7",
		"GOCACHE": "/tmp/plane_watcher_gocache",
	})
	for i, c := range arm {
		emit(build.Event{Kind: build.EventProgress, Payload: build.ProgressPayload{
			Message:  "building " + c.Name,
			Fraction: float64(i) / float64(len(arm)),
		}})
		out := filepath.Join(s.cfg.StagingDir, c.Name)
		if err := Run(ctx, RunOptions{
			Argv: []string{"go", "build", "-o", out, "./cmd/" + c.Name},
			Dir:  psDir,
			Env:  env,
			Emit: emit,
		}); err != nil {
			return fmt.Errorf("go build %s: %w", c.Name, err)
		}
	}
	emit(build.Event{Kind: build.EventProgress, Payload: build.ProgressPayload{
		Message: "all ARM commands built", Fraction: 1.0,
	}})
	return nil
}
