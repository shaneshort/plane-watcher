package main

import (
	"context"
	"fmt"
	"os"
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

// stepStyles maps state -> (glyph, style). Yellow split-flap palette;
// errors keep their universal red. lipgloss degrades colour gracefully
// for non-truecolor terminals.
var stepStyles = map[stepState]struct {
	glyph string
	style lipgloss.Style
}{
	stepPending:   {"·", styleDim},
	stepRunning:   {"▶", lipgloss.NewStyle().Foreground(clrYellowHi).Bold(true)},
	stepSucceeded: {"✓", styleYellow},
	stepFailed:    {"✗", styleBad},
	stepCancelled: {"⊘", styleGray},
	stepSkipped:   {"-", styleGray},
}

type runModel struct {
	cfg       *config.Config
	stepIDs   []string
	state     map[string]stepState
	started   map[string]time.Time
	ended     map[string]time.Time
	logs      map[string][]string
	active    string
	timing    *build.TimingResult
	err       error
	cancel    context.CancelFunc
	events    <-chan build.Event
	done      <-chan error
	completed bool
	width     int
	height    int
}

const tailLines = 1000

func newRunModel(cfg *config.Config, plan build.Plan, ctx context.Context, cancel context.CancelFunc) *runModel {
	rm := &runModel{
		cfg:     cfg,
		state:   map[string]stepState{},
		started: map[string]time.Time{},
		ended:   map[string]time.Time{},
		logs:    map[string][]string{},
		cancel:  cancel,
		width:   80, // sensible default before the first WindowSizeMsg
		height:  24,
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
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil
	case eventMsg:
		if !v.ok {
			m.completed = true
			return m, nil
		}
		m.applyEvent(v.e)
		return m, waitEvent(m.events)
	case tea.KeyMsg:
		if m.completed {
			return m, tea.Quit
		}
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
			m.appendLog(e.Step, line)
		}
	case build.EventProgress:
		if p, ok := e.Payload.(build.ProgressPayload); ok {
			m.appendLog(e.Step, "· "+p.Message)
		}
	case build.EventTimingResult:
		if r, ok := e.Payload.(build.TimingResult); ok {
			m.timing = &r
			status := "met"
			if !r.MetConstraints {
				status = "NOT MET"
			}
			m.appendLog(e.Step, fmt.Sprintf("timing %s WNS=%g TNS=%g WHS=%g", status, r.WNS, r.TNS, r.WHS))
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

// --- View helpers ---

func (m *runModel) renderStepList() string {
	var b strings.Builder
	for _, name := range m.stepIDs {
		elapsed := ""
		if start, ok := m.started[name]; ok {
			end, ended := m.ended[name]
			if !ended {
				end = time.Now()
			}
			elapsed = end.Sub(start).Round(time.Second).String()
		}
		ss := stepStyles[m.state[name]]
		nameStyle := styleYellow
		if m.state[name] == stepPending {
			nameStyle = styleDim
		}
		line := fmt.Sprintf(" %s %s  %s",
			ss.style.Render(ss.glyph),
			nameStyle.Render(fmt.Sprintf("%-22s", name)),
			styleHint.Render(elapsed),
		)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (m *runModel) renderLog(maxLines, maxWidth int) string {
	if maxLines <= 0 {
		return ""
	}
	tail := m.logs[m.active]
	// Wrap each line to maxWidth, then take the last maxLines of the
	// wrapped result so the visual count matches what fits on screen.
	wrapped := make([]string, 0, len(tail))
	for _, line := range tail {
		for _, w := range wrapLine(line, maxWidth) {
			wrapped = append(wrapped, w)
		}
	}
	if len(wrapped) > maxLines {
		wrapped = wrapped[len(wrapped)-maxLines:]
	}
	return strings.Join(wrapped, "\n")
}

// wrapLine breaks line into chunks of at most width runes. Width <= 0
// disables wrapping. Long lines without spaces are split mid-word.
func wrapLine(line string, width int) []string {
	if width <= 0 || len(line) <= width {
		return []string{line}
	}
	out := []string{}
	runes := []rune(line)
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func (m *runModel) View() string {
	title := styleTitle.Render("builder · running")
	steps := m.renderStepList()

	// Footer (hint line)
	footer := ""
	if m.completed {
		footer = styleHint.Render("run finished — press any key to exit")
	} else {
		footer = styleHint.Render("[c] cancel   [q] quit")
	}

	// Error stripe (if any)
	errStripe := ""
	if m.err != nil {
		errStripe = styleBad.Render("FAILED: "+m.err.Error()) + "\n\n"
	}

	// Reserve rows for: title (1) + blank (1) + steps + blank (1) + "log:" (1) +
	// log border (2 for top/bottom) + log content + blank (1) + errStripe (varies) + footer (1).
	stepsLines := strings.Count(steps, "\n")
	errLines := 0
	if errStripe != "" {
		errLines = strings.Count(errStripe, "\n")
	}
	reserved := 1 + 1 + stepsLines + 1 + 1 + 2 + 1 + errLines + 1
	logLines := m.height - reserved
	if logLines < 3 {
		logLines = 3
	}
	logWidth := m.width - 4 // border + padding
	if logWidth < 20 {
		logWidth = 20
	}

	logBody := m.renderLog(logLines, logWidth)
	logBox := styleBorder.Width(logWidth).Render(logBody)

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(steps)
	b.WriteString("\n")
	b.WriteString(styleHeading.Render("log"))
	b.WriteString("\n")
	b.WriteString(logBox)
	b.WriteString("\n")
	if errStripe != "" {
		b.WriteString(errStripe)
	}
	b.WriteString(footer)
	return b.String()
}

func runRunScreenInteractive(cfg *config.Config, sel build.Selections) int {
	plan, err := build.BuildPlan(sel, cfg)
	if err != nil {
		fmt.Println("plan:", err)
		return 2
	}
	if len(plan.Steps) == 0 {
		fmt.Println("no work selected — tick at least one stage in the composer")
		return 0
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
	runErr := <-rm.done
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "builder:", runErr)
		return 1
	}
	if rm.err != nil {
		fmt.Fprintln(os.Stderr, "builder:", rm.err)
		return 1
	}
	return 0
}
