package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBuilderTOMLTrackedOnly(t *testing.T) {
	dir := t.TempDir()
	tracked := filepath.Join(dir, "builder.toml")
	body := `
[services]
plane-feeder = "/etc/init.d/S99plane-feeder restart"

[defaults]
reboot_after_ssh_deploy = false
sd_eject_after_copy = false
`
	if err := os.WriteFile(tracked, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBuilderTOML(tracked, filepath.Join(dir, "builder.local.toml"))
	if err != nil {
		t.Fatalf("LoadBuilderTOML: %v", err)
	}
	if got.Services["plane-feeder"] != "/etc/init.d/S99plane-feeder restart" {
		t.Errorf("service map missing entry: %+v", got.Services)
	}
	if got.Defaults.RebootAfterSSHDeploy != false {
		t.Errorf("defaults parse: %+v", got.Defaults)
	}
}

func TestLoadBuilderTOMLLocalOverlay(t *testing.T) {
	dir := t.TempDir()
	tracked := filepath.Join(dir, "builder.toml")
	local := filepath.Join(dir, "builder.local.toml")
	if err := os.WriteFile(tracked, []byte(`
[defaults]
reboot_after_ssh_deploy = false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`
[last_run]
stages = ["bitstream", "petalinux"]
deploy = "ssh"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBuilderTOML(tracked, local)
	if err != nil {
		t.Fatalf("LoadBuilderTOML: %v", err)
	}
	if len(got.LastRun.Stages) != 2 || got.LastRun.Deploy != "ssh" {
		t.Errorf("last-run not loaded: %+v", got.LastRun)
	}
}

func TestLoadBuilderTOMLMissingLocalOK(t *testing.T) {
	dir := t.TempDir()
	tracked := filepath.Join(dir, "builder.toml")
	if err := os.WriteFile(tracked, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBuilderTOML(tracked, filepath.Join(dir, "absent.toml"))
	if err != nil {
		t.Fatalf("expected missing local to be silent, got %v", err)
	}
	if got == nil {
		t.Fatal("nil config")
	}
}

func TestLoadBuilderTOMLMissingTrackedOK(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadBuilderTOML(filepath.Join(dir, "absent.toml"), filepath.Join(dir, "absent-local.toml"))
	if err != nil {
		t.Fatalf("expected missing tracked to default cleanly, got %v", err)
	}
	if got.Services == nil {
		t.Fatal("Services map should be non-nil even when tracked file is absent")
	}
}
