package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

// runTUI is the entry point for interactive mode. It loops: composer
// screen → run pane or advanced pane → back to composer.
func runTUI(cfg *config.Config) int {
	for {
		composer := newComposerModel(cfg)
		p := tea.NewProgram(composer, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "tui:", err)
			return 1
		}
		switch {
		case composer.quit:
			return 0
		case composer.openAdv:
			if rc := runAdvanced(cfg); rc != 0 {
				return rc
			}
		case composer.startRun:
			persistLastRun(cfg, composer.sel)
			if rc := runRunScreenInteractive(cfg, composer.sel); rc != 0 {
				return rc
			}
		default:
			return 0
		}
	}
}

// persistLastRun writes the current Selections to tools/builder.local.toml
// so the next TUI launch defaults to what the user actually ran. Best-
// effort: write errors are swallowed (silent) so a read-only checkout
// doesn't break the run flow.
func persistLastRun(cfg *config.Config, sel build.Selections) {
	lr := config.LastRun{}
	if sel.Bitstream {
		lr.Stages = append(lr.Stages, "bitstream")
	}
	if sel.Petalinux {
		lr.Stages = append(lr.Stages, "petalinux")
	}
	if sel.PSSlipstream {
		lr.Stages = append(lr.Stages, "ps-slipstream")
	}
	if sel.PSHotswap {
		lr.Stages = append(lr.Stages, "ps-hotswap")
	}
	switch sel.Deploy {
	case build.DeploySSH:
		lr.Deploy = "ssh"
	case build.DeploySD:
		lr.Deploy = "sd"
	}
	localPath := filepath.Join(cfg.RepoRoot, "tools", "builder.local.toml")
	_ = config.SaveLastRun(localPath, lr)
}

func runAdvanced(cfg *config.Config) int {
	m := newAdvancedModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		return 1
	}
	return 0
}
