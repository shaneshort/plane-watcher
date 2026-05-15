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
	if len(cfg.LastRun.Stages) == 0 && cfg.LastRun.Deploy == "" {
		// First-run defaults: the common "full rebuild and deploy" recipe.
		// Captured from the typical workflow; subsequent runs persist
		// whatever the user actually used via SaveLastRun.
		m.sel.Bitstream = true
		m.sel.Petalinux = true
		m.sel.PSSlipstream = true
		m.deploy = build.DeploySSH
		m.sel.Reboot = true
	} else {
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
		m.sel.Reboot = cfg.Defaults.RebootAfterSSHDeploy
	}
	m.sel.Deploy = m.deploy
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
			if err := m.sel.Validate(); err != nil {
				return m, nil
			}
			// Refuse to exit the composer with an empty plan; the run
			// screen would otherwise show nothing for a millisecond and
			// then exit, which reads as "just exits" to the user.
			if !m.sel.Bitstream && !m.sel.Petalinux && !m.sel.PSSlipstream && !m.sel.PSHotswap {
				return m, nil
			}
			m.startRun = true
			return m, tea.Quit
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

func (m *composerModel) View() string {
	// Stages section.
	var stages strings.Builder
	for i, it := range m.items {
		cursor := " "
		if i == m.cursor {
			cursor = styleCursor
		}
		mark := styleUncheckedMark
		if it.get(&m.sel) {
			mark = styleCheckedMark
		}
		label := it.label
		if i == m.cursor {
			label = styleYellow.Render(label)
		}
		stages.WriteString(fmt.Sprintf("%s %s %s\n", cursor, mark, label))
	}
	stagesBox := styleBorder.Render(
		styleHeading.Render("Stages") + "\n" + strings.TrimRight(stages.String(), "\n"),
	)

	// Deploy section.
	var deploy strings.Builder
	deploy.WriteString(fmt.Sprintf("(%s) 1  None\n", radio(m.deploy == build.DeployNone)))
	deploy.WriteString(fmt.Sprintf("(%s) 2  SSH\n", radio(m.deploy == build.DeploySSH)))
	deploy.WriteString(fmt.Sprintf("(%s) 3  SD\n", radio(m.deploy == build.DeploySD)))
	rebootMark := styleUncheckedMark
	if m.sel.Reboot {
		rebootMark = styleCheckedMark
	}
	rebootLine := fmt.Sprintf("%s b  Reboot after SSH deploy", rebootMark)
	if m.sel.Deploy != build.DeploySSH {
		rebootLine = styleHint.Render(rebootLine + "  (disabled)")
	}
	deploy.WriteString(rebootLine)
	deployBox := styleBorder.Render(
		styleHeading.Render("Deploy") + "\n" + deploy.String(),
	)

	twoCol := lipgloss.JoinHorizontal(lipgloss.Top, stagesBox, "  ", deployBox)

	// Effective plan.
	var plan string
	if p, err := build.BuildPlan(m.sel, m.cfg); err != nil {
		plan = styleHint.Render("invalid: " + err.Error())
	} else {
		names := make([]string, len(p.Steps))
		for i, s := range p.Steps {
			names[i] = styleYellow.Render(s.Name())
		}
		if len(names) == 0 {
			plan = styleHint.Render("(nothing selected)")
		} else {
			plan = strings.Join(names, styleArrow)
		}
	}
	planBox := styleBorder.Render(
		styleHeading.Render("Effective plan") + "\n" + plan,
	)

	hint := styleHint.Render("[space] toggle   [1/2/3] deploy   [b] reboot   [a] advanced   [r] run   [q] quit")

	var b strings.Builder
	b.WriteString(styleTitle.Render("builder"))
	b.WriteString("\n\n")
	b.WriteString(twoCol)
	b.WriteString("\n\n")
	b.WriteString(planBox)
	b.WriteString("\n\n")
	b.WriteString(hint)
	b.WriteString("\n")
	return b.String()
}

func radio(on bool) string {
	if on {
		return lipgloss.NewStyle().Foreground(clrYellowHi).Bold(true).Render("•")
	}
	return " "
}
