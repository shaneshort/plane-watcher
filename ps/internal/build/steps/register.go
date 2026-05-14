package steps

import (
	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

// init wires concrete step factories into the build.factories map.
// plan.BuildPlan looks them up by name.
func init() {
	build.Register("vivado", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewVivadoStep(cfg)
	})
	build.Register("ps-slipstream-verify", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewPSSlipstreamVerifyStep(cfg)
	})
	build.Register("ps-build", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewPSBuildStep(cfg)
	})
	build.Register("ps-stage", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewPSStageStep(cfg)
	})
	build.Register("petalinux-fsbl-clean", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewPetalinuxFSBLCleanStep(cfg)
	})
	build.Register("petalinux", func(cfg *config.Config, sel build.Selections) (build.Step, error) {
		return NewPetalinuxStep(cfg, sel)
	})
	build.Register("package-boot", func(cfg *config.Config, sel build.Selections) (build.Step, error) {
		return NewPackageBootStep(cfg, sel)
	})
	build.Register("deploy-ssh", func(cfg *config.Config, sel build.Selections) (build.Step, error) {
		return NewDeploySSHStep(cfg, sel)
	})
	build.Register("deploy-sd", func(cfg *config.Config, sel build.Selections) (build.Step, error) {
		return NewDeploySDStep(cfg, sel)
	})
	build.Register("deploy-ssh-ps", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewDeploySSHPSStep(cfg)
	})
	build.Register("restart-services", func(cfg *config.Config, _ build.Selections) (build.Step, error) {
		return NewRestartServicesStep(cfg)
	})
}
