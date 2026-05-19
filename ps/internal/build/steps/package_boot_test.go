package steps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestPackageBootBitstreamOnly(t *testing.T) {
	deploy := t.TempDir()
	if err := os.WriteFile(filepath.Join(deploy, "BOOT.BIN"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BuildDeployDir: deploy}
	s, err := NewPackageBootStep(cfg, build.Selections{Bitstream: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestPackageBootFullRebuildNeedsImage(t *testing.T) {
	deploy := t.TempDir()
	_ = os.WriteFile(filepath.Join(deploy, "BOOT.BIN"), []byte("x"), 0o644)
	cfg := &config.Config{BuildDeployDir: deploy}
	s, _ := NewPackageBootStep(cfg, build.Selections{Yocto: true})
	err := s.Run(context.Background(), func(build.Event) {})
	if err == nil {
		t.Fatal("expected missing-image error")
	}
}
