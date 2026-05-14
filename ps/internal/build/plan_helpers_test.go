package build

import (
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		RepoRoot:       "/repo",
		StagingDir:     "/repo/firmware/petalinux/build/pwbuild-staging",
		TargetHost:     "root@board",
		SSHPassword:    "x",
		TargetImageDir: "/run/media/mmcblk0p1",
		RemoteDir:      "/usr/local/bin",
		VivadoSettings: "/opt/vivado/2025.2/Vivado/settings64.sh",
		ProjectDir:     "/repo/build/smartzynq/vivado",
		BuildDeployDir: "/repo/build/smartzynq/deploy",
		SDMount:        "/media/x/SD",
		Commands: []config.CommandMeta{
			{Name: "plane-feeder", Targets: []string{"arm"}, Service: "S99plane-feeder"},
			{Name: "dump-capture", Targets: []string{"arm"}},
		},
		Services: map[string]string{
			"plane-feeder": "/etc/init.d/S99plane-feeder restart",
		},
		RecipeIncludePath: "/repo/firmware/.../plane-watcher-tools-binaries.inc",
	}
}
