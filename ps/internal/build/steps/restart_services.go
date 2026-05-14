package steps

import (
	"context"
	"fmt"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type restartServicesStep struct {
	cfg *config.Config
}

func NewRestartServicesStep(cfg *config.Config) (build.Step, error) {
	if cfg.TargetHost == "" || cfg.SSHPassword == "" {
		return nil, fmt.Errorf("restart-services: TargetHost/SSHPassword required")
	}
	return &restartServicesStep{cfg: cfg}, nil
}

func (s *restartServicesStep) Name() string        { return "restart-services" }
func (s *restartServicesStep) DependsOn() []string { return []string{"deploy-ssh-ps"} }

func (s *restartServicesStep) Run(ctx context.Context, emit func(build.Event)) error {
	for _, c := range config.ARMCommands(s.cfg.Commands) {
		cmd, ok := s.cfg.Services[c.Name]
		if !ok {
			continue
		}
		emit(build.Event{Kind: build.EventLogLine, Payload: "restart " + c.Name + ": " + cmd})
		argv := []string{"sshpass", "-p" + s.cfg.SSHPassword, "ssh"}
		argv = append(argv, sshCommonOpts...)
		argv = append(argv, s.cfg.TargetHost, cmd)
		if err := Run(ctx, RunOptions{Argv: argv, Env: envForStep(s.cfg), Emit: emit}); err != nil {
			return fmt.Errorf("restart %s: %w", c.Name, err)
		}
	}
	return nil
}
