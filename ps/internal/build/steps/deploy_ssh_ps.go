package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type deploySSHPSStep struct {
	cfg *config.Config
}

func NewDeploySSHPSStep(cfg *config.Config) (build.Step, error) {
	if cfg.StagingDir == "" || cfg.TargetHost == "" || cfg.SSHPassword == "" || cfg.RemoteDir == "" {
		return nil, fmt.Errorf("deploy-ssh-ps: missing StagingDir/TargetHost/SSHPassword/RemoteDir")
	}
	return &deploySSHPSStep{cfg: cfg}, nil
}

func (s *deploySSHPSStep) Name() string        { return "deploy-ssh-ps" }
func (s *deploySSHPSStep) DependsOn() []string { return []string{"ps-build"} }

func (s *deploySSHPSStep) Run(ctx context.Context, emit func(build.Event)) error {
	arm := config.ARMCommands(s.cfg.Commands)
	if len(arm) == 0 {
		emit(build.Event{Kind: build.EventWarning, Payload: "no ARM commands to deploy"})
		return nil
	}
	// 1. scp all binaries into /tmp/ on the target.
	scpArgv := []string{"sshpass", "-p" + s.cfg.SSHPassword, "scp"}
	scpArgv = append(scpArgv, sshCommonOpts...)
	scpArgv = append(scpArgv, "-O")
	for _, c := range arm {
		scpArgv = append(scpArgv, filepath.Join(s.cfg.StagingDir, c.Name))
	}
	scpArgv = append(scpArgv, s.cfg.TargetHost+":/tmp/")
	if err := Run(ctx, RunOptions{Argv: scpArgv, Env: envForStep(s.cfg), Emit: emit}); err != nil {
		return err
	}
	// 2. atomic mv each /tmp/<name> -> RemoteDir/<name>.
	mvScript := ""
	for _, c := range arm {
		mvScript += fmt.Sprintf("mv -f /tmp/%s %s/%s; ", c.Name, s.cfg.RemoteDir, c.Name)
	}
	mvArgv := []string{"sshpass", "-p" + s.cfg.SSHPassword, "ssh"}
	mvArgv = append(mvArgv, sshCommonOpts...)
	mvArgv = append(mvArgv, s.cfg.TargetHost, mvScript)
	return Run(ctx, RunOptions{Argv: mvArgv, Env: envForStep(s.cfg), Emit: emit})
}
