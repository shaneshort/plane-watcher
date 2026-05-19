package build

import (
	"strings"
	"testing"
)

func planNames(p Plan) []string {
	out := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = s.Name()
	}
	return out
}

func TestPlanBitstreamOnly(t *testing.T) {
	p, err := BuildPlan(Selections{Bitstream: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "vivado,yocto,package-boot" {
		t.Errorf("bitstream-only plan: %s", got)
	}
}

func TestPlanYoctoOnly(t *testing.T) {
	p, err := BuildPlan(Selections{Yocto: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "yocto,package-boot" {
		t.Errorf("yocto-only plan: %s", got)
	}
}

func TestPlanFullRebuild(t *testing.T) {
	p, err := BuildPlan(Selections{Bitstream: true, Yocto: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "vivado,yocto-fsbl-clean,yocto,package-boot" {
		t.Errorf("full plan: %s", got)
	}
}

func TestPlanFullRebuildSkipFSBL(t *testing.T) {
	p, err := BuildPlan(Selections{Bitstream: true, Yocto: true, SkipFSBLClean: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "vivado,yocto,package-boot" {
		t.Errorf("skip-fsbl plan: %s", got)
	}
}

func TestPlanSlipstream(t *testing.T) {
	p, err := BuildPlan(Selections{Yocto: true, PSSlipstream: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	want := "ps-slipstream-verify,ps-build,ps-stage,yocto,package-boot"
	if got != want {
		t.Errorf("slipstream plan: got %s, want %s", got, want)
	}
}

func TestPlanHotswap(t *testing.T) {
	p, err := BuildPlan(Selections{PSHotswap: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "ps-build,deploy-ssh-ps,restart-services" {
		t.Errorf("hotswap plan: %s", got)
	}
}

func TestPlanHotswapNoRestart(t *testing.T) {
	p, err := BuildPlan(Selections{PSHotswap: true, NoRestart: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "ps-build,deploy-ssh-ps" {
		t.Errorf("no-restart plan: %s", got)
	}
}

func TestPlanDeploySSH(t *testing.T) {
	p, err := BuildPlan(Selections{Bitstream: true, Yocto: true, Deploy: DeploySSH, Reboot: true}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "vivado,yocto-fsbl-clean,yocto,package-boot,deploy-ssh" {
		t.Errorf("ssh deploy plan: %s", got)
	}
}

func TestPlanDeploySD(t *testing.T) {
	p, err := BuildPlan(Selections{Yocto: true, Deploy: DeploySD}, testCfg(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := strings.Join(planNames(p), ",")
	if got != "yocto,package-boot,deploy-sd" {
		t.Errorf("sd deploy plan: %s", got)
	}
}

func TestPlanRejectsInvalidSelections(t *testing.T) {
	_, err := BuildPlan(Selections{PSSlipstream: true, PSHotswap: true}, testCfg(t))
	if err == nil {
		t.Fatal("expected error for both PS modes")
	}
}
