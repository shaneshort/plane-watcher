package steps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

var ErrEjectNeedsDevice = errors.New("--eject requires SD_DEVICE to be set in plane_watcher.env")

type deploySDStep struct {
	cfg          *config.Config
	includeImage bool
	eject        bool
}

func NewDeploySDStep(cfg *config.Config, sel build.Selections) (build.Step, error) {
	if cfg.BuildDeployDir == "" || cfg.SDMount == "" {
		return nil, fmt.Errorf("deploy-sd: BuildDeployDir and SDMount required")
	}
	if sel.Eject && cfg.SDDevice == "" {
		return nil, ErrEjectNeedsDevice
	}
	return &deploySDStep{
		cfg:          cfg,
		includeImage: sel.Yocto,
		eject:        sel.Eject,
	}, nil
}

func (s *deploySDStep) Name() string        { return "deploy-sd" }
func (s *deploySDStep) DependsOn() []string { return []string{"package-boot"} }

func (s *deploySDStep) Run(ctx context.Context, emit func(build.Event)) error {
	if _, err := os.Stat(s.cfg.SDMount); err != nil {
		return fmt.Errorf("SD mount %s missing: %w", s.cfg.SDMount, err)
	}
	files := []string{"BOOT.BIN"}
	if s.includeImage {
		files = append(files, "image.ub")
	}
	for _, name := range files {
		src := filepath.Join(s.cfg.BuildDeployDir, name)
		dst := filepath.Join(s.cfg.SDMount, name)
		if err := copyFile(src, dst); err != nil {
			return err
		}
		emit(build.Event{Kind: build.EventLogLine, Payload: "copied " + src + " -> " + dst})
	}
	syncBin := "/bin/sync"
	if _, err := os.Stat(syncBin); err == nil {
		_ = Run(ctx, RunOptions{Argv: []string{syncBin}, Emit: emit})
	}
	if s.eject {
		if err := Run(ctx, RunOptions{Argv: []string{"eject", s.cfg.SDDevice}, Emit: emit}); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
