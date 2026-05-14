package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/plane-watcher/plane-feeder/internal/build"
)

// parseFlags returns the user's Selections plus a boolean indicating
// whether the TUI should launch (no stage flags supplied). Subcommands
// (sync-recipe) are handled separately in main; this function only
// understands the run flags.
func parseFlags(args []string) (build.Selections, bool, error) {
	fs := flag.NewFlagSet("builder", flag.ContinueOnError)
	var (
		bitstream    = fs.Bool("bitstream", false, "build Vivado bitstream + XSA")
		petalinux    = fs.Bool("petalinux", false, "run full petalinux-build")
		psSlipstream = fs.Bool("ps-slipstream", false, "cross-build PS tools and slipstream into rootfs")
		psHotswap    = fs.Bool("ps-hotswap", false, "cross-build PS tools and hot-swap to running board")
		deploy       = fs.String("deploy", "none", "deploy mode: none|ssh|sd")
		reboot       = fs.Bool("reboot", false, "ssh: reboot after deploy")
		skipFSBL     = fs.Bool("skip-fsbl-clean", false, "advanced: skip FSBL cleansstate (stale FSBL warning applies)")
		noRestart    = fs.Bool("no-restart", false, "ps-hotswap: do not restart services after deploy")
		eject        = fs.Bool("eject", false, "sd: eject SD_DEVICE after copy")
	)
	if err := fs.Parse(args); err != nil {
		return build.Selections{}, false, err
	}
	dep, err := parseDeployMode(*deploy)
	if err != nil {
		return build.Selections{}, false, err
	}
	sel := build.Selections{
		Bitstream:     *bitstream,
		Petalinux:     *petalinux,
		PSSlipstream:  *psSlipstream,
		PSHotswap:     *psHotswap,
		Deploy:        dep,
		Reboot:        *reboot,
		SkipFSBLClean: *skipFSBL,
		NoRestart:     *noRestart,
		Eject:         *eject,
	}
	interactive := !*bitstream && !*petalinux && !*psSlipstream && !*psHotswap && dep == build.DeployNone
	return sel, interactive, nil
}

func parseDeployMode(s string) (build.DeployMode, error) {
	switch strings.ToLower(s) {
	case "", "none":
		return build.DeployNone, nil
	case "ssh":
		return build.DeploySSH, nil
	case "sd":
		return build.DeploySD, nil
	default:
		return build.DeployNone, fmt.Errorf("unknown --deploy value: %q (want none|ssh|sd)", s)
	}
}
