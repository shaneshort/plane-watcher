package build

import (
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

// Plan is a topologically sorted list of Steps to execute.
type Plan struct {
	Steps []Step
}

// stepFactory is a function each step file registers so plan.go can stay
// independent of concrete step implementations. The map is wired in
// package init by the steps package (steps.init).
type stepFactory func(cfg *config.Config, sel Selections) (Step, error)

var factories = map[string]stepFactory{}

// Register is called by the steps package init to provide concrete
// implementations. plan.go names each step it needs by string; the
// factories map resolves the name to a concrete Step.
func Register(name string, f stepFactory) {
	factories[name] = f
}

// BuildPlan is a pure function from selections+config to a topologically
// sorted Plan. It does not touch the filesystem or environment beyond
// what cfg already encodes.
func BuildPlan(sel Selections, cfg *config.Config) (Plan, error) {
	if err := sel.Validate(); err != nil {
		return Plan{}, err
	}

	// 1. Pick step names from selections (the truth table).
	var names []string
	if sel.PSSlipstream {
		names = append(names, "ps-slipstream-verify", "ps-build", "ps-stage")
	}
	if sel.Bitstream {
		names = append(names, "vivado")
	}
	if sel.Bitstream && sel.Petalinux && !sel.SkipFSBLClean {
		names = append(names, "petalinux-fsbl-clean")
	}
	if sel.Petalinux || (sel.Bitstream && !sel.PSHotswap) {
		// Bitstream alone still runs the petalinux leaf script in
		// SKIP_BUILD=1 mode to repackage BOOT.BIN. The step instance reads
		// sel.Petalinux to know whether to skip the full build.
		names = append(names, "petalinux")
	}
	if sel.Bitstream || sel.Petalinux {
		names = append(names, "package-boot")
	}
	if sel.PSHotswap {
		// PSHotswap has its own deploy chain; does not use deploy-ssh/sd
		// (those move BOOT.BIN/image.ub, not Go tools). Cross-builds
		// reuse ps-build only if it isn't already in the plan.
		if !contains(names, "ps-build") {
			names = append(names, "ps-build")
		}
		names = append(names, "deploy-ssh-ps")
		if !sel.NoRestart {
			names = append(names, "restart-services")
		}
	}
	switch sel.Deploy {
	case DeploySSH:
		if sel.Bitstream || sel.Petalinux {
			names = append(names, "deploy-ssh")
		}
	case DeploySD:
		if sel.Bitstream || sel.Petalinux {
			names = append(names, "deploy-sd")
		}
	}

	// 2. Instantiate steps via registered factories.
	steps := make([]Step, 0, len(names))
	for _, n := range names {
		f, ok := factories[n]
		if !ok {
			return Plan{}, &MissingFactoryError{Name: n}
		}
		s, err := f(cfg, sel)
		if err != nil {
			return Plan{}, err
		}
		steps = append(steps, s)
	}

	// 3. Topo-sort (steps must declare their own DependsOn).
	sorted, err := TopoSort(steps)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Steps: sorted}, nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

type MissingFactoryError struct{ Name string }

func (e *MissingFactoryError) Error() string {
	return "no factory registered for step: " + e.Name
}
