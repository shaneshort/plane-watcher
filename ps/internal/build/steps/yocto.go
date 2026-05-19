package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type yoctoStep struct {
	cfg       *config.Config
	skipBuild bool
	deps      []string
}

func NewYoctoStep(cfg *config.Config, sel build.Selections) (build.Step, error) {
	if cfg.RepoRoot == "" {
		return nil, fmt.Errorf("yocto: cfg.RepoRoot empty")
	}
	var deps []string
	if sel.Bitstream && sel.Yocto && !sel.SkipFSBLClean {
		deps = []string{"vivado", "yocto-fsbl-clean"}
	} else if sel.Bitstream {
		deps = []string{"vivado"}
	}
	if sel.PSSlipstream {
		deps = append(deps, "ps-stage")
	}
	return &yoctoStep{
		cfg:       cfg,
		skipBuild: sel.Bitstream && !sel.Yocto,
		deps:      deps,
	}, nil
}

func (s *yoctoStep) Name() string        { return "yocto" }
func (s *yoctoStep) DependsOn() []string { return s.deps }

func (s *yoctoStep) Run(ctx context.Context, emit func(build.Event)) error {
	script := filepath.Join(s.cfg.RepoRoot, "tools", "build-yocto.sh")
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
