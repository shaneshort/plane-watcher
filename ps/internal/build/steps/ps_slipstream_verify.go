package steps

import (
	"context"
	"fmt"

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
	return nil
}
