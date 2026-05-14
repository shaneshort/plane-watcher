package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAllPopulatesPaths(t *testing.T) {
	repo := t.TempDir()
	for _, sub := range []string{
		"ps/cmd/plane-feeder",
		"tools",
		"firmware/petalinux/build",
		"firmware/petalinux/project-spec/meta-user/recipes-pwtools/plane-watcher-tools",
	} {
		_ = os.MkdirAll(filepath.Join(repo, sub), 0o755)
	}
	envBody := "TARGET_HOST=root@board\nVIVADO_SETTINGS=/opt/vivado/settings64.sh\nSSHPASS_PASSWORD=pw\n"
	_ = os.WriteFile(filepath.Join(repo, "tools/plane_watcher.env"), []byte(envBody), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "tools/builder.toml"), []byte(`
[services]
plane-feeder = "/etc/init.d/S99plane-feeder restart"
`), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "ps/cmd/build.toml"), []byte(`
[plane-feeder]
targets = ["arm"]
`), 0o644)
	incPath := filepath.Join(repo, "firmware/petalinux/project-spec/meta-user/recipes-pwtools/plane-watcher-tools/plane-watcher-tools-binaries.inc")
	_ = os.WriteFile(incPath, []byte("stub\n"), 0o644)

	cfg, err := LoadAll(LoadAllOptions{RepoRoot: repo})
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if cfg.TargetHost != "root@board" {
		t.Errorf("TargetHost = %q", cfg.TargetHost)
	}
	if cfg.Services["plane-feeder"] == "" {
		t.Errorf("Services not loaded: %+v", cfg.Services)
	}
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "plane-feeder" {
		t.Errorf("Commands not discovered: %+v", cfg.Commands)
	}
	if cfg.StagingDir == "" || cfg.LogDir == "" || cfg.LockPath == "" || cfg.RecipeIncludePath == "" {
		t.Errorf("paths not set: %+v", cfg)
	}
}
