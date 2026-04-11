// ubx is a CLI for configuring u-blox GNSS receivers via a running gpsd
// instance. See docs/plans/2026-04-10-ubx-gpsd-tool-design.md for the full
// design.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/gps"
)

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
	fmt.Fprintf(os.Stderr, "[ubx] receiver: %s (%s)\n", gen, mv.HwVersion)

	fmt.Println("ELAPSED  OBS    DUR(s)  MEAN_ACC(m)  STATUS")
	fmt.Println("──────────────────────────────────────────────────")
	reporter := func(elapsed time.Duration, st gps.SVINStatus) {
		status := "surveying"
		if st.Valid && !st.Active {
			status = "CONVERGED"
		}
		fmt.Printf("%5ds  %5d  %6d  %11.3f  %s\n",
			int(elapsed.Seconds()), st.Observations, st.DurationSec, st.MeanAccMeters, status)
	}

	res, err := gps.SurveyIn(ctx, dc, gen, gps.SurveyInOptions{
		MinDurationSec: uint32(*minDur),
		AccuracyMeters: *accM,
		PollInterval:   *pollInt,
		MaxDuration:    *maxDur,
		Ephemeral:      *ephemeral,
	}, reporter, os.Stderr)

	// printFinalSnapshot dumps the last SVIN status we saw — used on
	// SIGINT and timeout so the operator always gets the most recent
	// number before the process exits.
	printFinalSnapshot := func() {
		if res.Status.DurationSec == 0 && res.Status.Observations == 0 {
			return // never got a single SVIN response
		}
		fmt.Fprintln(os.Stderr, "[ubx] final SVIN snapshot:")
		fmt.Fprintf(os.Stderr, "  duration=%ds observations=%d meanAcc=%.3fm valid=%t active=%t\n",
			res.Status.DurationSec, res.Status.Observations, res.Status.MeanAccMeters,
			res.Status.Valid, res.Status.Active)
	}

	switch {
	case res.RollbackFailed:
		// Receiver may be in modified state; print recovery hint and
		// return whatever error came back (which already includes the
		// hex payload from gps.SurveyIn).
		printFinalSnapshot()
		fmt.Fprintln(os.Stderr, "[ubx] WARNING: rollback FAILED — receiver may be in modified state")
		return withExit(1, err)

	case interrupted.Load():
		// Regardless of the err value, SIGINT maps to exit 130.
		printFinalSnapshot()
		if res.RolledBack {
			fmt.Fprintln(os.Stderr, "[ubx] prior TMODE restored after interrupt")
		}
		return withExit(130, fmt.Errorf("survey-in interrupted by signal"))

	case errors.Is(err, gps.ErrSurveyInTimeout):
		// Overall survey deadline expired.
		printFinalSnapshot()
		if res.RolledBack {
			fmt.Fprintln(os.Stderr, "[ubx] prior TMODE restored after timeout")
		}
		return withExit(2, err)

	case err != nil:
		// Any other failure: NAK, protocol error, transport drop.
		if res.RolledBack {
			fmt.Fprintln(os.Stderr, "[ubx] prior TMODE restored after failure")
		}
		return withExit(1, err)

	case res.RolledBack:
		fmt.Fprintln(os.Stderr, "[ubx] prior TMODE restored (--ephemeral)")
	default:
		fmt.Fprintln(os.Stderr, "[ubx] new TMODE config persisted on receiver")
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
	fmt.Print(mv.String())
	fmt.Printf("generation: %s\n", gen)
	return nil
}

// ----- tmode show / set subcommands -----

func runTmodeShow(args []string) error {
	fs := flag.NewFlagSet("tmode show", flag.ContinueOnError)
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
		return errors.New("unknown receiver generation; tmode show needs a known generation")
	}
	pl, err := gps.PollTMODE(ctx, dc, gen)
	if err != nil {
		return err
	}
	fmt.Printf("generation: %s\n", gen)
	fmt.Printf("payload (%d bytes): %s\n", len(pl.Raw), hex.EncodeToString(pl.Raw))
	fmt.Printf("to restore: ubx tmode set %s\n", hex.EncodeToString(pl.Raw))
	return nil
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
	fmt.Println("OK (verified)")
	return nil
}

// ----- status subcommand -----

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	common := addCommonFlags(fs)
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
	fmt.Print(rpt.Format())
	return nil
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
	fmt.Println("OK: saved all config sections to BBR+Flash")
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

	if err := gps.WriteNAV5DynModel(ctx, dc, gps.DynModelStationary); err != nil {
		return fmt.Errorf("configure-stationary: %w", err)
	}
	fmt.Println("OK (verified): dynamic model = stationary")

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		fmt.Println("OK: saved to BBR+Flash")
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

	if err := gps.WriteTP5_1HzUTC(ctx, dc); err != nil {
		return fmt.Errorf("configure-pps: %w", err)
	}
	fmt.Println("OK (verified): PPS = 1 Hz, 50% duty, UTC-aligned, rising edge")

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		fmt.Println("OK: saved to BBR+Flash")
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
	mode := gps.TMODEMode(gen, pl.Raw)
	modeName := map[int]string{0: "disabled", 1: "survey-in", 2: "fixed"}[mode]
	if modeName == "" {
		modeName = "unknown"
	}
	fmt.Printf("generation: %s\n", gen)
	fmt.Printf("mode:       %s (%d)\n", modeName, mode)
	fmt.Printf("payload:    %s\n", hex.EncodeToString(pl.Raw))
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

	fmt.Fprintf(os.Stderr, "[ubx] using surveyed position: ECEF(%d, %d, %d) cm, meanAcc=%.3f m\n",
		st.MeanXCm, st.MeanYCm, st.MeanZCm, st.MeanAccMeters)

	frame, err := gps.FixedModeFrameFromSVIN(gen, st)
	if err != nil {
		return err
	}
	if err := gps.WriteTMODE(ctx, dc, gen, frame, frame.Payload); err != nil {
		return fmt.Errorf("fixed from-svin: %w", err)
	}
	fmt.Println("OK (verified): receiver locked into fixed time mode at surveyed position")

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		fmt.Println("OK: saved to BBR+Flash")
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

	if err := gps.WriteTMODE(ctx, dc, gen, frame, frame.Payload); err != nil {
		return fmt.Errorf("fixed set: %w", err)
	}
	fmt.Printf("OK (verified): fixed mode at ECEF(%d, %d, %d) cm, acc=%d mm\n", xCm, yCm, zCm, accMM)

	if *save {
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		fmt.Println("OK: saved to BBR+Flash")
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
	fmt.Fprintf(os.Stderr, "[ubx] receiver: %s (%s)\n", gen, mv.HwVersion)

	// Step 1: configure stationary dynamic model.
	fmt.Fprintln(os.Stderr, "[ubx] step 1/5: configuring stationary dynamic model")
	if err := gps.WriteNAV5DynModel(ctx, dc, gps.DynModelStationary); err != nil {
		return fmt.Errorf("deploy/stationary: %w", err)
	}

	// Step 2: configure PPS.
	fmt.Fprintln(os.Stderr, "[ubx] step 2/5: configuring 1 Hz UTC PPS")
	if err := gps.WriteTP5_1HzUTC(ctx, dc); err != nil {
		return fmt.Errorf("deploy/pps: %w", err)
	}

	// Step 3: check current TMODE. If already in fixed mode, optionally
	// skip the survey.
	fmt.Fprintln(os.Stderr, "[ubx] step 3/5: checking current TMODE state")
	cur, err := gps.PollTMODE(ctx, dc, gen)
	if err != nil {
		return fmt.Errorf("deploy/poll-tmode: %w", err)
	}
	curMode := gps.TMODEMode(gen, cur.Raw)
	alreadyFixed := curMode == 2
	if alreadyFixed && *skipIfFixed {
		fmt.Fprintln(os.Stderr, "[ubx]   receiver is already in fixed mode — skipping survey-in")
	} else {
		// Step 4: run survey-in.
		fmt.Fprintln(os.Stderr, "[ubx] step 4/5: running survey-in")
		fmt.Println("ELAPSED  OBS    DUR(s)  MEAN_ACC(m)  STATUS")
		fmt.Println("──────────────────────────────────────────────────")
		reporter := func(elapsed time.Duration, st gps.SVINStatus) {
			status := "surveying"
			if st.Valid && !st.Active {
				status = "CONVERGED"
			}
			fmt.Printf("%5ds  %5d  %6d  %11.3f  %s\n",
				int(elapsed.Seconds()), st.Observations, st.DurationSec, st.MeanAccMeters, status)
		}
		res, err := gps.SurveyIn(ctx, dc, gen, gps.SurveyInOptions{
			MinDurationSec: uint32(*minDur),
			AccuracyMeters: *accM,
			PollInterval:   *pollInt,
			MaxDuration:    *maxDur,
		}, reporter, os.Stderr)
		if err != nil {
			return fmt.Errorf("deploy/survey-in: %w", err)
		}
		_ = res

		// Step 5a: poll SVIN for the converged position and switch to
		// fixed mode.
		fmt.Fprintln(os.Stderr, "[ubx] step 5/5: locking into fixed mode at surveyed position")
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
		fmt.Fprintf(os.Stderr, "[ubx]   ECEF(%d, %d, %d) cm, meanAcc=%.3f m\n",
			st.MeanXCm, st.MeanYCm, st.MeanZCm, st.MeanAccMeters)

		frame, err := gps.FixedModeFrameFromSVIN(gen, st)
		if err != nil {
			return err
		}
		if err := gps.WriteTMODE(ctx, dc, gen, frame, frame.Payload); err != nil {
			return fmt.Errorf("deploy/fixed: %w", err)
		}
	}

	// Save.
	if !*noSave {
		fmt.Fprintln(os.Stderr, "[ubx] saving to BBR+Flash")
		if err := gps.SaveConfig(ctx, dc); err != nil {
			return fmt.Errorf("deploy/save: %w", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "[ubx] --no-save: config NOT persisted; reboot will revert")
	}

	fmt.Fprintln(os.Stderr, "[ubx] deploy complete")
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
