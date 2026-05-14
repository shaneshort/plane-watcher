package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type composerItem struct {
	label string
	get   func(*build.Selections) bool
	set   func(*build.Selections, bool)
}

type composerModel struct {
	cfg      *config.Config
	sel      build.Selections
	cursor   int
	items    []composerItem
	deploy   build.DeployMode
	startRun bool
	openAdv  bool
	quit     bool
}

func newComposerModel(cfg *config.Config) *composerModel {
	m := &composerModel{cfg: cfg}
	for _, s := range cfg.LastRun.Stages {
		switch s {
		case "bitstream":
			m.sel.Bitstream = true
		case "petalinux":
			m.sel.Petalinux = true
		case "ps-slipstream":
			m.sel.PSSlipstream = true
		case "ps-hotswap":
			m.sel.PSHotswap = true
		}
	}
	switch cfg.LastRun.Deploy {
	case "ssh":
		m.deploy = build.DeploySSH
	case "sd":
		m.deploy = build.DeploySD
	}
	m.sel.Deploy = m.deploy
	m.sel.Reboot = cfg.Defaults.RebootAfterSSHDeploy
	m.items = []composerItem{
		{label: "Bitstream (Vivado)", get: func(s *build.Selections) bool { return s.Bitstream }, set: func(s *build.Selections, v bool) { s.Bitstream = v }},
		{label: "Petalinux full build", get: func(s *build.Selections) bool { return s.Petalinux }, set: func(s *build.Selections, v bool) { s.Petalinux = v }},
		{label: "PS tools — slipstream", get: func(s *build.Selections) bool { return s.PSSlipstream }, set: func(s *build.Selections, v bool) {
			s.PSSlipstream = v
			if v {
				s.PSHotswap = false
			}
		}},
		{label: "PS tools — hot-swap", get: func(s *build.Selections) bool { return s.PSHotswap }, set: func(s *build.Selections, v bool) {
			s.PSHotswap = v
			if v {
				s.PSSlipstream = false
			}
		}},
	}
	return m
}

func (m *composerModel) Init() tea.Cmd { return nil }

func (m *composerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "q":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case " ", "enter":
			m.toggleAtCursor()
		case "1":
			m.deploy = build.DeployNone
			m.sel.Deploy = build.DeployNone
		case "2":
			m.deploy = build.DeploySSH
			m.sel.Deploy = build.DeploySSH
		case "3":
			m.deploy = build.DeploySD
			m.sel.Deploy = build.DeploySD
		case "b":
			if m.sel.Deploy == build.DeploySSH {
				m.sel.Reboot = !m.sel.Reboot
			}
		case "a":
			m.openAdv = true
			return m, tea.Quit
		case "r":
			if err := m.sel.Validate(); err == nil {
				m.startRun = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *composerModel) toggleAtCursor() {
	if m.cursor < len(m.items) {
		it := m.items[m.cursor]
		it.set(&m.sel, !it.get(&m.sel))
	}
}

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	hintStyle    = lipgloss.NewStyle().Faint(true)
)

func (m *composerModel) View() string {
	var b strings.Builder
	b.WriteString(headingStyle.Render("builder"))
	b.WriteString("\n\n")
	b.WriteString(headingStyle.Render("Stages"))
	b.WriteString("\n")
	for i, it := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		mark := "[ ]"
		if it.get(&m.sel) {
			mark = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", cursor, mark, it.label))
	}
	b.WriteString("\n")
	b.WriteString(headingStyle.Render("Deploy"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  (%s) 1 None\n", radio(m.deploy == build.DeployNone)))
	b.WriteString(fmt.Sprintf("  (%s) 2 SSH\n", radio(m.deploy == build.DeploySSH)))
	b.WriteString(fmt.Sprintf("  (%s) 3 SD\n", radio(m.deploy == build.DeploySD)))
	rebootMark := "[ ]"
	if m.sel.Reboot {
		rebootMark = "[x]"
	}
	rebootLine := fmt.Sprintf("  %s b Reboot after SSH deploy", rebootMark)
	if m.sel.Deploy != build.DeploySSH {
		rebootLine = hintStyle.Render(rebootLine + "  (disabled)")
	}
	b.WriteString(rebootLine)
	b.WriteString("\n\n")
	b.WriteString(headingStyle.Render("Effective plan"))
	b.WriteString("\n")
	if plan, err := build.BuildPlan(m.sel, m.cfg); err != nil {
		b.WriteString(hintStyle.Render("  invalid: " + err.Error()))
	} else {
		names := make([]string, len(plan.Steps))
		for i, s := range plan.Steps {
			names[i] = s.Name()
		}
		if len(names) == 0 {
			b.WriteString(hintStyle.Render("  (nothing selected)"))
		} else {
			b.WriteString("  " + strings.Join(names, " → "))
		}
	}
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("[space] toggle  [1/2/3] deploy  [b] reboot  [a] advanced  [r] run  [q] quit"))
	b.WriteString("\n")
	return b.String()
}

func radio(on bool) string {
	if on {
		return "•"
	}
	return " "
}
