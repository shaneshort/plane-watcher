package build

import (
	"context"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type stubStep struct {
	name string
	deps []string
}

func (s stubStep) Name() string                            { return s.name }
func (s stubStep) DependsOn() []string                     { return s.deps }
func (s stubStep) Run(context.Context, func(Event)) error  { return nil }

func init() {
	stubs := map[string][]string{
		"vivado":                nil,
		"ps-slipstream-verify":  nil,
		"ps-build":              nil,
		"ps-stage":              {"ps-build"},
		"yocto-fsbl-clean":  {"vivado"},
		"yocto":             nil, // deps set below per selection
		"package-boot":          {"yocto"},
		"deploy-ssh":            {"package-boot"},
		"deploy-sd":             {"package-boot"},
		"deploy-ssh-ps":         {"ps-build"},
		"restart-services":      {"deploy-ssh-ps"},
	}
	for n, d := range stubs {
		name, deps := n, d
		Register(name, func(cfg *config.Config, sel Selections) (Step, error) {
			if name == "yocto" {
				if sel.Bitstream {
					if sel.Yocto && !sel.SkipFSBLClean {
						return stubStep{name: name, deps: []string{"vivado", "yocto-fsbl-clean"}}, nil
					}
					return stubStep{name: name, deps: []string{"vivado"}}, nil
				}
				if sel.PSSlipstream {
					return stubStep{name: name, deps: []string{"ps-stage"}}, nil
				}
			}
			return stubStep{name: name, deps: deps}, nil
		})
	}
}
