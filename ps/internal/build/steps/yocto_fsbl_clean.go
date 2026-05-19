package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type yoctoFSBLCleanStep struct {
	cfg *config.Config
}

func NewYoctoFSBLCleanStep(cfg *config.Config) (build.Step, error) {
	if cfg.RepoRoot == "" {
		return nil, fmt.Errorf("yocto-fsbl-clean: cfg.RepoRoot empty")
	}
	return &yoctoFSBLCleanStep{cfg: cfg}, nil
}

func (s *yoctoFSBLCleanStep) Name() string        { return "yocto-fsbl-clean" }
func (s *yoctoFSBLCleanStep) DependsOn() []string { return []string{"vivado"} }

// Run invokes tools/build-yocto.sh with CLEANSSTATE_FSBL=1, which runs
// `bitbake -c cleansstate mc:<MACHINE>-cortexa9-fsbl:fsbl-firmware` and
// exits. Defensive port of the PetaLinux-era petalinux-fsbl-clean step:
// under EDF, bitbake's signature-based caching usually handles bitstream
// changes correctly on its own, but kept available as an explicit knob.
func (s *yoctoFSBLCleanStep) Run(ctx context.Context, emit func(build.Event)) error {
	emit(build.Event{Kind: build.EventProgress, Payload: build.ProgressPayload{
		Message: "cleansstate fsbl-firmware",
	}})
	script := filepath.Join(s.cfg.RepoRoot, "tools", "build-yocto.sh")
	env := mergeEnv(envForStep(s.cfg), map[string]string{"CLEANSSTATE_FSBL": "1"})
	return Run(ctx, RunOptions{
		Argv: []string{script},
		Dir:  s.cfg.RepoRoot,
		Env:  env,
		Emit: emit,
	})
}
