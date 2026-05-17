// ubx is a CLI for configuring u-blox GNSS receivers via a running gpsd
// instance. See docs/plans/2026-04-10-ubx-gpsd-tool-design.md for the full
// design.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	cbterm "github.com/charmbracelet/x/term"

	"github.com/plane-watcher/plane-feeder/internal/gps"
)

// isTTY reports whether f is an interactive terminal. Used to gate the
// carriage-return progress display in survey-in. (Lipgloss handles
// styling auto-detection per-renderer; this is for the panel choice.)
func isTTY(f *os.File) bool { return cbterm.IsTerminal(f.Fd()) }

// term carries lipgloss styles that respect the colour profile of the
// underlying stream. Styles are pre-built so the helpers below stay
// terse at call sites.
//
// Lipgloss already gates colour codes on terminal capability via
// colorprofile, so styled output passed through `tee` or `>` lands as
// plain text. Callers do not need to check isTTY before formatting.
type term struct {
	renderer    *lipgloss.Renderer
	boldStyle   lipgloss.Style
	dimStyle    lipgloss.Style
	greenStyle  lipgloss.Style
	yellowStyle lipgloss.Style
	redStyle    lipgloss.Style
}

func newTerm(f *os.File) term {
	r := lipgloss.NewRenderer(f)
	return term{
		renderer:    r,
		boldStyle:   r.NewStyle().Bold(true),
		dimStyle:    r.NewStyle().Faint(true),
		greenStyle:  r.NewStyle().Foreground(lipgloss.Color("2")),
		yellowStyle: r.NewStyle().Foreground(lipgloss.Color("3")),
		redStyle:    r.NewStyle().Foreground(lipgloss.Color("1")),
	}
}

func (t term) bold(s string) string   { return t.boldStyle.Render(s) }
func (t term) dim(s string) string    { return t.dimStyle.Render(s) }
func (t term) green(s string) string  { return t.greenStyle.Render(s) }
func (t term) yellow(s string) string { return t.yellowStyle.Render(s) }
func (t term) red(s string) string    { return t.redStyle.Render(s) }

// printTitle writes a top-level heading with a ═ rule beneath it. The
// rule length matches the visible width via lipgloss.Width, so ANSI
// codes don't push the underline out of alignment.
func printTitle(w io.Writer, t term, text string) {
	fmt.Fprintln(w, t.bold(text))
	fmt.Fprintln(w, strings.Repeat("═", lipgloss.Width(text)))
}

// printSection writes a subsection heading with a ─ rule. `note` (e.g.
// "(active)" or "(not used in this mode)") is rendered dim and counted
// toward the rule width.
func printSection(w io.Writer, t term, text, note string) {
	heading := t.bold(text)
	w0 := lipgloss.Width(text)
	if note != "" {
		heading += "  " + t.dim(note)
		w0 += 2 + lipgloss.Width(note)
	}
	fmt.Fprintln(w, heading)
	fmt.Fprintln(w, strings.Repeat("─", w0))
}

// labelValue renders a "<label>  <value>" row, right-padding the label
// to width so columns of rows line up under each other. The label is
// rendered dim; the value passes through verbatim (which may itself
// already contain styled fragments).
func labelValue(w io.Writer, t term, label string, width int, value string) {
	pad := width - lipgloss.Width(label)
	if pad < 1 {
		pad = 1
	}
	fmt.Fprintf(w, "  %s%s%s\n", t.dim(label), strings.Repeat(" ", pad), value)
}

// successf writes a green bold "OK" tag plus a fmt-formatted message.
func successf(w io.Writer, t term, format string, args ...any) {
	fmt.Fprintf(w, "%s  %s\n",
		t.greenStyle.Bold(true).Render("OK"),
		fmt.Sprintf(format, args...))
}

// alignLeft / alignRight pick per-column alignment for boxTable.
const (
	alignLeft  = false
	alignRight = true
)

// Common alignment specs used by the printers below.
var (
	labelLeftValueLeft  = []bool{alignLeft, alignLeft, alignLeft}
	labelLeftValueRight = []bool{alignLeft, alignRight, alignRight}
)

// boxTable renders a bordered Unicode-box table via lipgloss/table. If
// headers is non-nil it becomes a header row; pass nil for borderless
// key/value lists with no header. aligns picks per-column alignment;
// missing entries default to alignLeft. dimCols renders specific
// columns in the dim style — typically a trailing "raw" alternate-
// unit column.
func boxTable(w io.Writer, t term, headers []string, aligns []bool, rows [][]string, dimCols ...int) {
	dim := map[int]bool{}
	for _, d := range dimCols {
		dim[d] = true
	}
	base := t.renderer.NewStyle().Padding(0, 1)
	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(t.dimStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := base
			if row == table.HeaderRow {
				return s.Bold(true)
			}
			if col < len(aligns) && aligns[col] == alignRight {
				s = s.Align(lipgloss.Right)
			}
			if dim[col] {
				s = s.Faint(true)
			}
			return s
		}).
		Rows(rows...)
	if headers != nil {
		tbl = tbl.Headers(headers...)
	}
	fmt.Fprintln(w, tbl.Render())
}

// livePanel renders a fixed-height block of lines and re-renders it in
// place on every update by walking the cursor back up the previous
// block. Designed for the survey-in progress display.
//
// Caveat: must own the cursor from the first render onwards. Anything
// that writes to the same stream between renders (signal handlers,
// other goroutines) will get mangled by the next \033[<N>A jump.
type livePanel struct {
	out   io.Writer
	lines int
}

func (p *livePanel) render(content []string) {
	if p.lines > 0 {
		fmt.Fprintf(p.out, "\033[%dA\033[J", p.lines)
	}
	for _, line := range content {
		fmt.Fprintln(p.out, line)
	}
	p.lines = len(content)
}

const usage = `ubx - configure u-blox GNSS receivers via gpsd

Usage:
  ubx status                                one-shot dump of MON-VER, TMODE, SVIN, NAV5, TP5
  ubx version     [flags]                   poll UBX-MON-VER and print receiver info
  ubx survey-in   [flags]                   run a survey-in to improve PPS timing
  ubx deploy      [flags]                   full deployment pipeline (see below)
  ubx fixed show  [flags]                   print current TMODE mode + coords (if fixed)
  ubx fixed from-svin [--save] [flags]      lock into fixed mode at the converged SVIN position
  ubx fixed set   <ecefX_cm> <ecefY_cm> <ecefZ_cm> [<accMM>] [--save] [flags]
                                            lock into fixed mode at manual ECEF coordinates
  ubx tmode show  [flags]                   print current TMODE2/3 payload as hex
  ubx tmode set   <hex> [flags]             write a raw TMODE2/3 payload (recovery)
  ubx configure-stationary [--save] [flags] apply CFG-NAV5 stationary dynamic model
  ubx configure-pps [--save] [flags]        apply CFG-TP5 1 Hz 50% UTC-aligned PPS
  ubx save        [flags]                   save all configured sections to BBR+Flash

Common flags:
  --gpsd ADDR        gpsd address (default localhost:2947)
  --device PATH      device path; required when gpsd reports >1 device
  --debug            log UBX frames and gpsd JSON envelopes to stderr

survey-in flags:
  --min-duration SEC minimum survey-in duration in seconds (default 300)
  --accuracy M       required accuracy in metres (default 2.0)
  --poll-interval D  status poll interval (default 2s)
  --ephemeral        restore prior TMODE on success too (testing)

deploy flags:
  --min-duration SEC   minimum survey-in duration (default 300)
  --accuracy M         required accuracy in metres (default 2.0)
  --no-save            do NOT save to flash after deployment (default is to save)
  --skip-if-fixed      if the receiver is already in fixed mode, only apply NAV5/TP5/save
`

// exitError carries a specific exit code alongside an error so the command
// handlers can request a non-1 exit without calling os.Exit from deep
// inside themselves. main unwraps it and exits with the requested code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func withExit(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitError{code: code, err: err}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "survey-in":
		err = runSurveyIn(args)
	case "version":
		err = runVersion(args)
	case "status":
		err = runStatus(args)
	case "save":
		err = runSave(args)
	case "configure-stationary":
		err = runConfigureStationary(args)
	case "configure-pps":
		err = runConfigurePPS(args)
	case "deploy":
		err = runDeploy(args)
	case "fixed":
		if len(args) < 1 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch args[0] {
		case "show":
			err = runFixedShow(args[1:])
		case "from-svin":
			err = runFixedFromSVIN(args[1:])
		case "set":
			err = runFixedSet(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "unknown fixed subcommand: %q\n%s", args[0], usage)
			os.Exit(2)
		}
	case "tmode":
		if len(args) < 1 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch args[0] {
		case "show":
			err = runTmodeShow(args[1:])
		case "set":
			err = runTmodeSet(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "unknown tmode subcommand: %q\n%s", args[0], usage)
			os.Exit(2)
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}

// commonFlags adds the gpsd/device/debug flags to a FlagSet and returns
// pointers to the parsed values.
type commonOpts struct {
	gpsdAddr *string
	device   *string
	debug    *bool
}

func addCommonFlags(fs *flag.FlagSet) commonOpts {
	return commonOpts{
		gpsdAddr: fs.String("gpsd", "localhost:2947", "gpsd address"),
		device:   fs.String("device", "", "device path (required when gpsd reports more than one device)"),
		debug:    fs.Bool("debug", false, "log UBX frames and gpsd JSON envelopes to stderr"),
	}
}

func openClientAndDevice(ctx context.Context, opts commonOpts) (*gps.Client, *gps.DeviceClient, error) {
	c, err := gps.Dial(ctx, *opts.gpsdAddr)
	if err != nil {
		return nil, nil, err
	}
	if *opts.debug {
		c.SetDebug(os.Stderr)
	}

	devices := c.Devices()
	if len(devices) == 0 {
		c.Close()
		return nil, nil, errors.New("gpsd reports no devices — is the receiver connected and recognised?")
	}

	var path string
	if *opts.device != "" {
		path = *opts.device
		found := false
		for _, d := range devices {
			if d.Path == path {
				found = true
				break
			}
		}
		if !found {
			c.Close()
			return nil, nil, fmt.Errorf("--device %q not in gpsd device list:\n%s", path, formatDeviceList(devices))
		}
	} else {
		// Auto-pick when there is exactly one device, regardless of
		// gpsd's driver classification. gpsd labels u-blox receivers
		// as "NMEA0183" until it sees a UBX frame on the line, so
		// filtering by driver="u-blox" is a gpsd probing quirk we
		// don't want to inherit. Multiple devices still require
		// --device — that's the ambiguity case the earlier review
		// called out.
		if len(devices) > 1 {
			c.Close()
			return nil, nil, fmt.Errorf("multiple devices reported by gpsd; pass --device to choose:\n%s", formatDeviceList(devices))
		}
		path = devices[0].Path
		if !isUbloxDriver(devices[0].Driver) {
			// Not blocking, just informational — warn so the
			// operator knows we're talking to whatever gpsd
			// happens to have on that port.
			fmt.Fprintf(os.Stderr, "[ubx] note: gpsd reports driver=%q for %s; proceeding assuming u-blox.\n",
				devices[0].Driver, path)
		}
	}

	dc := c.Subscribe(path)
	return c, dc, nil
}

func formatDeviceList(devs []gps.DeviceInfo) string {
	var b strings.Builder
	for _, d := range devs {
		fmt.Fprintf(&b, "  %s  (driver: %s)\n", d.Path, d.Driver)
	}
	return b.String()
}

func isUbloxDriver(driver string) bool {
	d := strings.ToLower(driver)
	return strings.Contains(d, "u-blox") || strings.Contains(d, "ublox")
}

// ----- survey-in subcommand -----

func runSurveyIn(args []string) error {
	fs := flag.NewFlagSet("survey-in", flag.ContinueOnError)
	common := addCommonFlags(fs)
	minDur := fs.Uint("min-duration", 300, "minimum survey-in duration in seconds")
	accM := fs.Float64("accuracy", 2.0, "required survey-in accuracy in metres")
	pollInt := fs.Duration("poll-interval", 2*time.Second, "status poll interval")
	maxDur := fs.Duration("max-duration", 0, "overall survey deadline; if zero, derived as max(3×min-duration, min-duration+60s)")
	ephemeral := fs.Bool("ephemeral", false, "restore prior TMODE on success (testing only)")
	asJSON := fs.Bool("json", false, "force JSON Lines progress on stdout (default: JSON only when stderr is not a TTY)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// interrupted is set by the signal handler so the error-path code
	// below can distinguish SIGINT (exit 130) from other cancellation
	// causes.
	var interrupted atomic.Bool
	go func() {
		<-sigCh
		interrupted.Store(true)
		fmt.Fprintln(os.Stderr, "\n[ubx] interrupt received — restoring prior TMODE...")
		cancel()
	}()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, mv, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return fmt.Errorf("detect generation: %w", err)
	}
	if gen == gps.GenUnknown {
		return fmt.Errorf("unknown receiver generation (hwVersion=%q); run `ubx version` for triage", mv.HwVersion)
	}
	// TTY (default): render a multi-line live status panel on stderr so
	// the operator can watch convergence at a glance.
	// Non-TTY (or --json): emit JSON Lines per poll on stdout (stderr
	// keeps the human-readable preamble + outcome lines) so a
	// downstream `jq` pipeline gets structured data without the cursor
	// games.
	useLivePanel := isTTY(os.Stderr) && !*asJSON
	t := newTerm(os.Stderr)
	var progressEmitted bool

	if useLivePanel {
		fmt.Fprintln(os.Stderr)
		printTitle(os.Stderr, t, "GNSS Survey-In")
		labelValue(os.Stderr, t, "Receiver", 14, fmt.Sprintf("%s  %s", gen, t.dim("(hwVersion "+mv.HwVersion+")")))
		labelValue(os.Stderr, t, "Target", 14, fmt.Sprintf("≥ %d s, accuracy ≤ %.3f m", *minDur, *accM))
		fmt.Fprintln(os.Stderr)
	} else {
		fmt.Fprintf(os.Stderr, "[ubx] receiver: %s (%s)\n", gen, mv.HwVersion)
	}

	panel := &livePanel{out: os.Stderr}
	jsonEnc := json.NewEncoder(os.Stdout)
	reporter := func(elapsed time.Duration, st gps.SVINStatus) {
		progressEmitted = true
		status := surveyStatus(st, uint32(*minDur), *accM)
		if !useLivePanel {
			_ = jsonEnc.Encode(map[string]any{
				"event":      "poll",
				"elapsed_s":  int(elapsed.Seconds()),
				"obs":        st.Observations,
				"dur_s":      st.DurationSec,
				"mean_acc_m": st.MeanAccMeters,
				"valid":      st.Valid,
				"active":     st.Active,
				"status":     status,
			})
			return
		}
		panel.render(surveyPanelLines(t, st, uint32(*minDur), *accM, status))
	}

	res, err := gps.SurveyIn(ctx, dc, gen, gps.SurveyInOptions{
		MinDurationSec: uint32(*minDur),
		AccuracyMeters: *accM,
		PollInterval:   *pollInt,
		MaxDuration:    *maxDur,
		Ephemeral:      *ephemeral,
	}, reporter, os.Stderr)
	// In TTY mode the panel was the last thing drawn; emit a blank line
	// to put outcome / rollback chatter on a fresh row. Done inline (not
	// via defer) so the gap lands before the rollback messages from
	// gps.SurveyIn.
	if useLivePanel && progressEmitted {
		fmt.Fprintln(os.Stderr)
	}

	// printFinalSnapshot dumps the last SVIN status we saw — used on
	// SIGINT and timeout so the operator always gets the most recent
	// number before the process exits.
	printFinalSnapshot := func() {
		if res.Status.DurationSec == 0 && res.Status.Observations == 0 {
			return
		}
		fmt.Fprintln(os.Stderr, t.dim("Final survey-in snapshot:"))
		fmt.Fprintf(os.Stderr, "  duration=%ds  observations=%d  meanAcc=%.3fm  valid=%t  active=%t\n",
			res.Status.DurationSec, res.Status.Observations, res.Status.MeanAccMeters,
			res.Status.Valid, res.Status.Active)
	}

	// printRecoveryHex emits the cached pre-survey TMODE payload so the
	// operator can rewind manually if anything is unclear. Only called
	// on non-success paths to keep the interactive output clean when
	// nothing went wrong.
	printRecoveryHex := func() {
		if len(res.CachedTMODE.Raw) == 0 {
			return
		}
		hexBytes := hex.EncodeToString(res.CachedTMODE.Raw)
		fmt.Fprintln(os.Stderr, t.dim("Pre-survey TMODE was cached. To restore manually:"))
		fmt.Fprintf(os.Stderr, "  ubx tmode set %s\n", hexBytes)
	}

	switch {
	case res.RollbackFailed:
		// Receiver may be in modified state; surface recovery hint.
		printFinalSnapshot()
		fmt.Fprintln(os.Stderr, t.red(t.bold("WARNING:"))+" rollback FAILED — the receiver may be in a modified state.")
		printRecoveryHex()
		return withExit(1, err)

	case interrupted.Load():
		printFinalSnapshot()
		if res.RolledBack {
			fmt.Fprintln(os.Stderr, t.dim("Prior TMODE restored after interrupt."))
		}
		printRecoveryHex()
		return withExit(130, fmt.Errorf("survey-in interrupted by signal"))

	case errors.Is(err, gps.ErrSurveyInTimeout):
		printFinalSnapshot()
		if res.RolledBack {
			fmt.Fprintln(os.Stderr, t.dim("Prior TMODE restored after timeout."))
		}
		printRecoveryHex()
		return withExit(2, err)

	case err != nil:
		if res.RolledBack {
			fmt.Fprintln(os.Stderr, t.dim("Prior TMODE restored after failure."))
		}
		printRecoveryHex()
		return withExit(1, err)

	case res.RolledBack:
		successf(os.Stderr, t, "Survey-in run complete; prior TMODE restored %s", t.dim("(--ephemeral)"))
	default:
		successf(os.Stderr, t, "New TMODE config persisted on the receiver")
	}
	return nil
}

// ----- version subcommand -----

func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	common := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, mv, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return err
	}
	t := newTerm(os.Stdout)
	printTitle(os.Stdout, t, "Receiver Version")
	labelValue(os.Stdout, t, "Generation", 14, t.bold(gen.String()))
	labelValue(os.Stdout, t, "Software", 14, mv.SwVersion)
	labelValue(os.Stdout, t, "Hardware", 14, mv.HwVersion)
	for _, e := range mv.Extensions {
		labelValue(os.Stdout, t, "Extension", 14, e)
	}
	return nil
}

// ----- tmode show / set subcommands -----

func runTmodeShow(args []string) error {
	fs := flag.NewFlagSet("tmode show", flag.ContinueOnError)
	common := addCommonFlags(fs)
	asJSON := fs.Bool("json", false, "emit decoded TMODE as a JSON object instead of human-readable text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, _, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return err
	}
	if gen == gps.GenUnknown {
		return errors.New("unknown receiver generation; tmode show needs a known generation")
	}
	pl, err := gps.PollTMODE(ctx, dc, gen)
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(os.Stdout, buildTMODEJSON(gen, pl.Raw))
	}
	return printTMODEHuman(os.Stdout, newTerm(os.Stdout), gen, pl.Raw)
}

// printTMODEHuman writes a decoded CFG-TMODE2 / CFG-TMODE3 payload as
// labelled sections. The active mode's field group is marked
// "(active)"; the other group is marked "(not used in this mode)" so
// operators can see at a glance which numbers the receiver is actually
// applying.
func printTMODEHuman(w io.Writer, t term, gen gps.Generation, raw []byte) error {
	mode := gps.TMODEMode(gen, raw)
	modeName := map[int]string{0: "disabled", 1: "survey-in", 2: "fixed"}[mode]
	if modeName == "" {
		modeName = "unknown"
	}
	tmodeNum := 3
	if gen == gps.GenM8 {
		tmodeNum = 2
	}

	printTitle(w, t, "Time-Mode Configuration")
	labelValue(w, t, "Receiver", 16, fmt.Sprintf("%s  %s", gen,
		t.dim(fmt.Sprintf("(CFG-TMODE%d, %d bytes)", tmodeNum, len(raw)))))
	labelValue(w, t, "Current mode", 16, fmt.Sprintf("%s  %s",
		t.bold(modeName), t.dim(fmt.Sprintf("(timeMode=%d)", mode))))

	switch gen {
	case gps.GenM8:
		if len(raw) < 28 {
			return fmt.Errorf("TMODE2 payload too short: %d", len(raw))
		}
		printTMODE2Human(w, t, mode, raw)
	case gps.GenF9:
		if len(raw) < 40 {
			return fmt.Errorf("TMODE3 payload too short: %d", len(raw))
		}
		printTMODE3Human(w, t, mode, raw)
	}

	fmt.Fprintln(w)
	printSection(w, t, "Raw", "")
	labelValue(w, t, "Payload", 18, hex.EncodeToString(raw))
	labelValue(w, t, "Restore command", 18, t.dim("ubx tmode set ")+hex.EncodeToString(raw))
	return nil
}

func printTMODE2Human(w io.Writer, t term, mode int, raw []byte) {
	flags := binary.LittleEndian.Uint16(raw[2:4])
	labelValue(w, t, "Flags", 16, fmt.Sprintf("0x%04x", flags))

	ecefX := int32(binary.LittleEndian.Uint32(raw[4:8]))
	ecefY := int32(binary.LittleEndian.Uint32(raw[8:12]))
	ecefZ := int32(binary.LittleEndian.Uint32(raw[12:16]))
	fixedAcc := binary.LittleEndian.Uint32(raw[16:20])
	svinDur := binary.LittleEndian.Uint32(raw[20:24])
	svinAcc := binary.LittleEndian.Uint32(raw[24:28])

	fmt.Fprintln(w)
	printSection(w, t, "Fixed-mode position", activeNote(mode == 2))
	boxTable(w, t, nil, labelLeftValueRight, [][]string{
		{"X", formatMetres(ecefX), formatCm(ecefX)},
		{"Y", formatMetres(ecefY), formatCm(ecefY)},
		{"Z", formatMetres(ecefZ), formatCm(ecefZ)},
		{"Position accuracy",
			fmt.Sprintf("%.3f m", float64(fixedAcc)/1000.0),
			fmt.Sprintf("(%s mm)", thousands(int64(fixedAcc)))},
	}, 2)

	fmt.Fprintln(w)
	printSection(w, t, "Survey-in thresholds", activeNote(mode == 1))
	boxTable(w, t, nil, labelLeftValueLeft, [][]string{
		{"Minimum duration", fmt.Sprintf("%s s", thousands(int64(svinDur)))},
		{"Accuracy limit",
			fmt.Sprintf("%.3f m", float64(svinAcc)/1000.0),
			fmt.Sprintf("(%s mm)", thousands(int64(svinAcc)))},
	}, 2)
}

func printTMODE3Human(w io.Writer, t term, mode int, raw []byte) {
	flags := binary.LittleEndian.Uint16(raw[2:4])
	lla := flags&0x0100 != 0
	coordKind := "ECEF (cm + 0.1 mm HP)"
	if lla {
		coordKind = "LLA (deg×1e-7 + cm)"
	}
	labelValue(w, t, "Flags", 16,
		fmt.Sprintf("0x%04x  %s", flags, t.dim("(coords as "+coordKind+")")))

	ecefX := int32(binary.LittleEndian.Uint32(raw[4:8]))
	ecefY := int32(binary.LittleEndian.Uint32(raw[8:12]))
	ecefZ := int32(binary.LittleEndian.Uint32(raw[12:16]))
	hpX := int8(raw[16])
	hpY := int8(raw[17])
	hpZ := int8(raw[18])
	fixedAcc := binary.LittleEndian.Uint32(raw[20:24]) // 0.1 mm
	svinDur := binary.LittleEndian.Uint32(raw[24:28])
	svinAcc := binary.LittleEndian.Uint32(raw[28:32]) // 0.1 mm

	fmt.Fprintln(w)
	printSection(w, t, "Fixed-mode position", activeNote(mode == 2))
	boxTable(w, t, nil, labelLeftValueRight, [][]string{
		{"X", formatMetres(ecefX), formatCmHP(ecefX, hpX)},
		{"Y", formatMetres(ecefY), formatCmHP(ecefY, hpY)},
		{"Z", formatMetres(ecefZ), formatCmHP(ecefZ, hpZ)},
		{"Position accuracy",
			fmt.Sprintf("%.4f m", float64(fixedAcc)/10000.0),
			fmt.Sprintf("(%s × 0.1 mm)", thousands(int64(fixedAcc)))},
	}, 2)

	fmt.Fprintln(w)
	printSection(w, t, "Survey-in thresholds", activeNote(mode == 1))
	boxTable(w, t, nil, labelLeftValueLeft, [][]string{
		{"Minimum duration", fmt.Sprintf("%s s", thousands(int64(svinDur)))},
		{"Accuracy limit",
			fmt.Sprintf("%.4f m", float64(svinAcc)/10000.0),
			fmt.Sprintf("(%s × 0.1 mm)", thousands(int64(svinAcc)))},
	}, 2)
}

// buildTMODEJSON returns a JSON-friendly view of a decoded CFG-TMODE
// payload. Numeric fields are exposed in both raw native units (cm,
// mm or 0.1 mm) and metres for downstream convenience.
func buildTMODEJSON(gen gps.Generation, raw []byte) map[string]any {
	mode := gps.TMODEMode(gen, raw)
	modeName := map[int]string{0: "disabled", 1: "survey-in", 2: "fixed"}[mode]
	if modeName == "" {
		modeName = "unknown"
	}
	out := map[string]any{
		"generation":     gen.String(),
		"time_mode":      mode,
		"time_mode_name": modeName,
		"payload_bytes":  len(raw),
		"payload_hex":    hex.EncodeToString(raw),
	}
	switch gen {
	case gps.GenM8:
		if len(raw) < 28 {
			return out
		}
		flags := binary.LittleEndian.Uint16(raw[2:4])
		ecefX := int32(binary.LittleEndian.Uint32(raw[4:8]))
		ecefY := int32(binary.LittleEndian.Uint32(raw[8:12]))
		ecefZ := int32(binary.LittleEndian.Uint32(raw[12:16]))
		fixedAcc := binary.LittleEndian.Uint32(raw[16:20])
		svinDur := binary.LittleEndian.Uint32(raw[20:24])
		svinAcc := binary.LittleEndian.Uint32(raw[24:28])
		out["tmode_msg"] = "CFG-TMODE2"
		out["flags"] = fmt.Sprintf("0x%04x", flags)
		out["fixed_position"] = map[string]any{
			"active":      mode == 2,
			"ecef_x_cm":   ecefX,
			"ecef_y_cm":   ecefY,
			"ecef_z_cm":   ecefZ,
			"ecef_x_m":    float64(ecefX) / 100.0,
			"ecef_y_m":    float64(ecefY) / 100.0,
			"ecef_z_m":    float64(ecefZ) / 100.0,
			"accuracy_mm": fixedAcc,
			"accuracy_m":  float64(fixedAcc) / 1000.0,
		}
		out["survey_in_thresholds"] = map[string]any{
			"active":            mode == 1,
			"min_duration_s":    svinDur,
			"accuracy_limit_mm": svinAcc,
			"accuracy_limit_m":  float64(svinAcc) / 1000.0,
		}
	case gps.GenF9:
		if len(raw) < 40 {
			return out
		}
		flags := binary.LittleEndian.Uint16(raw[2:4])
		ecefX := int32(binary.LittleEndian.Uint32(raw[4:8]))
		ecefY := int32(binary.LittleEndian.Uint32(raw[8:12]))
		ecefZ := int32(binary.LittleEndian.Uint32(raw[12:16]))
		hpX := int8(raw[16])
		hpY := int8(raw[17])
		hpZ := int8(raw[18])
		fixedAcc := binary.LittleEndian.Uint32(raw[20:24])
		svinDur := binary.LittleEndian.Uint32(raw[24:28])
		svinAcc := binary.LittleEndian.Uint32(raw[28:32])
		out["tmode_msg"] = "CFG-TMODE3"
		out["flags"] = fmt.Sprintf("0x%04x", flags)
		out["fixed_position"] = map[string]any{
			"active":          mode == 2,
			"ecef_x_cm":       ecefX,
			"ecef_y_cm":       ecefY,
			"ecef_z_cm":       ecefZ,
			"ecef_x_hp_0p1mm": hpX,
			"ecef_y_hp_0p1mm": hpY,
			"ecef_z_hp_0p1mm": hpZ,
			"ecef_x_m":        float64(ecefX)/100.0 + float64(hpX)*0.0001,
			"ecef_y_m":        float64(ecefY)/100.0 + float64(hpY)*0.0001,
			"ecef_z_m":        float64(ecefZ)/100.0 + float64(hpZ)*0.0001,
			"accuracy_0p1mm":  fixedAcc,
			"accuracy_m":      float64(fixedAcc) / 10000.0,
		}
		out["survey_in_thresholds"] = map[string]any{
			"active":               mode == 1,
			"min_duration_s":       svinDur,
			"accuracy_limit_0p1mm": svinAcc,
			"accuracy_limit_m":     float64(svinAcc) / 10000.0,
		}
	}
	return out
}

func activeNote(active bool) string {
	if active {
		return "(active)"
	}
	return "(not used in this mode)"
}

// formatMetres renders an ECEF coordinate in metres with a comma
// thousand separator. Caller-passed cm is converted to int64 first so
// the sign is preserved when the whole-metres part rounds to zero
// (e.g. -1 cm → "-0.01 m", not "0.01 m").
func formatMetres(cm int32) string {
	n := int64(cm)
	neg := n < 0
	if neg {
		n = -n
	}
	whole := n / 100
	frac := n % 100
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%02d m", sign, thousands(whole), frac)
}

// formatCm renders the same coordinate as raw centimetres for the dim
// annotation column.
func formatCm(cm int32) string {
	return fmt.Sprintf("(%s cm)", thousands(int64(cm)))
}

// formatCmHP adds the TMODE3 sub-cm high-precision part.
func formatCmHP(cm int32, hp int8) string {
	return fmt.Sprintf("(%s cm + %+d × 0.1 mm)", thousands(int64(cm)), hp)
}

// thousands formats an integer with comma thousand separators.
func thousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if len(s) > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// emitJSON marshals v as indented JSON to w and appends a trailing
// newline. Used for the --json output paths of tmode show / status, so
// the artefact is both human-skimmable and `jq`-friendly.
func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// surveyStatus picks a status keyword for the current SVIN snapshot.
// "converged" once the receiver reports valid && !active. "waiting" once
// the duration threshold is met but the accuracy threshold isn't (so
// the operator knows why the bar has stalled at 100%). "surveying"
// otherwise.
func surveyStatus(st gps.SVINStatus, minDurSec uint32, targetAccM float64) string {
	if st.Valid && !st.Active {
		return "converged"
	}
	if st.DurationSec >= minDurSec && st.MeanAccMeters > targetAccM {
		return "waiting"
	}
	return "surveying"
}

// progressBar renders a fixed-width bar showing how far DurationSec has
// progressed towards minDurSec (capped at 100%). The receiver gates
// convergence on time AND accuracy, so a full bar does not by itself
// mean done — surveyStatus covers that.
func progressBar(durSec, minDurSec uint32, width int) string {
	frac := 0.0
	if minDurSec > 0 {
		frac = float64(durSec) / float64(minDurSec)
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	return "▕" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "▏"
}

// surveyPanelLines builds the lines for the live survey-in status
// panel. Caller passes the dimensioned status, target thresholds, and
// the pre-computed status keyword from surveyStatus.
func surveyPanelLines(t term, st gps.SVINStatus, minDurSec uint32, targetAccM float64, status string) []string {
	statusDisplay := t.yellow(t.bold("Surveying…"))
	switch status {
	case "converged":
		statusDisplay = t.green(t.bold("Converged ✓"))
	case "waiting":
		statusDisplay = t.yellow(t.bold("Waiting for accuracy"))
	}

	bar := progressBar(st.DurationSec, minDurSec, 28)
	pct := 0
	if minDurSec > 0 {
		pct = int(100 * float64(st.DurationSec) / float64(minDurSec))
		if pct > 100 {
			pct = 100
		}
	}

	accNote := fmt.Sprintf("target ≤ %.3f m", targetAccM)
	accMark := ""
	if st.MeanAccMeters <= targetAccM {
		accMark = "  " + t.green("✓")
	}

	const w = 16
	return []string{
		t.bold("GNSS Survey-In  ") + t.dim("(live)"),
		strings.Repeat("─", 22),
		fmt.Sprintf("  %s%s%s", t.dim("Status"), strings.Repeat(" ", w-len("Status")), statusDisplay),
		fmt.Sprintf("  %s%s%s  %ds of %ds  %s",
			t.dim("Progress"), strings.Repeat(" ", w-len("Progress")),
			bar, st.DurationSec, minDurSec, t.dim(fmt.Sprintf("(%d%%)", pct))),
		fmt.Sprintf("  %s%s%s",
			t.dim("Observations"), strings.Repeat(" ", w-len("Observations")),
			thousands(int64(st.Observations))),
		fmt.Sprintf("  %s%s%.3f m%s   %s",
			t.dim("Mean accuracy"), strings.Repeat(" ", w-len("Mean accuracy")),
			st.MeanAccMeters, accMark, t.dim("("+accNote+")")),
	}
}

func runTmodeSet(args []string) error {
	if len(args) < 1 {
		return errors.New("tmode set requires a hex payload argument")
	}
	hexArg := args[0]
	args = args[1:]

	fs := flag.NewFlagSet("tmode set", flag.ContinueOnError)
	common := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	rawBytes, err := hex.DecodeString(hexArg)
	if err != nil {
		return fmt.Errorf("invalid hex payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, _, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return err
	}
	if gen == gps.GenUnknown {
		return errors.New("unknown receiver generation; tmode set needs a known generation")
	}

	// Validate length matches the generation.
	switch gen {
	case gps.GenM8:
		if len(rawBytes) != 28 {
			return fmt.Errorf("M8 TMODE2 payload must be 28 bytes, got %d", len(rawBytes))
		}
	case gps.GenF9:
		if len(rawBytes) != 40 {
			return fmt.Errorf("F9 TMODE3 payload must be 40 bytes, got %d", len(rawBytes))
		}
	}

	frame, err := gps.RestoreFrame(gps.TMODEPayload{Generation: gen, Raw: rawBytes})
	if err != nil {
		return err
	}
	// Use WriteTMODE so the manual recovery path is also stale-ACK-safe:
	// after the ACK we poll back and verify the receiver actually applied
	// the payload before reporting OK.
	if err := gps.WriteTMODE(ctx, dc, gen, frame, rawBytes); err != nil {
		return fmt.Errorf("tmode set: %w (payload: %s)", err, hexArg)
	}
	successf(os.Stdout, newTerm(os.Stdout), "TMODE payload applied and verified")
	return nil
}

// ----- status subcommand -----

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	common := addCommonFlags(fs)
	asJSON := fs.Bool("json", false, "emit the full status report as a JSON object instead of human-readable text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	rpt, err := gps.GatherStatus(ctx, dc)
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(os.Stdout, buildStatusJSON(rpt))
	}
	printStatusHuman(os.Stdout, newTerm(os.Stdout), rpt)
	return nil
}

// printStatusHuman renders the full status report in the same visual
// style as `ubx tmode show` — title + sectioned tables with consistent
// label widths and right-aligned values.
func printStatusHuman(w io.Writer, t term, r gps.StatusReport) {
	printTitle(w, t, "Receiver Status")

	fmt.Fprintln(w)
	printSection(w, t, "Receiver", "")
	if r.MonVerErr != nil {
		fmt.Fprintf(w, "  %s %v\n", t.red(t.bold("ERROR")), r.MonVerErr)
	} else {
		// Time mode lives in the Receiver block — it's part of the
		// receiver's configured operating state, so colocating with
		// generation/SW/HW gives operators the full identity-plus-state
		// picture in one box instead of a separate one-row floater.
		var tmodeCell string
		switch {
		case r.TMODEErr != nil:
			tmodeCell = t.red(fmt.Sprintf("ERROR: %v", r.TMODEErr))
		default:
			modeName := map[int]string{0: "disabled", 1: "survey-in", 2: "fixed"}[r.TMODEMode]
			if modeName == "" {
				modeName = "unknown"
			}
			tmodeCell = fmt.Sprintf("%s  %s",
				t.bold(modeName),
				t.dim(fmt.Sprintf("(timeMode=%d)", r.TMODEMode)))
		}
		rows := [][]string{
			{"Generation", t.bold(r.Generation.String())},
			{"Software", r.MonVer.SwVersion},
			{"Hardware", r.MonVer.HwVersion},
			{"Time mode", tmodeCell},
		}
		for i, e := range r.MonVer.Extensions {
			label := ""
			if i == 0 {
				label = "Extensions"
			}
			rows = append(rows, []string{label, e})
		}
		boxTable(w, t, nil, labelLeftValueLeft, rows)
	}

	// Only render the Survey-In section when the receiver is actually
	// in survey-in mode. In fixed / disabled mode the SVIN poll returns
	// zeros and "idle" which is misleading rather than informative.
	if r.TMODEMode == 1 {
		fmt.Fprintln(w)
		printSection(w, t, "Survey-In", "")
		if r.SVINErr != nil {
			fmt.Fprintf(w, "  %s %v\n", t.red(t.bold("ERROR")), r.SVINErr)
		} else {
			var statusWord string
			switch {
			case r.SVIN.Valid && !r.SVIN.Active:
				statusWord = t.green(t.bold("converged"))
			case r.SVIN.Active:
				statusWord = t.yellow("active")
			default:
				statusWord = t.dim("idle")
			}
			boxTable(w, t, nil, labelLeftValueLeft, [][]string{
				{"Status", statusWord},
				{"Duration", fmt.Sprintf("%s s", thousands(int64(r.SVIN.DurationSec)))},
				{"Observations", thousands(int64(r.SVIN.Observations))},
				{"Mean accuracy", fmt.Sprintf("%.3f m", r.SVIN.MeanAccMeters)},
				{"Mean ECEF",
					fmt.Sprintf("(%s, %s, %s) cm",
						thousands(int64(r.SVIN.MeanXCm)),
						thousands(int64(r.SVIN.MeanYCm)),
						thousands(int64(r.SVIN.MeanZCm)))},
			})
		}
	}

	fmt.Fprintln(w)
	printSection(w, t, "Navigation Engine", "(CFG-NAV5)")
	if r.NAV5Err != nil {
		fmt.Fprintf(w, "  %s %v\n", t.red(t.bold("ERROR")), r.NAV5Err)
	} else {
		// dim annotation inlined into the value cell so the table stays
		// two-column instead of stranding an empty third column.
		boxTable(w, t, nil, labelLeftValueLeft, [][]string{
			{"Dynamic model", fmt.Sprintf("%s  %s",
				gps.DynModelName(r.NAV5.DynModel),
				t.dim(fmt.Sprintf("(%d)", r.NAV5.DynModel)))},
			{"Fix mode", fmt.Sprintf("%d", r.NAV5.FixMode)},
			{"Min elevation", fmt.Sprintf("%d°", r.NAV5.MinElev)},
			{"UTC standard", fmt.Sprintf("%d", r.NAV5.UtcStandard)},
		})
	}

	fmt.Fprintln(w)
	printSection(w, t, "Timing", "(NAV-CLOCK)")
	if r.ClockErr != nil {
		fmt.Fprintf(w, "  %s %v\n", t.red(t.bold("ERROR")), r.ClockErr)
	} else {
		boxTable(w, t, nil, labelLeftValueLeft, [][]string{
			{"Time accuracy", formatNs(r.Clock.TimeAccuracyNs)},
			{"Frequency accuracy", fmt.Sprintf("%s ps/s", thousands(int64(r.Clock.FreqAccuracyPsPerS)))},
			{"Clock bias", formatSignedNs(r.Clock.ClockBiasNs)},
			{"Clock drift", fmt.Sprintf("%s ns/s", thousands(int64(r.Clock.ClockDriftNsPerS)))},
		})
	}

	fmt.Fprintln(w)
	printSection(w, t, "Time Pulse", "(CFG-TP5, tpIdx=0)")
	if r.TP5Err != nil {
		fmt.Fprintf(w, "  %s %v\n", t.red(t.bold("ERROR")), r.TP5Err)
	} else {
		freqRow := []string{"Frequency", fmt.Sprintf("%s Hz", thousands(int64(r.TP5.FreqPeriod)))}
		if !r.TP5.IsFreq {
			freqRow = []string{"Period", fmt.Sprintf("%s µs", thousands(int64(r.TP5.FreqPeriod)))}
		}
		pulseRow := []string{"Pulse length", fmt.Sprintf("%s µs", thousands(int64(r.TP5.PulseLenRatio)))}
		if !r.TP5.IsLength {
			pulseRow = []string{"Pulse ratio", fmt.Sprintf("%s × 2⁻³² duty", thousands(int64(r.TP5.PulseLenRatio)))}
		}
		grid := "GPS"
		if r.TP5.GridUTC {
			grid = "UTC"
		}
		polarity := "falling edge at top of second"
		if r.TP5.RisingAtTop {
			polarity = "rising edge at top of second"
		}
		boxTable(w, t, nil, labelLeftValueLeft, [][]string{
			{"Active", yesNo(t, r.TP5.Active)},
			{"Locked to GNSS", yesNo(t, r.TP5.LockGpsFreq)},
			freqRow,
			pulseRow,
			{"Align to TOW", yesNo(t, r.TP5.AlignToTow)},
			{"Polarity", polarity},
			{"Grid", grid},
		})
	}

	fmt.Fprintln(w)
	printSection(w, t, "Satellites", "(gpsd SKY)")
	if r.SKYErr != nil {
		fmt.Fprintf(w, "  %s %v\n", t.red(t.bold("ERROR")), r.SKYErr)
	} else {
		printSatellites(w, t, r.SKY)
	}
}

// printSatellites renders the DOP summary plus the per-satellite list
// as two bordered tables: a short stats table and a wider per-SV grid
// sorted by signal strength descending so the strongest contributors
// appear first.
func printSatellites(w io.Writer, t term, sky *gps.SKY) {
	boxTable(w, t, nil, labelLeftValueLeft, [][]string{
		{"Used in fix", fmt.Sprintf("%d of %d seen", sky.USat, sky.NSat)},
		{"HDOP", fmt.Sprintf("%.2f", sky.HDOP)},
		{"VDOP", fmt.Sprintf("%.2f", sky.VDOP)},
		{"PDOP", fmt.Sprintf("%.2f", sky.PDOP)},
	})

	if len(sky.Satellites) == 0 {
		fmt.Fprintln(w, "  "+t.dim("(no satellite detail in the SKY report)"))
		return
	}
	// Sort by SNR descending — the most useful satellites first. Stable
	// ordering on ties (lipgloss table doesn't reorder rows).
	sats := append([]gps.Satellite(nil), sky.Satellites...)
	sort.SliceStable(sats, func(i, j int) bool { return sats[i].SS > sats[j].SS })

	rows := make([][]string, 0, len(sats))
	for _, s := range sats {
		used := t.dim("·")
		if s.Used {
			used = t.green("✓")
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", s.PRN),
			gps.GNSSName(s.GnssID),
			fmt.Sprintf("%.0f°", s.Az),
			fmt.Sprintf("%.0f°", s.El),
			fmt.Sprintf("%.0f", s.SS),
			used,
		})
	}
	headers := []string{"PRN", "GNSS", "Az", "El", "SNR dB-Hz", "Used"}
	aligns := []bool{alignRight, alignLeft, alignRight, alignRight, alignRight, alignLeft}
	boxTable(w, t, headers, aligns, rows)
}

// formatNs renders a nanosecond magnitude with a unit choice that keeps
// the displayed number under four digits where possible: ns → µs → ms →
// s. Used for u-blox time accuracy / clock bias values where the wire
// unit is always ns but the magnitude spans many orders.
func formatNs(ns uint32) string      { return formatNsValue(int64(ns)) }
func formatSignedNs(ns int32) string { return formatNsValue(int64(ns)) }
func formatNsValue(ns int64) string {
	abs := ns
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs < 1_000:
		return fmt.Sprintf("%s ns", thousands(ns))
	case abs < 1_000_000:
		return fmt.Sprintf("%.3f µs", float64(ns)/1_000.0)
	case abs < 1_000_000_000:
		return fmt.Sprintf("%.3f ms", float64(ns)/1_000_000.0)
	default:
		return fmt.Sprintf("%.3f s", float64(ns)/1_000_000_000.0)
	}
}

func yesNo(t term, b bool) string {
	if b {
		return t.green("yes")
	}
	return t.dim("no")
}

// buildStatusJSON returns a JSON-friendly view of the StatusReport.
// Errors are stringified; on success the corresponding error key is omitted.
func buildStatusJSON(r gps.StatusReport) map[string]any {
	out := map[string]any{
		"generation": r.Generation.String(),
	}
	if r.MonVerErr != nil {
		out["receiver_error"] = r.MonVerErr.Error()
	} else {
		out["receiver"] = map[string]any{
			"software":   r.MonVer.SwVersion,
			"hardware":   r.MonVer.HwVersion,
			"extensions": r.MonVer.Extensions,
		}
	}
	if r.TMODEErr != nil {
		out["time_mode_error"] = r.TMODEErr.Error()
	} else {
		modeName := map[int]string{0: "disabled", 1: "survey-in", 2: "fixed"}[r.TMODEMode]
		out["time_mode"] = map[string]any{
			"value": r.TMODEMode,
			"name":  modeName,
		}
	}
	if r.SVINErr != nil {
		out["survey_in_error"] = r.SVINErr.Error()
	} else {
		out["survey_in"] = map[string]any{
			"active":       r.SVIN.Active,
			"valid":        r.SVIN.Valid,
			"duration_s":   r.SVIN.DurationSec,
			"observations": r.SVIN.Observations,
			"mean_acc_m":   r.SVIN.MeanAccMeters,
			"mean_ecef_cm": map[string]int32{
				"x": r.SVIN.MeanXCm,
				"y": r.SVIN.MeanYCm,
				"z": r.SVIN.MeanZCm,
			},
		}
	}
	if r.NAV5Err != nil {
		out["nav5_error"] = r.NAV5Err.Error()
	} else {
		out["nav5"] = map[string]any{
			"dyn_model":      r.NAV5.DynModel,
			"dyn_model_name": gps.DynModelName(r.NAV5.DynModel),
			"fix_mode":       r.NAV5.FixMode,
			"min_elevation":  r.NAV5.MinElev,
			"utc_standard":   r.NAV5.UtcStandard,
		}
	}
	if r.TP5Err != nil {
		out["tp5_error"] = r.TP5Err.Error()
	} else {
		out["tp5"] = map[string]any{
			"active":          r.TP5.Active,
			"lock_to_gnss":    r.TP5.LockGpsFreq,
			"freq_or_period":  r.TP5.FreqPeriod,
			"is_frequency_hz": r.TP5.IsFreq,
			"pulse_value":     r.TP5.PulseLenRatio,
			"is_length_us":    r.TP5.IsLength,
			"align_to_tow":    r.TP5.AlignToTow,
			"rising_at_top":   r.TP5.RisingAtTop,
			"grid_utc":        r.TP5.GridUTC,
		}
	}
	if r.ClockErr != nil {
		out["timing_error"] = r.ClockErr.Error()
	} else {
		out["timing"] = map[string]any{
			"time_accuracy_ns":      r.Clock.TimeAccuracyNs,
			"frequency_accuracy_ps": r.Clock.FreqAccuracyPsPerS,
			"clock_bias_ns":         r.Clock.ClockBiasNs,
			"clock_drift_ns_per_s":  r.Clock.ClockDriftNsPerS,
			"itow_ms":               r.Clock.ITOW,
		}
	}
	if r.SKYErr != nil {
		out["satellites_error"] = r.SKYErr.Error()
	} else {
		sats := make([]map[string]any, 0, len(r.SKY.Satellites))
		for _, s := range r.SKY.Satellites {
			sats = append(sats, map[string]any{
				"prn":       s.PRN,
				"gnss_id":   s.GnssID,
				"gnss_name": gps.GNSSName(s.GnssID),
				"svid":      s.SvID,
				"az_deg":    s.Az,
				"el_deg":    s.El,
				"snr_dbhz":  s.SS,
				"used":      s.Used,
				"health":    s.Health,
				"quality":   s.Qual,
			})
		}
		out["satellites"] = map[string]any{
			"seen":       r.SKY.NSat,
			"used":       r.SKY.USat,
			"hdop":       r.SKY.HDOP,
			"vdop":       r.SKY.VDOP,
			"pdop":       r.SKY.PDOP,
			"gdop":       r.SKY.GDOP,
			"tdop":       r.SKY.TDOP,
			"satellites": sats,
		}
	}
	return out
}

// ----- save subcommand -----

func runSave(args []string) error {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	common := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := gps.SaveConfig(ctx, dc); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	successf(os.Stdout, newTerm(os.Stdout), "All config sections saved to BBR+Flash")
	return nil
}

// ----- configure-stationary subcommand -----

func runConfigureStationary(args []string) error {
	fs := flag.NewFlagSet("configure-stationary", flag.ContinueOnError)
	common := addCommonFlags(fs)
	save := fs.Bool("save", false, "also save to BBR+Flash after writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	tt := newTerm(os.Stdout)
	if err := gps.WriteNAV5DynModel(ctx, dc, gps.DynModelStationary); err != nil {
		return fmt.Errorf("configure-stationary: %w", err)
	}
	successf(os.Stdout, tt, "Dynamic model set to %s (verified)", tt.bold("stationary"))

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		successf(os.Stdout, tt, "Saved to BBR+Flash")
	}
	return nil
}

// ----- configure-pps subcommand -----

func runConfigurePPS(args []string) error {
	fs := flag.NewFlagSet("configure-pps", flag.ContinueOnError)
	common := addCommonFlags(fs)
	save := fs.Bool("save", false, "also save to BBR+Flash after writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	tt := newTerm(os.Stdout)
	if err := gps.WriteTP5_1HzUTC(ctx, dc); err != nil {
		return fmt.Errorf("configure-pps: %w", err)
	}
	successf(os.Stdout, tt, "PPS configured: %s", tt.bold("1 Hz, 50%% duty, UTC-aligned, rising edge"))

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		successf(os.Stdout, tt, "Saved to BBR+Flash")
	}
	return nil
}

// ----- fixed subcommands -----

func runFixedShow(args []string) error {
	fs := flag.NewFlagSet("fixed show", flag.ContinueOnError)
	common := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, _, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return err
	}
	if gen == gps.GenUnknown {
		return errors.New("unknown receiver generation")
	}
	pl, err := gps.PollTMODE(ctx, dc, gen)
	if err != nil {
		return err
	}
	t := newTerm(os.Stdout)
	mode := gps.TMODEMode(gen, pl.Raw)
	modeName := map[int]string{0: "disabled", 1: "survey-in", 2: "fixed"}[mode]
	if modeName == "" {
		modeName = "unknown"
	}
	printTitle(os.Stdout, t, "Fixed-Mode Status")
	labelValue(os.Stdout, t, "Receiver", 14, gen.String())
	labelValue(os.Stdout, t, "Current mode", 14,
		fmt.Sprintf("%s  %s", t.bold(modeName), t.dim(fmt.Sprintf("(timeMode=%d)", mode))))

	switch mode {
	case 2:
		// Active fixed mode — decode and show the locked-in position.
		xCm, yCm, zCm, hpX, hpY, hpZ, accMM, ok := gps.ExtractFixedModeECEF(gen, pl.Raw)
		if !ok {
			return fmt.Errorf("fixed mode active but ECEF payload too short (%d bytes)", len(pl.Raw))
		}
		fmt.Fprintln(os.Stdout)
		printSection(os.Stdout, t, "Locked position", "(ECEF)")
		var rows [][]string
		switch gen {
		case gps.GenM8:
			rows = [][]string{
				{"X", formatMetres(xCm), formatCm(xCm)},
				{"Y", formatMetres(yCm), formatCm(yCm)},
				{"Z", formatMetres(zCm), formatCm(zCm)},
				{"Position accuracy",
					fmt.Sprintf("%.3f m", float64(accMM)/1000.0),
					fmt.Sprintf("(%s mm)", thousands(int64(accMM)))},
			}
		case gps.GenF9:
			rows = [][]string{
				{"X", formatMetres(xCm), formatCmHP(xCm, hpX)},
				{"Y", formatMetres(yCm), formatCmHP(yCm, hpY)},
				{"Z", formatMetres(zCm), formatCmHP(zCm, hpZ)},
				{"Position accuracy",
					fmt.Sprintf("%.3f m", float64(accMM)/1000.0),
					fmt.Sprintf("(%s mm)", thousands(int64(accMM)))},
			}
		}
		boxTable(os.Stdout, t, nil, labelLeftValueRight, rows, 2)

	case 1:
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "  "+t.yellow("Receiver is currently in survey-in mode."))
		fmt.Fprintln(os.Stdout, "  Run "+t.bold("`ubx survey-in`")+" to watch convergence, then "+
			t.bold("`ubx fixed from-svin --save`")+" to lock the converged position.")

	case 0:
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "  "+t.yellow("Receiver has no time mode configured."))
		fmt.Fprintln(os.Stdout, "  Run "+t.bold("`ubx survey-in`")+" to start a survey, or "+
			t.bold("`ubx fixed set <X cm> <Y cm> <Z cm>`")+" to set coordinates manually.")
	}
	return nil
}

func runFixedFromSVIN(args []string) error {
	fs := flag.NewFlagSet("fixed from-svin", flag.ContinueOnError)
	common := addCommonFlags(fs)
	save := fs.Bool("save", false, "also save to BBR+Flash after switching to fixed mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, _, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return err
	}
	if gen == gps.GenUnknown {
		return errors.New("unknown receiver generation")
	}

	// Poll SVIN status to get the converged position.
	svinPoll, err := gps.SVINPollFrame(gen)
	if err != nil {
		return err
	}
	resp, err := dc.SendAndAwaitResponse(ctx, svinPoll, svinPoll.Class, svinPoll.ID)
	if err != nil {
		return fmt.Errorf("SVIN poll: %w", err)
	}
	st, err := gps.ParseSVIN(gen, resp.Payload)
	if err != nil {
		return fmt.Errorf("SVIN parse: %w", err)
	}
	if !st.Valid || st.Active {
		return fmt.Errorf("receiver is not in a converged survey-in state (active=%t valid=%t dur=%ds meanAcc=%.3fm); run `ubx survey-in` first",
			st.Active, st.Valid, st.DurationSec, st.MeanAccMeters)
	}

	tStderr := newTerm(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s surveyed position: ECEF (%s, %s, %s) cm, mean accuracy %.3f m\n",
		tStderr.dim("→"),
		thousands(int64(st.MeanXCm)), thousands(int64(st.MeanYCm)), thousands(int64(st.MeanZCm)),
		st.MeanAccMeters)

	frame, err := gps.FixedModeFrameFromSVIN(gen, st)
	if err != nil {
		return err
	}
	tt := newTerm(os.Stdout)
	if err := gps.WriteTMODE(ctx, dc, gen, frame, frame.Payload); err != nil {
		return fmt.Errorf("fixed from-svin: %w", err)
	}
	successf(os.Stdout, tt, "Receiver locked into %s at the surveyed position", tt.bold("fixed time mode"))

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		successf(os.Stdout, tt, "Saved to BBR+Flash")
	}
	return nil
}

func runFixedSet(args []string) error {
	if len(args) < 3 {
		return errors.New("fixed set requires at least <ecefX_cm> <ecefY_cm> <ecefZ_cm>")
	}
	xCm, err := parseInt32(args[0], "ecefX")
	if err != nil {
		return err
	}
	yCm, err := parseInt32(args[1], "ecefY")
	if err != nil {
		return err
	}
	zCm, err := parseInt32(args[2], "ecefZ")
	if err != nil {
		return err
	}
	rest := args[3:]
	var accMM uint32 = 1000 // 1 m default
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		n, err := parseUint32(rest[0], "accMM")
		if err != nil {
			return err
		}
		accMM = n
		rest = rest[1:]
	}

	fs := flag.NewFlagSet("fixed set", flag.ContinueOnError)
	common := addCommonFlags(fs)
	save := fs.Bool("save", false, "also save to BBR+Flash after switching to fixed mode")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, _, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return err
	}
	if gen == gps.GenUnknown {
		return errors.New("unknown receiver generation")
	}

	var frame gps.Frame
	switch gen {
	case gps.GenM8:
		frame = gps.Frame{
			Class:   gps.ClassCFG,
			ID:      gps.IDCfgTMODE2,
			Payload: gps.BuildFixedModeTMODE2(xCm, yCm, zCm, accMM),
		}
	case gps.GenF9:
		// For F9, 0.1 mm accuracy units. Convert from mm.
		acc01mm := accMM * 10
		frame = gps.Frame{
			Class:   gps.ClassCFG,
			ID:      gps.IDCfgTMODE3,
			Payload: gps.BuildFixedModeTMODE3(xCm, yCm, zCm, 0, 0, 0, acc01mm),
		}
	}

	tt := newTerm(os.Stdout)
	if err := gps.WriteTMODE(ctx, dc, gen, frame, frame.Payload); err != nil {
		return fmt.Errorf("fixed set: %w", err)
	}
	successf(os.Stdout, tt, "Locked into fixed mode at ECEF (%s, %s, %s) cm, accuracy %s mm",
		thousands(int64(xCm)), thousands(int64(yCm)), thousands(int64(zCm)),
		thousands(int64(accMM)))

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		successf(os.Stdout, tt, "Saved to BBR+Flash")
	}
	return nil
}

// ----- deploy subcommand -----

func runDeploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	common := addCommonFlags(fs)
	minDur := fs.Uint("min-duration", 300, "minimum survey-in duration in seconds")
	accM := fs.Float64("accuracy", 2.0, "required accuracy in metres")
	pollInt := fs.Duration("poll-interval", 2*time.Second, "status poll interval")
	maxDur := fs.Duration("max-duration", 0, "overall survey deadline (0 = auto)")
	noSave := fs.Bool("no-save", false, "do not save to flash at the end (default is to save)")
	skipIfFixed := fs.Bool("skip-if-fixed", true, "if receiver is already in fixed mode, skip the survey-in step")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n[ubx] interrupt received — cancelling deploy...")
		cancel()
	}()

	c, dc, err := openClientAndDevice(ctx, common)
	if err != nil {
		return err
	}
	defer c.Close()

	gen, mv, err := gps.DetectGeneration(ctx, dc)
	if err != nil {
		return fmt.Errorf("detect generation: %w", err)
	}
	if gen == gps.GenUnknown {
		return fmt.Errorf("unknown receiver generation (hwVersion=%q)", mv.HwVersion)
	}
	tStderr := newTerm(os.Stderr)
	tStdout := newTerm(os.Stdout)
	printTitle(os.Stderr, tStderr, "Deploy Pipeline")
	labelValue(os.Stderr, tStderr, "Receiver", 14, fmt.Sprintf("%s  %s", gen, tStderr.dim("(hwVersion "+mv.HwVersion+")")))
	labelValue(os.Stderr, tStderr, "Steps", 14, "NAV5 → TP5 → TMODE check → survey-in → fixed lock → save")

	step := func(n int, desc string) {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s %s\n",
			tStderr.bold(fmt.Sprintf("Step %d/5", n)),
			desc)
	}

	step(1, "Configure stationary dynamic model")
	if err := gps.WriteNAV5DynModel(ctx, dc, gps.DynModelStationary); err != nil {
		return fmt.Errorf("deploy/stationary: %w", err)
	}
	successf(os.Stderr, tStderr, "NAV5 dynModel = stationary")

	step(2, "Configure 1 Hz UTC PPS")
	if err := gps.WriteTP5_1HzUTC(ctx, dc); err != nil {
		return fmt.Errorf("deploy/pps: %w", err)
	}
	successf(os.Stderr, tStderr, "TP5 = 1 Hz, 50%% duty, UTC-aligned, rising edge")

	step(3, "Check current TMODE state")
	cur, err := gps.PollTMODE(ctx, dc, gen)
	if err != nil {
		return fmt.Errorf("deploy/poll-tmode: %w", err)
	}
	curMode := gps.TMODEMode(gen, cur.Raw)
	alreadyFixed := curMode == 2
	if alreadyFixed && *skipIfFixed {
		fmt.Fprintln(os.Stderr, tStderr.dim("→")+" receiver is already in fixed mode — skipping survey-in")
	} else {
		step(4, "Run survey-in")
		// In TTY mode the survey-in panel renders here. In non-TTY mode
		// it emits JSON Lines on stdout; either way the gps.SurveyIn
		// helper handles it.
		runEmbeddedSurveyIn := func() error {
			useLivePanel := isTTY(os.Stderr)
			panel := &livePanel{out: os.Stderr}
			jsonEnc := json.NewEncoder(os.Stdout)
			reporter := func(elapsed time.Duration, st gps.SVINStatus) {
				status := surveyStatus(st, uint32(*minDur), *accM)
				if !useLivePanel {
					_ = jsonEnc.Encode(map[string]any{
						"event": "poll", "elapsed_s": int(elapsed.Seconds()),
						"obs": st.Observations, "dur_s": st.DurationSec,
						"mean_acc_m": st.MeanAccMeters, "valid": st.Valid,
						"active": st.Active, "status": status,
					})
					return
				}
				panel.render(surveyPanelLines(tStderr, st, uint32(*minDur), *accM, status))
			}
			_, err := gps.SurveyIn(ctx, dc, gen, gps.SurveyInOptions{
				MinDurationSec: uint32(*minDur),
				AccuracyMeters: *accM,
				PollInterval:   *pollInt,
				MaxDuration:    *maxDur,
			}, reporter, os.Stderr)
			if useLivePanel {
				fmt.Fprintln(os.Stderr)
			}
			return err
		}
		if err := runEmbeddedSurveyIn(); err != nil {
			return fmt.Errorf("deploy/survey-in: %w", err)
		}

		step(5, "Lock into fixed mode at the surveyed position")
		svinPoll, err := gps.SVINPollFrame(gen)
		if err != nil {
			return err
		}
		resp, err := dc.SendAndAwaitResponse(ctx, svinPoll, svinPoll.Class, svinPoll.ID)
		if err != nil {
			return fmt.Errorf("deploy/svin-poll: %w", err)
		}
		st, err := gps.ParseSVIN(gen, resp.Payload)
		if err != nil {
			return err
		}
		if !st.Valid || st.Active {
			return fmt.Errorf("survey-in succeeded but SVIN status is not converged (active=%t valid=%t)", st.Active, st.Valid)
		}
		fmt.Fprintf(os.Stderr, "%s surveyed position: ECEF (%s, %s, %s) cm, mean accuracy %.3f m\n",
			tStderr.dim("→"),
			thousands(int64(st.MeanXCm)), thousands(int64(st.MeanYCm)), thousands(int64(st.MeanZCm)),
			st.MeanAccMeters)
		frame, err := gps.FixedModeFrameFromSVIN(gen, st)
		if err != nil {
			return err
		}
		if err := gps.WriteTMODE(ctx, dc, gen, frame, frame.Payload); err != nil {
			return fmt.Errorf("deploy/fixed: %w", err)
		}
		successf(os.Stderr, tStderr, "Receiver locked into fixed time mode")
	}

	if !*noSave {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, tStderr.bold("Saving to BBR+Flash"))
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("deploy/save: %w", err)
		}
		successf(os.Stderr, tStderr, "Persisted to non-volatile storage")
	} else {
		fmt.Fprintln(os.Stderr, tStderr.yellow("--no-save: config NOT persisted; reboot will revert"))
	}

	fmt.Fprintln(os.Stderr)
	successf(os.Stderr, tStdout, "Deploy complete")
	return nil
}

// parseInt32 parses a decimal signed integer and returns it as int32.
func parseInt32(s, name string) (int32, error) {
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if v < -2147483648 || v > 2147483647 {
		return 0, fmt.Errorf("%s out of int32 range: %d", name, v)
	}
	return int32(v), nil
}

// parseUint32 parses a decimal unsigned integer and returns it as uint32.
func parseUint32(s, name string) (uint32, error) {
	var v uint64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if v > 4294967295 {
		return 0, fmt.Errorf("%s out of uint32 range: %d", name, v)
	}
	return uint32(v), nil
}
