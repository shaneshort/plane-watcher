package gps

import (
	"context"
	"fmt"
	"strings"
)

// StatusReport is a one-shot snapshot of everything the `ubx status`
// subcommand cares about. Fields that couldn't be read carry the
// corresponding error; other fields remain valid.
type StatusReport struct {
	Generation Generation
	MonVer     MonVer
	MonVerErr  error

	TMODE     TMODEPayload
	TMODEMode int // 0=disabled, 1=survey-in, 2=fixed, -1 unknown
	TMODEErr  error

	SVIN    SVINStatus
	SVINErr error

	NAV5    NAV5Summary
	NAV5Err error

	TP5Raw []byte
	TP5    TP5Summary
	TP5Err error
}

// GatherStatus polls every CFG/MON message used by the `ubx status`
// subcommand and returns a best-effort report. It never fails fast — if
// one poll errors, the rest still run and the report carries per-field
// errors for inspection.
func GatherStatus(ctx context.Context, dc *DeviceClient) (StatusReport, error) {
	var rpt StatusReport

	// 1. MON-VER (and generation detection).
	gen, mv, err := DetectGeneration(ctx, dc)
	rpt.Generation = gen
	rpt.MonVer = mv
	rpt.MonVerErr = err
	if gen == GenUnknown {
		// Without a known generation we can't poll gen-specific
		// messages (TMODE2 vs TMODE3), so surface the error and stop.
		if err == nil {
			err = fmt.Errorf("unknown receiver generation (hwVersion=%q)", mv.HwVersion)
		}
		return rpt, err
	}

	// 2. CFG-TMODE2/3.
	tmode, err := PollTMODE(ctx, dc, gen)
	rpt.TMODE = tmode
	rpt.TMODEErr = err
	rpt.TMODEMode = -1
	if err == nil {
		rpt.TMODEMode = TMODEMode(gen, tmode.Raw)
	}

	// 3. SVIN status (TIM-SVIN for M8, NAV-SVIN for F9).
	svinPoll, svinErr := SVINPollFrame(gen)
	if svinErr == nil {
		resp, pollErr := dc.SendAndAwaitResponse(ctx, svinPoll, svinPoll.Class, svinPoll.ID)
		if pollErr != nil {
			rpt.SVINErr = fmt.Errorf("SVIN poll: %w", pollErr)
		} else {
			st, parseErr := ParseSVIN(gen, resp.Payload)
			if parseErr != nil {
				rpt.SVINErr = parseErr
			} else {
				rpt.SVIN = st
			}
		}
	} else {
		rpt.SVINErr = svinErr
	}

	// 4. CFG-NAV5.
	nav5Raw, err := PollNAV5(ctx, dc)
	if err != nil {
		rpt.NAV5Err = err
	} else {
		rpt.NAV5, rpt.NAV5Err = ParseNAV5(nav5Raw)
	}

	// 5. CFG-TP5.
	tp5Raw, err := PollTP5(ctx, dc)
	rpt.TP5Raw = tp5Raw
	if err != nil {
		rpt.TP5Err = err
	} else {
		rpt.TP5, rpt.TP5Err = ParseTP5(tp5Raw)
	}

	return rpt, nil
}

// Format renders the StatusReport as a human-readable multiline string.
func (r StatusReport) Format() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Receiver\n")
	if r.MonVerErr != nil {
		fmt.Fprintf(&b, "  ERROR: %v\n", r.MonVerErr)
	} else {
		fmt.Fprintf(&b, "  generation: %s\n", r.Generation)
		fmt.Fprintf(&b, "  sw:         %s\n", r.MonVer.SwVersion)
		fmt.Fprintf(&b, "  hw:         %s\n", r.MonVer.HwVersion)
		for _, e := range r.MonVer.Extensions {
			fmt.Fprintf(&b, "  ext:        %s\n", e)
		}
	}

	fmt.Fprintf(&b, "\nTime Mode\n")
	if r.TMODEErr != nil {
		fmt.Fprintf(&b, "  ERROR: %v\n", r.TMODEErr)
	} else {
		modeName := "unknown"
		switch r.TMODEMode {
		case 0:
			modeName = "disabled"
		case 1:
			modeName = "survey-in"
		case 2:
			modeName = "fixed"
		}
		fmt.Fprintf(&b, "  mode:       %s (%d)\n", modeName, r.TMODEMode)
	}

	fmt.Fprintf(&b, "\nSurvey-In\n")
	if r.SVINErr != nil {
		fmt.Fprintf(&b, "  ERROR: %v\n", r.SVINErr)
	} else {
		fmt.Fprintf(&b, "  active:     %t\n", r.SVIN.Active)
		fmt.Fprintf(&b, "  valid:      %t\n", r.SVIN.Valid)
		fmt.Fprintf(&b, "  duration:   %d s\n", r.SVIN.DurationSec)
		fmt.Fprintf(&b, "  obs:        %d\n", r.SVIN.Observations)
		fmt.Fprintf(&b, "  meanAcc:    %.3f m\n", r.SVIN.MeanAccMeters)
		fmt.Fprintf(&b, "  meanECEF:   (%d, %d, %d) cm\n", r.SVIN.MeanXCm, r.SVIN.MeanYCm, r.SVIN.MeanZCm)
	}

	fmt.Fprintf(&b, "\nNavigation Engine (CFG-NAV5)\n")
	if r.NAV5Err != nil {
		fmt.Fprintf(&b, "  ERROR: %v\n", r.NAV5Err)
	} else {
		fmt.Fprintf(&b, "  dynModel:   %s (%d)\n", DynModelName(r.NAV5.DynModel), r.NAV5.DynModel)
		fmt.Fprintf(&b, "  fixMode:    %d\n", r.NAV5.FixMode)
		fmt.Fprintf(&b, "  minElev:    %d deg\n", r.NAV5.MinElev)
		fmt.Fprintf(&b, "  utcStd:     %d\n", r.NAV5.UtcStandard)
	}

	fmt.Fprintf(&b, "\nTime Pulse (CFG-TP5, tpIdx=0)\n")
	if r.TP5Err != nil {
		fmt.Fprintf(&b, "  ERROR: %v\n", r.TP5Err)
	} else {
		fmt.Fprintf(&b, "  active:     %t\n", r.TP5.Active)
		fmt.Fprintf(&b, "  lockGNSS:   %t\n", r.TP5.LockGpsFreq)
		freqUnit := "(period, us)"
		if r.TP5.IsFreq {
			freqUnit = "(freq, Hz)"
		}
		fmt.Fprintf(&b, "  freq:       %d %s\n", r.TP5.FreqPeriod, freqUnit)
		pulseUnit := "(duty, 2^-32)"
		if r.TP5.IsLength {
			pulseUnit = "(length, us)"
		}
		fmt.Fprintf(&b, "  pulseLen:   %d %s\n", r.TP5.PulseLenRatio, pulseUnit)
		fmt.Fprintf(&b, "  alignToTow: %t\n", r.TP5.AlignToTow)
		edge := "falling@top"
		if r.TP5.RisingAtTop {
			edge = "rising@top"
		}
		fmt.Fprintf(&b, "  polarity:   %s\n", edge)
		grid := "GPS"
		if r.TP5.GridUTC {
			grid = "UTC"
		}
		fmt.Fprintf(&b, "  grid:       %s\n", grid)
	}

	return b.String()
}
