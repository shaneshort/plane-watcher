package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

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
			if rc := runRunScreenInteractive(cfg, composer.sel); rc != 0 {
				return rc
			}
		default:
			return 0
		}
	}
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
