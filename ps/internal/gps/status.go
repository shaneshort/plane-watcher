package gps

import (
	"context"
	"errors"
	"fmt"
	"time"
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

	// Receiver self-assessed clock quality (UBX-NAV-CLOCK). Reports
	// time-of-pulse accuracy, frequency stability, and current clock
	// bias/drift — the key timing-receiver health metrics.
	Clock    ClockStatus
	ClockErr error

	// Satellite reception snapshot — sourced from the most recent gpsd
	// SKY message. Populated by GatherStatus's wait loop.
	SKY    *SKY
	SKYErr error
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

	// 6. NAV-CLOCK — receiver-side timing health (tAcc, fAcc, bias, drift).
	rpt.Clock, rpt.ClockErr = PollNavClock(ctx, dc)

	// 7. Satellite reception (gpsd SKY). gpsd emits SKY messages
	// asynchronously — there's no poll to request one. The client
	// caches the most recent. Wait briefly so a freshly-opened session
	// has time for the first SKY to arrive.
	rpt.SKY = waitForSKY(ctx, dc, 5*time.Second)
	if rpt.SKY == nil {
		rpt.SKYErr = errors.New("no SKY message received within 5s (gpsd may not be reporting satellites)")
	}

	return rpt, nil
}

// waitForSKY polls dc.LatestSKY at 100 ms intervals until a non-nil SKY
// arrives or the timeout / context expires. Returns whatever SKY is
// most recently cached on the device, or nil if none arrived in time.
func waitForSKY(ctx context.Context, dc *DeviceClient, timeout time.Duration) *SKY {
	if sky := dc.LatestSKY(); sky != nil {
		return sky
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return dc.LatestSKY()
		case <-deadline.C:
			return dc.LatestSKY()
		case <-tick.C:
			if sky := dc.LatestSKY(); sky != nil {
				return sky
			}
		}
	}
}
