package steps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestVivadoStepNamingAndDeps(t *testing.T) {
	cfg := &config.Config{RepoRoot: "/tmp"}
	s, err := NewVivadoStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "vivado" {
		t.Errorf("Name() = %q", s.Name())
	}
	if len(s.DependsOn()) != 0 {
		t.Errorf("DependsOn() = %v, want []", s.DependsOn())
	}
}

func TestVivadoStepEmitsTimingFromReport(t *testing.T) {
	repo := t.TempDir()
	scriptPath := filepath.Join(repo, "tools", "build-smartzynq-phase1.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho fake-vivado\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rptDir := filepath.Join(repo, "build", "smartzynq", "vivado", "plane_watcher_phase1.runs", "impl_1")
	if err := os.MkdirAll(rptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rptPath := filepath.Join(rptDir, "smartzynq_phase1_wrapper_timing_summary_routed.rpt")
	src, _ := os.ReadFile(filepath.Join("testdata", "timing_met.rpt"))
	if err := os.WriteFile(rptPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RepoRoot:   repo,
		ProjectDir: filepath.Join(repo, "build", "smartzynq", "vivado"),
	}
	s, err := NewVivadoStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var sawTiming bool
	err = s.Run(context.Background(), func(e build.Event) {
		if e.Kind == build.EventTimingResult {
			r := e.Payload.(build.TimingResult)
			if !r.MetConstraints {
				t.Errorf("expected MetConstraints true")
			}
			sawTiming = true
		}
	})
	if err != nil {
		t.Errorf("Run: %v", err)
	}
	if !sawTiming {
		t.Error("expected TimingResult event")
	}
}

func TestVivadoStepFailsOnTimingViolation(t *testing.T) {
	repo := t.TempDir()
	scriptPath := filepath.Join(repo, "tools", "build-smartzynq-phase1.sh")
	_ = os.MkdirAll(filepath.Dir(scriptPath), 0o755)
	_ = os.WriteFile(scriptPath, []byte("#!/bin/bash\nexit 0\n"), 0o755)
	rptDir := filepath.Join(repo, "build", "smartzynq", "vivado", "plane_watcher_phase1.runs", "impl_1")
	_ = os.MkdirAll(rptDir, 0o755)
	src, _ := os.ReadFile(filepath.Join("testdata", "timing_not_met.rpt"))
	_ = os.WriteFile(filepath.Join(rptDir, "smartzynq_phase1_wrapper_timing_summary_routed.rpt"), src, 0o644)

	cfg := &config.Config{RepoRoot: repo, ProjectDir: filepath.Join(repo, "build", "smartzynq", "vivado")}
	s, _ := NewVivadoStep(cfg)
	err := s.Run(context.Background(), func(build.Event) {})
	if err == nil {
		t.Fatal("expected timing-failure error")
	}
	var te *TimingViolationError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TimingViolationError, got %T: %v", err, err)
	}
}
