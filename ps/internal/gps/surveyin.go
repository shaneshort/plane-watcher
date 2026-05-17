package gps

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// SurveyInOptions configures a survey-in run.
type SurveyInOptions struct {
	MinDurationSec uint32
	AccuracyMeters float64
	PollInterval   time.Duration
	// MaxDuration caps the entire survey-in (not just one poll). When the
	// deadline expires before convergence, SurveyIn returns a timeout
	// error and runs the rollback handler. If zero, defaults to
	// max(MinDurationSec * 3, MinDurationSec + 60s) — a "safety factor"
	// generous enough that a real receiver should converge within it.
	MaxDuration time.Duration
	// Ephemeral inverts the success-path semantics: even on a successful
	// convergence, restore the cached pre-run TMODE before exiting. Used
	// for testing against production receivers you don't want to
	// reconfigure.
	Ephemeral bool
}

// SurveyInResult is what SurveyIn returns. Inspect RolledBack and
// RollbackFailed to understand what state the receiver is in:
//
//	RolledBack=false RollbackFailed=false → new TMODE persisted (default success)
//	RolledBack=true  RollbackFailed=false → cached TMODE restored
//	RolledBack=false RollbackFailed=true  → restore was attempted and FAILED;
//	                                        receiver is in modified state, use
//	                                        CachedTMODE.Raw with `ubx tmode set`
type SurveyInResult struct {
	Status         SVINStatus
	CachedTMODE    TMODEPayload
	RolledBack     bool
	RollbackFailed bool
}

// ErrSurveyInTimeout is returned when MaxDuration elapses before the
// receiver reports convergence.
var ErrSurveyInTimeout = errors.New("survey-in: overall deadline expired before convergence")

// SurveyIn runs the full read-before-write → configure → poll → persist or
// rollback flow on a single device. The reporter callback is invoked after
// each successful SVIN poll so the CLI can print progress lines.
//
// SurveyIn uses named return values so the deferred rollback handler can
// surface rollback errors back to the caller — a rollback failure on what
// would otherwise be a success path becomes a non-nil error.
func SurveyIn(
	ctx context.Context,
	dc *DeviceClient,
	gen Generation,
	opts SurveyInOptions,
	reporter func(elapsed time.Duration, st SVINStatus),
	debugOut io.Writer,
) (result SurveyInResult, err error) {
	if gen == GenUnknown {
		return SurveyInResult{}, errors.New("survey-in: unknown receiver generation; refusing to send config")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.MaxDuration <= 0 {
		// Safety factor: 3× min-duration, with a minimum extra of 60s
		// so very short surveys still get a reasonable buffer.
		base := time.Duration(opts.MinDurationSec) * time.Second
		opts.MaxDuration = base * 3
		if extra := base + 60*time.Second; opts.MaxDuration < extra {
			opts.MaxDuration = extra
		}
	}

	// Wrap the caller's context with the overall survey deadline. Both
	// SIGINT cancellation (caller cancels ctx) and timeout-driven exit
	// flow through the same path.
	surveyCtx, cancelSurvey := context.WithTimeout(ctx, opts.MaxDuration)
	defer cancelSurvey()

	// 1. Read-before-write: cache current TMODE so we can roll back.
	cached, err := PollTMODE(surveyCtx, dc, gen)
	if err != nil {
		return SurveyInResult{}, fmt.Errorf("read-before-write: %w", err)
	}
	result.CachedTMODE = cached
	// Cached payload is returned via result.CachedTMODE so the CLI can
	// surface the recovery hex only on failure paths. Avoiding the
	// preamble in the success path keeps the interactive output clean.

	// 2. Build & install rollback. Initially ARMED — failure paths run
	// it. The success path disarms it (unless --ephemeral).
	//
	// IMPORTANT: a bare ACK is NOT proof of receiver state. UBX ACKs
	// carry no request nonce, so a late ACK from a previously timed-out
	// write of the same class/id can falsely satisfy a wait. Every
	// TMODE write — initial AND rollback — must be verified by reading
	// TMODE back and bytes-comparing against the expected payload.
	// WriteTMODE is the single chokepoint that enforces this.
	armed := true
	runRollback := func() error {
		restoreFrame, rfErr := RestoreFrame(cached)
		if rfErr != nil {
			return fmt.Errorf("build restore frame: %w", rfErr)
		}
		// Fresh deadline so a cancelled outer context doesn't
		// immediately abort the restore attempt.
		rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return WriteTMODE(rbCtx, dc, gen, restoreFrame, cached.Raw)
	}

	defer func() {
		if !armed {
			return
		}
		debugf(debugOut, "[ubx] rolling back to cached TMODE...")
		if rbErr := runRollback(); rbErr != nil {
			result.RolledBack = false
			result.RollbackFailed = true
			debugf(debugOut, "[ubx] ROLLBACK FAILED: %v", rbErr)
			debugf(debugOut, "[ubx] cached payload (manual recovery): ubx tmode set %s",
				hex.EncodeToString(cached.Raw))
			// Surface rollback failure back to the caller. If there
			// was already an error, wrap it; if not (the only way
			// that happens is the --ephemeral success path), the
			// rollback failure becomes the error.
			rollbackErr := fmt.Errorf("rollback failed: %w (manual recovery: ubx tmode set %s)",
				rbErr, hex.EncodeToString(cached.Raw))
			if err == nil {
				err = rollbackErr
			} else {
				err = fmt.Errorf("%w; %v", err, rollbackErr)
			}
			return
		}
		result.RolledBack = true
		debugf(debugOut, "[ubx] rollback complete")
	}()

	// 3. Send the survey-in TMODE config and VERIFY by read-back.
	// Same stale-ACK race applies to the initial write as to rollback,
	// so we go through WriteTMODE here too. SurveyInFrame does the
	// per-generation accuracy unit conversion (mm for M8, 0.1 mm for F9)
	// so we pass AccuracyMeters directly.
	configFrame, err := SurveyInFrame(gen, opts.MinDurationSec, opts.AccuracyMeters)
	if err != nil {
		return result, fmt.Errorf("build survey-in frame: %w", err)
	}
	cfgCtx, cancel := context.WithTimeout(surveyCtx, 10*time.Second)
	cfgErr := WriteTMODE(cfgCtx, dc, gen, configFrame, configFrame.Payload)
	cancel()
	if cfgErr != nil {
		// On NAK, timeout, or verify mismatch: receiver may or may
		// not have applied the config. Rollback handler runs via the
		// deferred closure.
		return result, fmt.Errorf("survey-in config: %w", cfgErr)
	}

	// 4. Poll loop.
	//
	// Same UBX-no-nonce principle as TMODE writes: a single SVIN poll
	// response cannot be trusted in isolation, because a late stale
	// reply from a previously timed-out poll could falsely show
	// `valid && !active` and terminate the loop early. Defence: require
	// TWO consecutive convergence reports across consecutive poll
	// cycles before declaring success. Two stale frames lining up is
	// essentially impossible.
	start := time.Now()
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	pollFrame, err := SVINPollFrame(gen)
	if err != nil {
		return result, err
	}
	respClass, respID := pollFrame.Class, pollFrame.ID

	prevConverged := false
	for {
		// Send a poll, wait for the response. Each poll has its own
		// short timeout AND inherits the overall survey deadline.
		pollCtx, cancel := context.WithTimeout(surveyCtx, 5*time.Second)
		respFrame, pollErr := dc.SendAndAwaitResponse(pollCtx, pollFrame, respClass, respID)
		cancel()
		if pollErr != nil {
			if errors.Is(pollErr, context.Canceled) || errors.Is(pollErr, context.DeadlineExceeded) {
				if surveyCtx.Err() != nil {
					if errors.Is(surveyCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
						return result, ErrSurveyInTimeout
					}
					return result, fmt.Errorf("survey-in interrupted: %w", surveyCtx.Err())
				}
				// Just this poll timed out. Reset the
				// convergence streak and try again.
				prevConverged = false
				debugf(debugOut, "[ubx] SVIN poll timeout; retrying")
			} else {
				return result, fmt.Errorf("survey-in poll: %w", pollErr)
			}
		} else {
			st, perr := ParseSVIN(gen, respFrame.Payload)
			if perr != nil {
				debugf(debugOut, "[ubx] parse SVIN: %v", perr)
				prevConverged = false
			} else {
				result.Status = st
				if reporter != nil {
					reporter(time.Since(start), st)
				}
				converged := st.Valid && !st.Active
				if converged && prevConverged {
					// Two consecutive convergence reports
					// — confirmed. Disarm rollback unless
					// --ephemeral.
					if !opts.Ephemeral {
						armed = false
					}
					return result, nil
				}
				prevConverged = converged
			}
		}

		// Wait for next tick or cancellation.
		select {
		case <-surveyCtx.Done():
			if errors.Is(surveyCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return result, ErrSurveyInTimeout
			}
			return result, fmt.Errorf("survey-in interrupted: %w", surveyCtx.Err())
		case <-ticker.C:
		}
	}
}

// WriteAndVerify writes a CFG frame and confirms the receiver applied it
// by reading the target setting back via a poll. This is the single
// chokepoint used by every CFG write in this package — TMODE2/TMODE3,
// NAV5, TP5 — because UBX has no request nonce and a bare ACK or a single
// poll response cannot be trusted in isolation (stale frames from prior
// timed-out requests can satisfy a fresh wait).
//
// The verify strategy requires TWO CONSECUTIVE successful verify polls,
// with a retry-on-mismatch inside each slot:
//
//   - Retry-on-mismatch handles stale frames that LOOK wrong.
//   - Two-consecutive-match closes the stale-matching-frame hole: even if
//     one slot consumes a stale frame that happens to satisfy the verify
//     predicate, a second independent poll after a drain is extremely
//     unlikely to line up the same way.
//
// verify is called with the raw poll-response payload. It returns nil if
// the payload matches whatever the caller expected, or a descriptive
// error otherwise (wrapped into the outer mismatch error).
func WriteAndVerify(
	ctx context.Context,
	dc *DeviceClient,
	frame Frame,
	pollClass, pollID byte,
	verify func(got []byte) error,
) error {
	if err := dc.SendAndAwaitACK(ctx, frame); err != nil {
		return fmt.Errorf("ACK: %w", err)
	}
	for slot := 0; slot < 2; slot++ {
		if err := writeVerifySlot(ctx, dc, pollClass, pollID, verify, slot); err != nil {
			return err
		}
	}
	return nil
}

// writeVerifySlot does one verify poll with a retry-on-mismatch.
func writeVerifySlot(ctx context.Context, dc *DeviceClient, pollClass, pollID byte, verify func([]byte) error, slot int) error {
	pollFrame := PollFrame(pollClass, pollID)
	resp, err := dc.SendAndAwaitResponse(ctx, pollFrame, pollClass, pollID)
	if err != nil {
		return fmt.Errorf("verify poll slot %d: %w", slot, err)
	}
	if verifyErr := verify(resp.Payload); verifyErr == nil {
		return nil
	}
	// Mismatch — could be a single stale frame. Drain and retry once.
	dc.Drain()
	resp2, err := dc.SendAndAwaitResponse(ctx, pollFrame, pollClass, pollID)
	if err != nil {
		return fmt.Errorf("verify poll slot %d retry: %w", slot, err)
	}
	if verifyErr := verify(resp2.Payload); verifyErr != nil {
		return fmt.Errorf("verify mismatch at slot %d: %w", slot, verifyErr)
	}
	return nil
}

// WriteTMODE writes a CFG-TMODE2 / CFG-TMODE3 frame and verifies by poll
// read-back. This is the chokepoint for every TMODE write — survey-in
// config, rollback restore, manual `tmode set`, and fixed-mode lock.
func WriteTMODE(ctx context.Context, dc *DeviceClient, gen Generation, frame Frame, expectedPayload []byte) error {
	pollID := byte(IDCfgTMODE3)
	if gen == GenM8 {
		pollID = IDCfgTMODE2
	}
	// Verify against the mode-relevant subset of fields. The u-blox M8
	// firmware retains the prior-mode field values when switching mode
	// via CFG-TMODE2 (e.g. ECEF/fixedPosAcc survive a switch into
	// Survey-in mode where those fields are unused). A strict byte-equal
	// verify would reject a correctly-applied mode-switch write.
	verify := func(got []byte) error {
		if err := compareTMODEByMode(gen, got, expectedPayload); err != nil {
			return fmt.Errorf("receiver TMODE = %s, expected %s: %w",
				hex.EncodeToString(got), hex.EncodeToString(expectedPayload), err)
		}
		return nil
	}
	return WriteAndVerify(ctx, dc, frame, ClassCFG, pollID, verify)
}

// PollTMODE polls the appropriate CFG-TMODE message for the receiver
// generation and returns the cached payload bytes.
func PollTMODE(ctx context.Context, dc *DeviceClient, gen Generation) (TMODEPayload, error) {
	pollFrame, err := PollTMODEFrame(gen)
	if err != nil {
		return TMODEPayload{}, err
	}
	respCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := dc.SendAndAwaitResponse(respCtx, pollFrame, pollFrame.Class, pollFrame.ID)
	if err != nil {
		return TMODEPayload{}, fmt.Errorf("poll TMODE: %w", err)
	}
	return TMODEPayload{Generation: gen, Raw: append([]byte(nil), resp.Payload...)}, nil
}

// DetectGeneration polls UBX-MON-VER and returns the receiver generation.
// Returns GenUnknown (with no error) if the hwVersion does not match a known
// family — callers should refuse to proceed in that case.
func DetectGeneration(ctx context.Context, dc *DeviceClient) (Generation, MonVer, error) {
	pollFrame := PollFrame(ClassMON, IDMonVER)
	respCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := dc.SendAndAwaitResponse(respCtx, pollFrame, ClassMON, IDMonVER)
	if err != nil {
		return GenUnknown, MonVer{}, fmt.Errorf("MON-VER poll: %w", err)
	}
	mv, err := ParseMonVer(resp.Payload)
	if err != nil {
		return GenUnknown, MonVer{}, err
	}
	return mv.Generation(), mv, nil
}

func tmodeNumberFor(g Generation) int {
	if g == GenM8 {
		return 2
	}
	return 3
}

func debugf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format+"\n", args...)
}

// bytesEqual is a tiny helper avoiding the bytes package import for one call.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
