package steps

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type deploySSHStep struct {
	cfg          *config.Config
	includeImage bool
	reboot       bool
}

func NewDeploySSHStep(cfg *config.Config, sel build.Selections) (build.Step, error) {
	if cfg.TargetHost == "" || cfg.SSHPassword == "" || cfg.TargetImageDir == "" {
		return nil, fmt.Errorf("deploy-ssh: TargetHost/SSHPassword/TargetImageDir required")
	}
	return &deploySSHStep{
		cfg:          cfg,
		includeImage: sel.Yocto,
		reboot:       sel.Reboot,
	}, nil
}

func (s *deploySSHStep) Name() string        { return "deploy-ssh" }
func (s *deploySSHStep) DependsOn() []string { return []string{"package-boot"} }

var sshCommonOpts = []string{
	"-F", "/dev/null",
	"-o", "StrictHostKeyChecking=no",
	"-o", "UserKnownHostsFile=/dev/null",
	"-o", "CheckHostIP=no",
	"-o", "PubkeyAuthentication=no",
}

func (s *deploySSHStep) Run(ctx context.Context, emit func(build.Event)) error {
	files := []string{filepath.Join(s.cfg.BuildDeployDir, "BOOT.BIN")}
	if s.includeImage {
		files = append(files, filepath.Join(s.cfg.BuildDeployDir, "image.ub"))
	}
	scpArgv := []string{"sshpass", "-p" + s.cfg.SSHPassword, "scp"}
	scpArgv = append(scpArgv, sshCommonOpts...)
	scpArgv = append(scpArgv, "-O")
	scpArgv = append(scpArgv, files...)
	scpArgv = append(scpArgv, s.cfg.TargetHost+":"+s.cfg.TargetImageDir+"/")
	if err := Run(ctx, RunOptions{Argv: scpArgv, Env: envForStep(s.cfg), Emit: emit}); err != nil {
		return err
	}
	syncArgv := []string{"sshpass", "-p" + s.cfg.SSHPassword, "ssh"}
	syncArgv = append(syncArgv, sshCommonOpts...)
	syncArgv = append(syncArgv, s.cfg.TargetHost, "sync")
	if err := Run(ctx, RunOptions{Argv: syncArgv, Env: envForStep(s.cfg), Emit: emit}); err != nil {
		return err
	}
	if !s.reboot {
		return nil
	}
	rebootArgv := []string{"sshpass", "-p" + s.cfg.SSHPassword, "ssh"}
	rebootArgv = append(rebootArgv, sshCommonOpts...)
	rebootArgv = append(rebootArgv, s.cfg.TargetHost, "reboot")
	// reboot severs the ssh session, treat non-zero exit as success.
	if err := Run(ctx, RunOptions{Argv: rebootArgv, Env: envForStep(s.cfg), Emit: emit}); err != nil {
		emit(build.Event{Kind: build.EventWarning, Payload: "reboot ssh exited non-zero (expected): " + err.Error()})
	}
	return nil
}
