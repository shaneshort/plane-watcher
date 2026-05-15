package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
	"github.com/plane-watcher/plane-feeder/internal/build/recipe"
)

type psSlipstreamVerifyStep struct {
	cfg *config.Config
}

func NewPSSlipstreamVerifyStep(cfg *config.Config) (build.Step, error) {
	if cfg.RecipeIncludePath == "" {
		return nil, fmt.Errorf("ps-slipstream-verify: cfg.RecipeIncludePath empty")
	}
	return &psSlipstreamVerifyStep{cfg: cfg}, nil
}

func (s *psSlipstreamVerifyStep) Name() string        { return "ps-slipstream-verify" }
func (s *psSlipstreamVerifyStep) DependsOn() []string { return nil }

func (s *psSlipstreamVerifyStep) Run(_ context.Context, emit func(build.Event)) error {
	if err := recipe.Verify(s.cfg.RecipeIncludePath, s.cfg.Commands); err != nil {
		return err
	}
	emit(build.Event{Kind: build.EventLogLine, Payload: "recipe .inc matches ps/cmd/build.toml"})

	// Defaults-file drift: catches the case where someone edits
	// ps/cmd/plane-feeder/plane-feeder.default but forgets to run
	// `builder sync-recipe --write`. Skip silently if the defaults file
	// has never been synced yet (recipe scaffolding is a separate step).
	recipeDir := filepath.Dir(s.cfg.RecipeIncludePath)
	if err := recipe.VerifyDefaultsFile(s.cfg.RepoRoot, recipeDir); err != nil {
		return err
	}
	emit(build.Event{Kind: build.EventLogLine, Payload: "recipe defaults file matches ps/cmd/plane-feeder/plane-feeder.default"})
	return nil
}
