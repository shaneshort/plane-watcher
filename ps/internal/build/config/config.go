package config

// Config holds everything needed to construct a Plan and run its steps.
// Built from: plane_watcher.env (env package), tools/builder.toml,
// tools/builder.local.toml, ps/cmd/build.toml. Values are resolved once
// at startup; per-run overrides applied via the advanced pane mutate a
// copy.
type Config struct {
	// From plane_watcher.env
	TargetHost     string
	SSHPassword    string
	VivadoSettings string
	ProjectDir     string
	BuildDeployDir string
	SDMount        string
	SDDevice       string
	TargetImageDir string
	RemoteDir      string

	// From tools/builder.toml [services] map: binary name -> ssh-side restart command
	Services map[string]string

	// From tools/builder.toml [defaults]
	Defaults Defaults

	// From tools/builder.local.toml [last_run]
	LastRun LastRun

	// Discovered from ps/cmd/* and ps/cmd/build.toml
	Commands []CommandMeta

	// Fixed paths derived from RepoRoot
	RepoRoot   string
	StagingDir string // firmware/petalinux/build/pwbuild-staging
	LogDir     string // firmware/petalinux/build/builder-logs
	LockPath   string // firmware/petalinux/build/.builder.lock

	// Recipe paths
	RecipeIncludePath string
	BuildTOMLPath     string
	PlaneWatcherEnv   string
	RawEnv            map[string]string
}

type Defaults struct {
	RebootAfterSSHDeploy bool `toml:"reboot_after_ssh_deploy"`
	SDEjectAfterCopy     bool `toml:"sd_eject_after_copy"`
}

type LastRun struct {
	Stages []string `toml:"stages"`
	Deploy string   `toml:"deploy"`
}

// CommandMeta is one entry from ps/cmd/build.toml combined with the
// fact that a corresponding ps/cmd/<name>/ directory exists.
type CommandMeta struct {
	Name    string
	Targets []string // subset of {"host", "arm"}
	Service string   // optional restart hint
}

func (m CommandMeta) BuildsForARM() bool {
	for _, t := range m.Targets {
		if t == "arm" {
			return true
		}
	}
	return false
}

func (m CommandMeta) BuildsForHost() bool {
	for _, t := range m.Targets {
		if t == "host" {
			return true
		}
	}
	return false
}
