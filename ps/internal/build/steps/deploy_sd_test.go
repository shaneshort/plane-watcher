package steps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestDeploySDCopiesArtefacts(t *testing.T) {
	deploy := t.TempDir()
	sd := t.TempDir()
	_ = os.WriteFile(filepath.Join(deploy, "BOOT.BIN"), []byte("boot"), 0o644)
	_ = os.WriteFile(filepath.Join(deploy, "image.ub"), []byte("img"), 0o644)
	cfg := &config.Config{BuildDeployDir: deploy, SDMount: sd}
	s, err := NewDeploySDStep(cfg, build.Selections{Yocto: true, Deploy: build.DeploySD})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range []string{"BOOT.BIN", "image.ub"} {
		if _, err := os.Stat(filepath.Join(sd, f)); err != nil {
			t.Errorf("%s missing on SD: %v", f, err)
		}
	}
}

func TestDeploySDMissingMountIsError(t *testing.T) {
	deploy := t.TempDir()
	_ = os.WriteFile(filepath.Join(deploy, "BOOT.BIN"), []byte("boot"), 0o644)
	cfg := &config.Config{BuildDeployDir: deploy, SDMount: filepath.Join(t.TempDir(), "missing")}
	s, _ := NewDeploySDStep(cfg, build.Selections{Bitstream: true, Deploy: build.DeploySD})
	err := s.Run(context.Background(), func(build.Event) {})
	if err == nil {
		t.Fatal("expected missing-mount error")
	}
}

func TestDeploySDEjectRequiresDevice(t *testing.T) {
	deploy := t.TempDir()
	sd := t.TempDir()
	_ = os.WriteFile(filepath.Join(deploy, "BOOT.BIN"), []byte("x"), 0o644)
	cfg := &config.Config{BuildDeployDir: deploy, SDMount: sd, SDDevice: ""}
	_, err := NewDeploySDStep(cfg, build.Selections{Bitstream: true, Deploy: build.DeploySD, Eject: true})
	if err == nil {
		t.Fatal("expected error when --eject set with no SDDevice")
	}
	if !errors.Is(err, ErrEjectNeedsDevice) {
		t.Errorf("error = %v, want ErrEjectNeedsDevice", err)
	}
}
