package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type stepState int

const (
	stepPending stepState = iota
	stepRunning
	stepSucceeded
	stepFailed
	stepCancelled
	stepSkipped
)

func (s stepState) String() string {
	switch s {
	case stepPending:
		return "·"
	case stepRunning:
		return "▶"
	case stepSucceeded:
		return "✓"
	case stepFailed:
		return "✗"
	case stepCancelled:
		return "C"
	case stepSkipped:
		return "S"
	}
	return "?"
}

type runModel struct {
	cfg     *config.Config
	stepIDs []string
	state   map[string]stepState
	started map[string]time.Time
	ended   map[string]time.Time
	logs    map[string][]string
	active  string
	timing  *build.TimingResult
	err     error
	cancel  context.CancelFunc
	events  <-chan build.Event
	done    <-chan error
}

const tailLines = 200

func newRunModel(cfg *config.Config, plan build.Plan, ctx context.Context, cancel context.CancelFunc) *runModel {
	rm := &runModel{
		cfg:     cfg,
		state:   map[string]stepState{},
		started: map[string]time.Time{},
		ended:   map[string]time.Time{},
		logs:    map[string][]string{},
		cancel:  cancel,
	}
	for _, s := range plan.Steps {
		rm.stepIDs = append(rm.stepIDs, s.Name())
		rm.state[s.Name()] = stepPending
	}
	runner := build.NewRunner(plan)
	rm.events = runner.Events
	d := make(chan error, 1)
	go func() { d <- runner.Run(ctx) }()
	rm.done = d
	return rm
}

func (m *runModel) Init() tea.Cmd { return waitEvent(m.events) }

type eventMsg struct {
	e  build.Event
	ok bool
}

func waitEvent(events <-chan build.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-events
		return eventMsg{e: e, ok: ok}
	}
}

func (m *runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case eventMsg:
		if !v.ok {
			return m, tea.Quit
		}
		m.applyEvent(v.e)
		return m, waitEvent(m.events)
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c", "c":
			m.cancel()
		case "q":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *runModel) applyEvent(e build.Event) {
	switch e.Kind {
	case build.EventStarted:
		m.state[e.Step] = stepRunning
		m.started[e.Step] = e.At
		m.active = e.Step
	case build.EventFinished:
		m.state[e.Step] = stepSucceeded
		m.ended[e.Step] = e.At
	case build.EventFailed:
		m.state[e.Step] = stepFailed
		m.ended[e.Step] = e.At
		if err, ok := e.Payload.(error); ok {
			m.err = err
		}
	case build.EventCancelled:
		if m.state[e.Step] == stepRunning {
			m.state[e.Step] = stepCancelled
			m.ended[e.Step] = e.At
		} else {
			m.state[e.Step] = stepCancelled
		}
	case build.EventSkipped:
		m.state[e.Step] = stepSkipped
	case build.EventLogLine:
		if line, ok := e.Payload.(string); ok {
			m.appendLog(e.Step, "["+e.Step+"] "+line)
		}
	case build.EventProgress:
		if p, ok := e.Payload.(build.ProgressPayload); ok {
			m.appendLog(e.Step, "["+e.Step+"] · "+p.Message)
		}
	case build.EventTimingResult:
		if r, ok := e.Payload.(build.TimingResult); ok {
			m.timing = &r
			status := "met"
			if !r.MetConstraints {
				status = "NOT MET"
			}
			m.appendLog(e.Step, fmt.Sprintf("[%s] timing %s WNS=%g TNS=%g WHS=%g", e.Step, status, r.WNS, r.TNS, r.WHS))
		}
	}
}

func (m *runModel) appendLog(step, line string) {
	all := m.logs[step]
	all = append(all, line)
	if len(all) > tailLines {
		all = all[len(all)-tailLines:]
	}
	m.logs[step] = all
}

func (m *runModel) View() string {
	var b strings.Builder
	b.WriteString(headingStyle.Render("builder · running"))
	b.WriteString("\n\n")
	for _, name := range m.stepIDs {
		elapsed := ""
		if start, ok := m.started[name]; ok {
			end, ended := m.ended[name]
			if !ended {
				end = time.Now()
			}
			elapsed = end.Sub(start).Round(time.Second).String()
		}
		b.WriteString(fmt.Sprintf(" %s %-22s %s\n", m.state[name], name, elapsed))
	}
	b.WriteString("\n")
	b.WriteString(headingStyle.Render("log"))
	b.WriteString("\n")
	if m.active != "" {
		for _, line := range m.logs[m.active] {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")).Render("FAILED: " + m.err.Error()))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(hintStyle.Render("[c] cancel  [q] quit"))
	return b.String()
}

func runRunScreenInteractive(cfg *config.Config, sel build.Selections) int {
	plan, err := build.BuildPlan(sel, cfg)
	if err != nil {
		fmt.Println("plan:", err)
		return 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	rm := newRunModel(cfg, plan, ctx, cancel)
	p := tea.NewProgram(rm, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		cancel()
		fmt.Println("tui:", err)
		return 1
	}
	cancel()
	<-rm.done
	if rm.err != nil {
		return 1
	}
	return 0
}
