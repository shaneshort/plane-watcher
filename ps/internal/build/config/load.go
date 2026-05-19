package config

import (
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build/env"
)

type LoadAllOptions struct {
	RepoRoot      string
	EnvPath       string // override; default tools/plane_watcher.env
	EnvExplicit   bool
	TrackedTOML   string // override; default tools/builder.toml
	LocalTOML     string // override; default tools/builder.local.toml
	BuildTOMLPath string // override; default ps/cmd/build.toml
}

// LoadAll resolves every config input into a Config. Missing optional
// files are silent; missing explicit overrides are errors.
func LoadAll(o LoadAllOptions) (*Config, error) {
	if o.EnvPath == "" {
		o.EnvPath = filepath.Join(o.RepoRoot, "tools", "plane_watcher.env")
	}
	if o.TrackedTOML == "" {
		o.TrackedTOML = filepath.Join(o.RepoRoot, "tools", "builder.toml")
	}
	if o.LocalTOML == "" {
		o.LocalTOML = filepath.Join(o.RepoRoot, "tools", "builder.local.toml")
	}
	if o.BuildTOMLPath == "" {
		o.BuildTOMLPath = filepath.Join(o.RepoRoot, "ps", "cmd", "build.toml")
	}

	cfg, err := LoadBuilderTOML(o.TrackedTOML, o.LocalTOML)
	if err != nil {
		return nil, err
	}
	cfg.RepoRoot = o.RepoRoot
	cfg.PlaneWatcherEnv = o.EnvPath

	raw, err := env.Load(env.LoadOptions{
		Path:     o.EnvPath,
		RepoRoot: o.RepoRoot,
		Explicit: o.EnvExplicit,
	})
	if err != nil {
		return nil, err
	}
	cfg.RawEnv = raw

	cfg.TargetHost = raw["TARGET_HOST"]
	cfg.SSHPassword = raw["SSHPASS_PASSWORD"]
	cfg.VivadoSettings = raw["VIVADO_SETTINGS"]
	cfg.ProjectDir = firstNonEmpty(raw["PROJECT_DIR"], filepath.Join(o.RepoRoot, "build", "smartzynq", "vivado"))
	cfg.BuildDeployDir = firstNonEmpty(raw["DEPLOY_DIR"], filepath.Join(o.RepoRoot, "build", "smartzynq", "deploy"))
	cfg.SDMount = raw["SD_MOUNT"]
	cfg.SDDevice = raw["SD_DEVICE"]
	cfg.TargetImageDir = raw["TARGET_IMAGE_DIR"]
	cfg.RemoteDir = firstNonEmpty(raw["REMOTE_DIR"], "/usr/local/bin")

	cmds, err := DiscoverCommands(CmdRootRelative(o.RepoRoot), o.BuildTOMLPath)
	if err != nil {
		return nil, err
	}
	cfg.Commands = cmds
	cfg.BuildTOMLPath = o.BuildTOMLPath

	// StagingDir / LogDir / LockPath still live under firmware/petalinux/build/
	// because the EDF meta-plane-watcher layer's PWBUILD_STAGING_DIR default in
	// conf/layer.conf points there. Phase E (container + wrapper orchestration)
	// is the natural moment to lift these out of firmware/petalinux/; doing it
	// here would require a coordinated layer.conf change.
	cfg.StagingDir = filepath.Join(o.RepoRoot, "firmware", "petalinux", "build", "pwbuild-staging")
	cfg.LogDir = filepath.Join(o.RepoRoot, "firmware", "petalinux", "build", "builder-logs")
	cfg.LockPath = filepath.Join(o.RepoRoot, "firmware", "petalinux", "build", ".builder.lock")
	cfg.RecipeIncludePath = filepath.Join(
		o.RepoRoot,
		"firmware", "yocto", "meta-plane-watcher",
		"recipes-pwtools", "plane-watcher-tools",
		"plane-watcher-tools-binaries.inc",
	)
	return cfg, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
