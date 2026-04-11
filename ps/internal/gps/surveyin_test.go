package gps

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// extractUbxClassID parses a `?DEVICE={"path":"...","hexdata":"..."};` command
// line, decodes the hex, and returns the inner UBX frame's class and ID.
// Used by the test scripts to dispatch responses based on which UBX command
// the client sent.
var hexdataRE = regexp.MustCompile(`"hexdata":"([0-9a-fA-F]+)"`)

func extractUbxClassID(cmd string) (class, id byte, ok bool) {
	m := hexdataRE.FindStringSubmatch(cmd)
	if len(m) != 2 {
		return 0, 0, false
	}
	b, err := hex.DecodeString(m[1])
	if err != nil || len(b) < 4 {
		return 0, 0, false
	}
	if b[0] != SyncByte1 || b[1] != SyncByte2 {
		return 0, 0, false
	}
	return b[2], b[3], true
}

// scriptedDevice is a tiny test harness that script-replies to whatever
// command the gps.Client sends. It saves us from having to manage timing of
// inject calls vs the survey-in state machine.
//
// The script is a slice of responder functions; each one is invoked when a
// matching command is observed.
// scriptOpts customises the behaviour of the test script dispatcher beyond
// the basic happy path.
type scriptOpts struct {
	// AckInitialTMODE controls whether the first CFG-TMODE write is
	// ACK'd (true) or NAK'd (false).
	AckInitialTMODE bool
	// DropInitialTMODEWrite simulates the post-send stale-ACK race on
	// the INITIAL survey-in write: the script ACKs but the stored
	// "current TMODE state" is NOT updated. The verify-by-poll path
	// must catch this and fail before entering the SVIN poll loop.
	DropInitialTMODEWrite bool
	// DropRollbackWrites simulates the same race for rollback writes:
	// any TMODE write after the initial one is ACK'd but the receiver's
	// stored state is NOT updated. The rollback verify must fail.
	DropRollbackWrites bool
	// StaleVerifyPollsM8 / F9: for the first N TMODE polls, return this
	// stale payload instead of currentTMODE. Models a late TMODE poll
	// response from a previously timed-out poll arriving on top of a
	// fresh poll. WriteTMODE's retry-on-mismatch path must drain this
	// and re-poll to get the real value.
	StaleVerifyPollPayload []byte // if set, returned for next StaleVerifyPollCount polls
	StaleVerifyPollCount   int
}

// startSurveyInScript spins up a fakeGpsd that dispatches replies based on
// which UBX command the client sends. The script tracks "current TMODE
// state" so post-rollback verify polls return a faithful answer.
func startSurveyInScript(t *testing.T, devicePath string, gen Generation, cachedTMODE []byte, svinSequence []SVINStatus, opts scriptOpts) (*Client, *DeviceClient, *fakeGpsd) {
	t.Helper()
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	f.injectDevices(DeviceInfo{Path: devicePath, Driver: "u-blox"})

	dc := c.Subscribe(devicePath)

	tmodeCfgID := byte(IDCfgTMODE3)
	svinClass := byte(ClassNAV)
	svinID := byte(IDNavSVIN)
	hw := "00190000"
	if gen == GenM8 {
		tmodeCfgID = IDCfgTMODE2
		svinClass = ClassTIM
		svinID = IDTimSVIN
		hw = "00080000"
	}

	// currentTMODE models the receiver's actual current TMODE state.
	// Starts as cachedTMODE, updated by TMODE writes (unless dropped).
	currentTMODE := append([]byte(nil), cachedTMODE...)
	staleRemaining := opts.StaleVerifyPollCount

	go func() {
		seenInitialTMODEWrite := false
		svinIdx := 0
		for {
			cmd, err := f.awaitCommand(3 * time.Second)
			if err != nil {
				return
			}
			class, id, ok := extractUbxClassID(cmd)
			if !ok {
				continue
			}
			payloadLen := payloadLenFromCommand(cmd)
			isPoll := payloadLen == 0
			switch {
			case class == ClassMON && id == IDMonVER && isPoll:
				f.injectRaw(devicePath, Frame{
					Class:   ClassMON,
					ID:      IDMonVER,
					Payload: buildMonVerPayload("EXT CORE 1.00", hw),
				})
			case class == ClassCFG && id == tmodeCfgID && isPoll:
				// TMODE poll → return either a stale payload
				// (if a write has happened AND
				// StaleVerifyPollCount > 0) or the receiver's
				// actual current state. Stale polls are gated
				// on seenInitialTMODEWrite so the read-before-
				// write step still captures the real cached
				// state.
				var payload []byte
				if seenInitialTMODEWrite && staleRemaining > 0 && opts.StaleVerifyPollPayload != nil {
					payload = append([]byte(nil), opts.StaleVerifyPollPayload...)
					staleRemaining--
				} else {
					payload = append([]byte(nil), currentTMODE...)
				}
				f.injectRaw(devicePath, Frame{
					Class:   ClassCFG,
					ID:      tmodeCfgID,
					Payload: payload,
				})
			case class == ClassCFG && id == tmodeCfgID && !isPoll:
				// TMODE write. Update or drop receiver state,
				// then ACK or NAK.
				ackKind := byte(IDAckACK)
				if !seenInitialTMODEWrite {
					if !opts.AckInitialTMODE {
						ackKind = IDAckNAK
					}
					if opts.AckInitialTMODE && !opts.DropInitialTMODEWrite {
						currentTMODE = extractWritePayload(cmd, len(currentTMODE))
					}
					seenInitialTMODEWrite = true
				} else {
					// Rollback (or further config). Honour
					// DropRollbackWrites — script ACKs but
					// receiver state stays modified.
					if !opts.DropRollbackWrites {
						currentTMODE = extractWritePayload(cmd, len(currentTMODE))
					}
				}
				f.injectRaw(devicePath, Frame{
					Class:   ClassACK,
					ID:      ackKind,
					Payload: []byte{ClassCFG, tmodeCfgID},
				})
			case class == svinClass && id == svinID && isPoll:
				if len(svinSequence) == 0 {
					continue
				}
				st := svinSequence[len(svinSequence)-1]
				if svinIdx < len(svinSequence) {
					st = svinSequence[svinIdx]
					svinIdx++
				}
				f.injectRaw(devicePath, Frame{
					Class:   svinClass,
					ID:      svinID,
					Payload: encodeSVINPayload(gen, st),
				})
			}
		}
	}()

	return c, dc, f
}

// extractWritePayload pulls the UBX payload bytes out of a `?DEVICE` command
// line. Used by the script to track receiver state on TMODE writes.
func extractWritePayload(cmd string, expectedLen int) []byte {
	m := hexdataRE.FindStringSubmatch(cmd)
	if len(m) != 2 {
		return make([]byte, expectedLen)
	}
	b, err := hex.DecodeString(m[1])
	if err != nil || len(b) < 8 {
		return make([]byte, expectedLen)
	}
	plLen := int(b[4]) | int(b[5])<<8
	if plLen != expectedLen {
		return make([]byte, expectedLen)
	}
	out := make([]byte, plLen)
	copy(out, b[6:6+plLen])
	return out
}

// payloadLenFromCommand decodes the UBX payload length out of a `?DEVICE`
// command line. Returns -1 if the command isn't parseable.
func payloadLenFromCommand(cmd string) int {
	m := hexdataRE.FindStringSubmatch(cmd)
	if len(m) != 2 {
		return -1
	}
	b, err := hex.DecodeString(m[1])
	if err != nil || len(b) < 6 {
		return -1
	}
	return int(b[4]) | int(b[5])<<8
}

func encodeSVINPayload(gen Generation, st SVINStatus) []byte {
	if gen == GenM8 {
		pl := make([]byte, 28)
		binary.LittleEndian.PutUint32(pl[0:4], st.DurationSec)
		// meanV (variance in mm^2). Convert metres → mm → square.
		mm := st.MeanAccMeters * 1000
		binary.LittleEndian.PutUint32(pl[16:20], uint32(mm*mm))
		binary.LittleEndian.PutUint32(pl[20:24], st.Observations)
		if st.Valid {
			pl[24] = 1
		}
		if st.Active {
			pl[25] = 1
		}
		return pl
	}
	pl := make([]byte, 40)
	binary.LittleEndian.PutUint32(pl[8:12], st.DurationSec)
	binary.LittleEndian.PutUint32(pl[28:32], uint32(st.MeanAccMeters*10000))
	binary.LittleEndian.PutUint32(pl[32:36], st.Observations)
	if st.Valid {
		pl[36] = 1
	}
	if st.Active {
		pl[37] = 1
	}
	return pl
}

func TestSurveyIn_F9_HappyPath(t *testing.T) {
	cachedTMODE := make([]byte, 40) // F9: 40-byte TMODE3 payload, all zeros = disabled
	// Two consecutive converged frames are required to confirm success.
	svinSequence := []SVINStatus{
		{DurationSec: 2, Observations: 47, MeanAccMeters: 142.318, Active: true},
		{DurationSec: 4, Observations: 96, MeanAccMeters: 87.221, Active: true},
		{DurationSec: 6, Observations: 6240, MeanAccMeters: 1.847, Active: false, Valid: true},
		{DurationSec: 8, Observations: 6280, MeanAccMeters: 1.823, Active: false, Valid: true},
	}
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, svinSequence, scriptOpts{AckInitialTMODE: true})
	_ = c

	// Drive detect first (SurveyIn doesn't do it internally — the CLI does).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gen, _, err := DetectGeneration(ctx, dc)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if gen != GenF9 {
		t.Fatalf("gen = %s, want F9", gen)
	}

	progress := []SVINStatus{}
	res, err := SurveyIn(ctx, dc, gen, SurveyInOptions{
		MinDurationSec: 5,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
	}, func(_ time.Duration, st SVINStatus) {
		progress = append(progress, st)
	}, nil)
	if err != nil {
		t.Fatalf("SurveyIn: %v", err)
	}
	if !res.Status.Valid || res.Status.Active {
		t.Errorf("final status: %+v", res.Status)
	}
	if len(progress) != 4 {
		t.Errorf("progress entries: %d, want 4", len(progress))
	}
	// Default success path: rollback should NOT have run.
	if res.RolledBack {
		t.Errorf("default success path should not roll back")
	}
}

func TestSurveyIn_F9_Ephemeral(t *testing.T) {
	cachedTMODE := make([]byte, 40)
	svinSequence := []SVINStatus{
		{DurationSec: 2, Observations: 100, MeanAccMeters: 1.5, Active: false, Valid: true},
		{DurationSec: 3, Observations: 110, MeanAccMeters: 1.5, Active: false, Valid: true},
	}
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, svinSequence, scriptOpts{AckInitialTMODE: true})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	res, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 5,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
		Ephemeral:      true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("SurveyIn ephemeral: %v", err)
	}
	if !res.RolledBack {
		t.Errorf("ephemeral success path should roll back")
	}
}

func TestSurveyIn_F9_AckNak(t *testing.T) {
	cachedTMODE := make([]byte, 40)
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, nil, scriptOpts{AckInitialTMODE: false})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	_, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 5,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
	}, nil, nil)
	if err == nil {
		t.Fatalf("expected error on ACK-NAK")
	}
	if !errors.Is(err, ErrAckNAK) && !strings.Contains(err.Error(), "ACK-NAK") {
		t.Errorf("expected ACK-NAK error, got %v", err)
	}
}

func TestSurveyIn_M8_HappyPath(t *testing.T) {
	cachedTMODE := make([]byte, 28)
	svinSequence := []SVINStatus{
		{DurationSec: 5, Observations: 100, MeanAccMeters: 1.5, Active: false, Valid: true},
		{DurationSec: 6, Observations: 110, MeanAccMeters: 1.5, Active: false, Valid: true},
	}
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenM8, cachedTMODE, svinSequence, scriptOpts{AckInitialTMODE: true})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gen, _, err := DetectGeneration(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if gen != GenM8 {
		t.Fatalf("gen = %s, want M8", gen)
	}

	res, err := SurveyIn(ctx, dc, gen, SurveyInOptions{
		MinDurationSec: 5,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
	}, nil, nil)
	if err != nil {
		t.Fatalf("SurveyIn M8: %v", err)
	}
	if !res.Status.Valid {
		t.Errorf("final status not valid: %+v", res.Status)
	}
}

func TestSurveyIn_UnknownGenerationRefuses(t *testing.T) {
	f, c := newFakeGpsd(t)
	f.drainWatchHandshake(t)
	dc := c.Subscribe("/dev/ttyPS1")

	_, err := SurveyIn(context.Background(), dc, GenUnknown, SurveyInOptions{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown receiver generation") {
		t.Errorf("expected refusal, got %v", err)
	}
}

// TestSurveyIn_F9_TimeoutRollback verifies that an overall MaxDuration
// expiry triggers rollback and returns ErrSurveyInTimeout. This is the rev4
// requirement that "a receiver that never converges" must auto-rollback
// instead of being left in modified state.
func TestSurveyIn_F9_TimeoutRollback(t *testing.T) {
	cachedTMODE := make([]byte, 40)
	// SVIN sequence that NEVER converges — every poll returns active=1.
	svinSequence := []SVINStatus{
		{DurationSec: 1, Observations: 10, MeanAccMeters: 50.0, Active: true},
	}
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, svinSequence, scriptOpts{AckInitialTMODE: true})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	res, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 1,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
		MaxDuration:    150 * time.Millisecond, // forces fast timeout
	}, nil, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrSurveyInTimeout) {
		t.Errorf("expected ErrSurveyInTimeout, got %v", err)
	}
	if !res.RolledBack {
		t.Errorf("expected rollback to have run on timeout")
	}
	if res.RollbackFailed {
		t.Errorf("rollback should have succeeded (script ACKs all TMODE writes)")
	}
}

// TestWriteTMODE_StaleMatchingPollDirectRace is the regression test for the
// fifth-pass code-review finding: retry-on-mismatch is insufficient because
// a stale poll response that happens to MATCH expectedPayload exactly will
// false-satisfy a single verify poll. The fix is two-consecutive-match.
//
// Concrete scenario being modelled: during rollback, expectedPayload is
// the cached pre-run TMODE. A late poll response from the read-before-
// write step (which carries exactly those cached bytes) arrives during the
// rollback's verify. With single-poll verify, it false-passes. With
// two-slot verify, slot 0 consumes the stale matching frame but slot 1
// polls fresh and sees the receiver's real (modified) state → mismatch. The script is primed with a single stale
// poll payload that matches what the test will pass as expectedPayload,
// while the receiver's real state differs. Two-slot verify must fail
// because slot 1 (a fresh poll after slot 0's drain) sees the real state.
func TestWriteTMODE_StaleMatchingPollDirectRace(t *testing.T) {
	// Receiver's real state = survey-in payload (non-zero).
	realState := BuildSurveyInTMODE3(300, 20000)
	// Expected payload = all zeros (the "cached" rollback target).
	// Stale frame returned by the FIRST verify poll will carry these
	// expected bytes and would false-satisfy single-poll verify.
	expectedPayload := make([]byte, 40)

	// Start a script with the receiver already in realState, and the
	// initial write dropped so the write we issue here doesn't move it.
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, realState, nil, scriptOpts{
		AckInitialTMODE:        true,
		DropInitialTMODEWrite:  true, // our write to expected bytes is dropped, receiver stays in realState
		StaleVerifyPollPayload: expectedPayload,
		StaleVerifyPollCount:   1, // first verify poll returns the stale matching frame
	})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	// Build a frame that carries expectedPayload and call WriteTMODE.
	// Slot 0: stale matching frame → matches → slot 0 passes.
	// Slot 1: fresh poll → receiver's real state (realState) → MISMATCH.
	// Slot 1 retry: same realState → MISMATCH → WriteTMODE returns error.
	frame := Frame{Class: ClassCFG, ID: IDCfgTMODE3, Payload: expectedPayload}
	err := WriteTMODE(ctx, dc, GenF9, frame, expectedPayload)
	if err == nil {
		t.Fatal("WriteTMODE should have caught stale-matching verify; expected non-nil error")
	}
	if !strings.Contains(err.Error(), "verify mismatch at slot 1") {
		t.Errorf("expected mismatch at slot 1, got %q", err.Error())
	}
}

// TestSurveyIn_StaleConvergedFrameRequiresConfirmation verifies that the
// SVIN poll loop requires TWO consecutive `valid && !active` frames to
// declare success. A single stale "converged" frame (e.g., from a prior
// session's converged survey, arriving late on the wire) must NOT
// terminate the loop on its own.
//
// Sequence: stale converged → real active → real active → converged →
// converged. Expected: loop runs through the sequence and only terminates
// on the last two consecutive convergence reports, not on the first stale
// one.
func TestSurveyIn_StaleConvergedFrameRequiresConfirmation(t *testing.T) {
	cachedTMODE := make([]byte, 40)
	svinSequence := []SVINStatus{
		// First poll: stale "converged" frame from a prior session.
		{DurationSec: 9999, Observations: 99999, MeanAccMeters: 0.3, Active: false, Valid: true},
		// Real progress.
		{DurationSec: 1, Observations: 20, MeanAccMeters: 80, Active: true, Valid: false},
		{DurationSec: 2, Observations: 50, MeanAccMeters: 40, Active: true, Valid: false},
		// Real convergence (first occurrence).
		{DurationSec: 5, Observations: 200, MeanAccMeters: 1.5, Active: false, Valid: true},
		// Real convergence (second consecutive — confirms).
		{DurationSec: 6, Observations: 220, MeanAccMeters: 1.5, Active: false, Valid: true},
	}
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, svinSequence, scriptOpts{AckInitialTMODE: true})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	progress := []SVINStatus{}
	res, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 1,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
		MaxDuration:    2 * time.Second,
	}, func(_ time.Duration, st SVINStatus) {
		progress = append(progress, st)
	}, nil)
	if err != nil {
		t.Fatalf("SurveyIn: %v", err)
	}
	// We must have seen at least all 5 sequence entries — the loop
	// cannot have terminated on the first stale converged frame.
	if len(progress) < 5 {
		t.Errorf("loop terminated too early: %d progress entries (expected >=5)", len(progress))
	}
	// Final status should be the last entry, with duration 6.
	if res.Status.DurationSec != 6 {
		t.Errorf("loop terminated on wrong frame: final dur=%d (want 6)", res.Status.DurationSec)
	}
}

// TestSurveyIn_StaleVerifyPollRetried is the regression test for the
// fourth-pass finding: TMODE verify polls (the read-back inside
// WriteTMODE) suffer the same stale-response race as ACKs. A late TMODE
// poll response from a previously timed-out request can falsely satisfy a
// fresh verify poll, producing a spurious mismatch (when the receiver is
// actually in the right state) or — on rollback — a spurious success.
//
// Fix: WriteTMODE's verify is two-shot. On a first-poll mismatch, it
// drains the channel and polls a second time. Only a sustained mismatch
// across two consecutive polls counts as a real failure.
//
// This test scripts the survey-in convergence success path (so we'd
// otherwise expect persist-on-success), with one stale TMODE poll response
// queued — the FIRST verify poll receives the stale (wrong) payload, the
// retry receives the real (correct) payload. WriteTMODE must succeed.
func TestSurveyIn_StaleVerifyPollRetried(t *testing.T) {
	cachedTMODE := make([]byte, 40) // F9, all zeros = disabled
	stalePayload := make([]byte, 40)
	for i := range stalePayload {
		stalePayload[i] = 0xCC // deliberately wrong-looking bytes
	}
	svinSequence := []SVINStatus{
		{DurationSec: 5, Observations: 100, MeanAccMeters: 1.5, Active: false, Valid: true},
	}
	svinSequence = append(svinSequence, SVINStatus{DurationSec: 6, Observations: 110, MeanAccMeters: 1.5, Active: false, Valid: true})
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, svinSequence, scriptOpts{
		AckInitialTMODE:        true,
		StaleVerifyPollPayload: stalePayload,
		StaleVerifyPollCount:   1, // first verify poll returns stale; retry returns real
	})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	res, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 1,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
		MaxDuration:    1 * time.Second,
	}, nil, nil)
	if err != nil {
		t.Fatalf("SurveyIn should retry past the stale verify poll, got %v", err)
	}
	if res.RolledBack || res.RollbackFailed {
		t.Errorf("expected default success path with no rollback, got %+v", res)
	}
	if !res.Status.Valid {
		t.Errorf("expected valid convergence status, got %+v", res.Status)
	}
}

// TestSurveyIn_PostSendStaleAckInitialWriteVerifyCatches is the regression
// test for the third-pass finding: the same stale-ACK race that was fixed
// for rollback also applies to the INITIAL survey-in write. UBX has no
// nonce, so a bare ACK can never be trusted as proof of receiver state.
//
// The script ACKs the initial CFG-TMODE write but does not update the
// receiver's stored state. The verify-by-poll inside writeTMODEAndVerify
// must catch the mismatch and abort the survey before entering the SVIN
// poll loop. The rollback then runs and succeeds (the receiver was never
// actually modified, so restoring the cached state is a no-op).
func TestSurveyIn_PostSendStaleAckInitialWriteVerifyCatches(t *testing.T) {
	cachedTMODE := make([]byte, 40) // F9, all zeros = disabled
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, nil, scriptOpts{
		AckInitialTMODE:       true,
		DropInitialTMODEWrite: true, // ACK but leave receiver in cached state
	})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	res, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 1,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
		MaxDuration:    500 * time.Millisecond,
	}, nil, nil)
	if err == nil {
		t.Fatal("expected verify mismatch on initial TMODE write")
	}
	if !strings.Contains(err.Error(), "verify mismatch") {
		t.Errorf("expected 'verify mismatch' in error, got %q", err.Error())
	}
	// Receiver was never actually modified (drop), so the rollback's
	// re-write of the cached payload succeeds. RolledBack should be true,
	// RollbackFailed should be false.
	if !res.RolledBack || res.RollbackFailed {
		t.Errorf("expected RolledBack=true, RollbackFailed=false; got %+v", res)
	}
}

// TestSurveyIn_PostSendStaleAckRollbackVerifyCatches is the regression test
// for the second-pass code-review finding: drain-before-send only handles
// pre-buffered stale ACKs. A late ACK that arrives AFTER the rollback's
// SendUBX can still falsely satisfy the rollback's WaitFor.
//
// The fix is post-rollback verification — after the ACK, poll TMODE and
// bytes-compare against the cached payload. If they don't match, the ACK we
// got was almost certainly stale and the receiver is still in modified
// state.
//
// This test models that race by configuring the script to ACK the rollback
// write but NOT update the receiver's stored TMODE state. The verify-by-
// poll then sees the modified TMODE and SurveyIn must report rollback
// failure even though the ACK was received.
func TestSurveyIn_PostSendStaleAckRollbackVerifyCatches(t *testing.T) {
	cachedTMODE := make([]byte, 40) // F9 TMODE3, all zeros = disabled
	svinSequence := []SVINStatus{
		// Never converges → forces timeout → forces rollback.
		{DurationSec: 1, Observations: 10, MeanAccMeters: 50.0, Active: true},
	}
	c, dc, _ := startSurveyInScript(t, "/dev/ttyPS1", GenF9, cachedTMODE, svinSequence, scriptOpts{
		AckInitialTMODE:    true,
		DropRollbackWrites: true, // ACK rollback but leave receiver modified
	})
	_ = c

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := DetectGeneration(ctx, dc); err != nil {
		t.Fatal(err)
	}

	res, err := SurveyIn(ctx, dc, GenF9, SurveyInOptions{
		MinDurationSec: 1,
		AccuracyMeters: 2.0,
		PollInterval:   10 * time.Millisecond,
		MaxDuration:    150 * time.Millisecond,
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error: rollback verify must catch the receiver still being in modified state")
	}
	if !res.RollbackFailed {
		t.Errorf("expected RollbackFailed=true, got result %+v", res)
	}
	if res.RolledBack {
		t.Errorf("RolledBack should be false when verify fails, got %+v", res)
	}
	if !strings.Contains(err.Error(), "verify mismatch") && !strings.Contains(err.Error(), "rollback failed") {
		t.Errorf("expected rollback verify failure in error, got %q", err.Error())
	}
}
