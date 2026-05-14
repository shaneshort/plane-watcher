package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type petalinuxStep struct {
	cfg       *config.Config
	skipBuild bool
	deps      []string
}

func NewPetalinuxStep(cfg *config.Config, sel build.Selections) (build.Step, error) {
	if cfg.RepoRoot == "" {
		return nil, fmt.Errorf("petalinux: cfg.RepoRoot empty")
	}
	var deps []string
	if sel.Bitstream && sel.Petalinux && !sel.SkipFSBLClean {
		deps = []string{"vivado", "petalinux-fsbl-clean"}
	} else if sel.Bitstream {
		deps = []string{"vivado"}
	}
	if sel.PSSlipstream {
		deps = append(deps, "ps-stage")
	}
	return &petalinuxStep{
		cfg:       cfg,
		skipBuild: sel.Bitstream && !sel.Petalinux,
		deps:      deps,
	}, nil
}

func (s *petalinuxStep) Name() string        { return "petalinux" }
func (s *petalinuxStep) DependsOn() []string { return s.deps }

func (s *petalinuxStep) Run(ctx context.Context, emit func(build.Event)) error {
	script := filepath.Join(s.cfg.RepoRoot, "tools", "build-petalinux.sh")
	env := envForStep(s.cfg)
	if s.skipBuild {
		env = mergeEnv(env, map[string]string{"SKIP_BUILD": "1"})
	}
	return Run(ctx, RunOptions{
		Argv: []string{script},
		Dir:  s.cfg.RepoRoot,
		Env:  env,
		Emit: emit,
	})
}
