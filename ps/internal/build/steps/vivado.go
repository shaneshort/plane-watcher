package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type vivadoStep struct {
	cfg *config.Config
}

func NewVivadoStep(cfg *config.Config) (build.Step, error) {
	if cfg.RepoRoot == "" {
		return nil, fmt.Errorf("vivado step: cfg.RepoRoot empty")
	}
	return &vivadoStep{cfg: cfg}, nil
}

func (s *vivadoStep) Name() string        { return "vivado" }
func (s *vivadoStep) DependsOn() []string { return nil }

func (s *vivadoStep) Run(ctx context.Context, emit func(build.Event)) error {
	script := filepath.Join(s.cfg.RepoRoot, "tools", "build-smartzynq-phase1.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("vivado: %s missing: %w", script, err)
	}
	if err := Run(ctx, RunOptions{
		Argv: []string{script},
		Dir:  s.cfg.RepoRoot,
		Env:  envForStep(s.cfg),
		Emit: emit,
	}); err != nil {
		return err
	}
	rpt := filepath.Join(
		s.cfg.ProjectDir,
		"plane_watcher_phase1.runs", "impl_1",
		"smartzynq_phase1_wrapper_timing_summary_routed.rpt",
	)
	tr, err := ParseTimingReport(rpt)
	if err != nil {
		return fmt.Errorf("parse timing report: %w", err)
	}
	emit(build.Event{Kind: build.EventTimingResult, Payload: tr})
	if !tr.MetConstraints {
		return &TimingViolationError{Report: tr}
	}
	return nil
}

type TimingViolationError struct {
	Report build.TimingResult
}

func (e *TimingViolationError) Error() string {
	return fmt.Sprintf("timing constraints not met (WNS=%g TNS=%g): %s",
		e.Report.WNS, e.Report.TNS, e.Report.ReportPath)
}

// envForStep produces the os.Environ-style env slice for a step,
// overlaying RawEnv from plane_watcher.env over the parent environment.
func envForStep(cfg *config.Config) []string {
	if cfg.RawEnv == nil {
		return os.Environ()
	}
	return mergeEnv(os.Environ(), cfg.RawEnv)
}

func mergeEnv(base []string, overrides map[string]string) []string {
	have := map[string]bool{}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		eq := -1
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				eq = i
				break
			}
		}
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		name := kv[:eq]
		if v, ok := overrides[name]; ok {
			out = append(out, name+"="+v)
			have[name] = true
		} else {
			out = append(out, kv)
		}
	}
	for k, v := range overrides {
		if !have[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}
