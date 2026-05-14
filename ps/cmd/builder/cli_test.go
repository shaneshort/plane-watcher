package main

import (
	"strings"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
)

func TestParseFlagsBitstreamOnly(t *testing.T) {
	sel, _, err := parseFlags([]string{"--bitstream"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !sel.Bitstream || sel.Petalinux {
		t.Errorf("unexpected selections: %+v", sel)
	}
}

func TestParseFlagsAllStages(t *testing.T) {
	sel, _, err := parseFlags([]string{"--bitstream", "--petalinux", "--ps-slipstream", "--deploy=ssh", "--reboot"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !sel.Bitstream || !sel.Petalinux || !sel.PSSlipstream || sel.Deploy != build.DeploySSH || !sel.Reboot {
		t.Errorf("unexpected selections: %+v", sel)
	}
}

func TestParseFlagsDeployAliases(t *testing.T) {
	for _, in := range []string{"--deploy=sd", "--deploy=SD"} {
		sel, _, err := parseFlags([]string{"--petalinux", in})
		if err != nil {
			t.Fatalf("parseFlags(%q): %v", in, err)
		}
		if sel.Deploy != build.DeploySD {
			t.Errorf("deploy mode for %q = %s", in, sel.Deploy)
		}
	}
}

func TestParseFlagsInvalidDeployValue(t *testing.T) {
	_, _, err := parseFlags([]string{"--deploy=cloud"})
	if err == nil || !strings.Contains(err.Error(), "deploy") {
		t.Errorf("expected error for unknown deploy value, got %v", err)
	}
}

func TestParseFlagsHasInteractiveModeWhenEmpty(t *testing.T) {
	_, interactive, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !interactive {
		t.Errorf("expected interactive=true when no flags supplied")
	}
}
