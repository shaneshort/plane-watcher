# GPS ToA Timestamping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement PPS-disciplined GPS timestamping for MLAT-quality Beast output, as specified in `docs/plans/2026-03-28-gps-toa-timestamping.md`.

**Architecture:** Three new Go packages (`chrony`, `pps`, updated `beast`) plus FPGA/DT plumbing. A PPS watcher goroutine polls hardware registers at 2 Hz, queries chrony sync at 2s intervals, and publishes a `ClockRef` consumed by the Beast encoder. The encoder uses measured tick rate for nanosecond correction and gates the GPS sync bit on combined PPS health + chrony lock.

**Tech Stack:** Go 1.25, Verilog (top-level wrapper edits), Vivado Tcl (BD script), Linux device tree

---

## Scope Note

This plan covers **two independent subsystems** that can be developed in parallel:

1. **Software (Tasks 1-7):** Go changes to plane-feeder — can be developed and tested on the dev machine with mock readers, no hardware required.
2. **FPGA + Linux (Tasks 8-10):** Vivado BD script, `system_top.v`, device tree, boot config — requires Vivado and target hardware for validation.

Tasks 1-7 are TDD. Tasks 8-10 are configuration/plumbing verified on hardware.

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `ps/internal/regs/regs.go` | Add `ReadPpsStable()` |
| Modify | `ps/internal/regs/regs_test.go` | Test coherent PPS reads |
| Create | `ps/internal/chrony/chrony.go` | Parse `chronyc tracking` output for sync status |
| Create | `ps/internal/chrony/chrony_test.go` | Test parser against real chronyc output samples |
| Create | `ps/internal/pps/pps.go` | `ClockRef` type, `Watcher` goroutine, `Stats` |
| Create | `ps/internal/pps/pps_test.go` | Test watcher state machine with mock reader + mock sync |
| Modify | `ps/internal/beast/beast.go` | New `EncodeV2()` using `ClockRef`, measuredTicks correction, conditional sync bit |
| Modify | `ps/internal/beast/beast_test.go` | Tests for corrected nanos, conditional sync bit, degraded mode |
| Modify | `ps/internal/web/web.go` | Add GPS/clock fields to `StatsData` |
| Modify | `ps/internal/web/web_test.go` | Verify GPS fields in stats JSON and dashboard labels |
| Modify | `ps/internal/web/static/index.html` | Add GPS stats rows to dashboard |
| Modify | `ps/cmd/plane-feeder/main.go` | Wire watcher, remove inline PPS handling, pass `ClockRef` to encoder |
| Modify | `hdl/vivado/build_vendor.tcl` | Replace GND tie-off with external `pps_in` BD port |
| Modify | `hdl/vivado/system_top.v` | Add `pps_in` port, wire `gpio_i[17]` |
| Modify | `hdl/vivado/constr/plane_watcher_integration.xdc` | Add PPS pin constraint |
| Modify | `linux-dts/zynq-pluto-sdr-plane-watcher-hybrid.dtsi` | Add pps-gpio node, enable uart0 |

---

## Task 1: Coherent PPS Register Reads

**Files:**
- Modify: `ps/internal/regs/regs.go:255-262`
- Modify: `ps/internal/regs/regs_test.go`

The current `ReadPps()` does three separate MMIO reads with no atomicity. If a PPS edge lands mid-read, the software gets a torn snapshot. Fix with a count-stable retry loop.

- [ ] **Step 1: Write the failing test for ReadPpsStable**

Add to `ps/internal/regs/regs_test.go`:

```go
func TestReadPpsStableRetries(t *testing.T) {
	// Simulate a PPS edge landing between the first Count read and the
	// counter reads. The mock returns count=5 on first read, then
	// updated counter values, then count=6 on the verification read.
	// ReadPpsStable must retry and return the consistent count=6 snapshot.
	callNum := 0
	reads := []struct {
		offset uint32
		value  uint32
	}{
		// First attempt: count=5, lo=100M, hi=0, recheck count=6 → mismatch, retry
		{RegPpsCount, 5},
		{RegPpsCtrLo, 200_000_000}, // already updated by new edge
		{RegPpsCtrHi, 0},
		{RegPpsCount, 6}, // mismatch → retry
		// Second attempt: count=6, lo=200M, hi=0, recheck count=6 → match
		{RegPpsCount, 6},
		{RegPpsCtrLo, 200_000_000},
		{RegPpsCtrHi, 0},
		{RegPpsCount, 6},
	}

	reader := &sequenceReader{reads: reads, callNum: &callNum, t: t}
	pps := ReadPpsStable(reader)

	if pps.Count != 6 {
		t.Errorf("Count: got %d, want 6", pps.Count)
	}
	if pps.CounterLo != 200_000_000 {
		t.Errorf("CounterLo: got %d, want 200000000", pps.CounterLo)
	}
	if callNum != 8 {
		t.Errorf("expected 8 reads (1 retry), got %d", callNum)
	}
}

func TestReadPpsStableNoRetryNeeded(t *testing.T) {
	m := NewMockReader()
	m.SetPps(PpsState{Count: 10, CounterLo: 500_000_000, CounterHi: 1})
	pps := ReadPpsStable(m)

	if pps.Count != 10 {
		t.Errorf("Count: got %d, want 10", pps.Count)
	}
	if pps.CounterLo != 500_000_000 {
		t.Errorf("CounterLo: got %d, want 500000000", pps.CounterLo)
	}
	if pps.CounterHi != 1 {
		t.Errorf("CounterHi: got %d, want 1", pps.CounterHi)
	}
}

// sequenceReader returns predetermined values for a sequence of Read32 calls,
// validating that the expected register offset is read each time.
type sequenceReader struct {
	reads   []struct {
		offset uint32
		value  uint32
	}
	callNum *int
	t       *testing.T
}

func (s *sequenceReader) Read32(offset uint32) uint32 {
	idx := *s.callNum
	*s.callNum++
	if idx < len(s.reads) {
		if s.reads[idx].offset != offset {
			s.t.Errorf("Read32 call %d: got offset 0x%02X, want 0x%02X", idx, offset, s.reads[idx].offset)
		}
		return s.reads[idx].value
	}
	s.t.Fatalf("Read32 call %d: unexpected (past end of sequence)", idx)
	return 0
}

func (s *sequenceReader) Write32(offset uint32, value uint32) {}
func (s *sequenceReader) Close() error                        { return nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ps && go test ./internal/regs/ -run TestReadPpsStable -v`
Expected: FAIL — `ReadPpsStable` undefined

- [ ] **Step 3: Implement ReadPpsStable**

Add to `ps/internal/regs/regs.go` after the existing `ReadPps` function:

```go
// ReadPpsStable reads the PPS state with a count-stable retry loop.
// If a PPS edge lands between the individual register reads, the count
// will have changed and we retry. PPS fires once per second; MMIO reads
// take microseconds, so this virtually never loops more than once.
func ReadPpsStable(r RegisterReader) PpsState {
	for {
		c1 := r.Read32(RegPpsCount)
		lo := r.Read32(RegPpsCtrLo)
		hi := r.Read32(RegPpsCtrHi)
		c2 := r.Read32(RegPpsCount)
		if c1 == c2 {
			return PpsState{Count: c1, CounterLo: lo, CounterHi: hi}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ps && go test ./internal/regs/ -run TestReadPpsStable -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd ps && git add internal/regs/regs.go internal/regs/regs_test.go
git commit -m "feat(regs): add ReadPpsStable with count-stable retry loop

Prevents torn PPS snapshots when a PPS edge lands between the three
separate MMIO reads for count, counter_lo, counter_hi.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Chrony Sync Checker

**Files:**
- Create: `ps/internal/chrony/chrony.go`
- Create: `ps/internal/chrony/chrony_test.go`

Pure parser for `chronyc tracking` output. The exec wrapper is trivial; the parser is what we test.

- [ ] **Step 1: Write the failing test for the parser**

Create `ps/internal/chrony/chrony_test.go`:

```go
package chrony

import "testing"

func TestParseSynced(t *testing.T) {
	output := `Reference ID    : 50505300 (PPS)
Stratum         : 1
Ref time (UTC)  : Sat Mar 29 02:15:30 2026
System time     : 0.000000023 seconds fast of NTP time
Last offset     : -0.000000011 seconds
RMS offset      : 0.000000015 seconds
Frequency       : 14.703 ppm slow
Residual freq   : -0.000 ppm
Skew            : 0.003 ppm
Root delay      : 0.000000001 seconds
Root dispersion : 0.000012345 seconds
Update interval : 1.0 seconds
Leap status     : Normal`

	if !parseLeapSynced(output) {
		t.Error("expected synced for Leap status: Normal")
	}
}

func TestParseNotSynced(t *testing.T) {
	output := `Reference ID    : 00000000 ()
Stratum         : 0
Ref time (UTC)  : Thu Jan 01 00:00:00 1970
System time     : 0.000000000 seconds fast of NTP time
Last offset     : +0.000000000 seconds
RMS offset      : 0.000000000 seconds
Frequency       : 0.000 ppm slow
Residual freq   : +0.000 ppm
Skew            : 0.000 ppm
Root delay      : 0.000000001 seconds
Root dispersion : 0.000000001 seconds
Update interval : 0.0 seconds
Leap status     : Not synchronised`

	if parseLeapSynced(output) {
		t.Error("expected not synced for Leap status: Not synchronised")
	}
}

func TestParseInsertLeap(t *testing.T) {
	// During a leap second event, Leap status is "Insert second" — not Normal.
	// Timestamps may be ambiguous, so we treat this as not synced.
	output := `Leap status     : Insert second`
	if parseLeapSynced(output) {
		t.Error("expected not synced during leap second insertion")
	}
}

func TestParseEmptyOutput(t *testing.T) {
	if parseLeapSynced("") {
		t.Error("expected not synced for empty output")
	}
}

func TestParseGarbageOutput(t *testing.T) {
	if parseLeapSynced("connection refused\n") {
		t.Error("expected not synced for error output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ps && go test ./internal/chrony/ -v`
Expected: FAIL — package/function not found

- [ ] **Step 3: Implement the chrony package**

Create `ps/internal/chrony/chrony.go`:

```go
package chrony

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// parseLeapSynced returns true if the chronyc tracking output contains
// "Leap status     : Normal", indicating chrony is synchronized.
func parseLeapSynced(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Leap status") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]) == "Normal"
			}
		}
	}
	return false
}

// Checker queries chrony synchronization status by executing chronyc
// against the chronyd Unix socket (as specified in the design doc).
type Checker struct {
	bin     string
	sock    string // path to chronyd control socket
	timeout time.Duration
}

// NewChecker creates a Checker.
// bin defaults to "chronyc"; sock defaults to "/var/run/chrony/chronyd.sock".
func NewChecker(bin, sock string) *Checker {
	if bin == "" {
		bin = "chronyc"
	}
	if sock == "" {
		sock = "/var/run/chrony/chronyd.sock"
	}
	return &Checker{bin: bin, sock: sock, timeout: 2 * time.Second}
}

// IsSynced returns true if chrony reports Leap status: Normal.
// Returns false on any error (chrony not running, timeout, parse failure).
// Uses a 2-second exec timeout to prevent stalling the watcher.
// Connects via the Unix socket (-h flag) per the design doc requirement.
func (c *Checker) IsSynced() bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin, "-h", c.sock, "tracking").Output()
	if err != nil {
		return false
	}
	return parseLeapSynced(string(out))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ps && go test ./internal/chrony/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd ps && git add internal/chrony/
git commit -m "feat(chrony): add sync checker parsing chronyc tracking output

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: PPS Watcher — ClockRef Type and Computation

**Files:**
- Create: `ps/internal/pps/pps.go`
- Create: `ps/internal/pps/pps_test.go`

The watcher is split into two tasks: this one covers the core types and the pure state-transition function (testable without goroutines). Task 4 covers the goroutine loop.

- [ ] **Step 1: Write tests for ClockRef computation**

Create `ps/internal/pps/pps_test.go`:

```go
package pps

import (
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

func TestUpdateNoPreviousPps(t *testing.T) {
	// First PPS seen — no previous state, can't compute ticks yet.
	w := newWatcherState()
	pps := regs.PpsState{Count: 1, CounterLo: 100_000_000, CounterHi: 0}
	wall := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)

	ref := w.update(pps, wall, true)

	if ref.GpsSync {
		t.Error("GpsSync should be false with only 1 PPS edge")
	}
	if ref.MeasuredTicks != 0 {
		t.Error("MeasuredTicks should be 0 with no previous PPS")
	}
	if ref.Count != 1 {
		t.Errorf("Count: got %d, want 1", ref.Count)
	}
}

func TestUpdateNominalPps(t *testing.T) {
	// Two consecutive PPS edges, exactly 100M ticks apart, chrony synced.
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 200_000_000}, wall2, true)

	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000", ref.MeasuredTicks)
	}
	if ref.Carryover != 0 {
		t.Errorf("Carryover: got %d, want 0", ref.Carryover)
	}
	if !ref.GpsSync {
		t.Error("GpsSync should be true")
	}
	if ref.Degraded {
		t.Error("should not be degraded")
	}
}

func TestUpdateDriftingOscillator(t *testing.T) {
	// -15 ppm: 99,998,500 ticks per second.
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 199_998_500}, wall2, true)

	if ref.MeasuredTicks != 99_998_500 {
		t.Errorf("MeasuredTicks: got %d, want 99998500", ref.MeasuredTicks)
	}
	if ref.Carryover != -1500 {
		t.Errorf("Carryover: got %d, want -1500", ref.Carryover)
	}
	if !ref.GpsSync {
		t.Error("GpsSync should be true (-1500 is within ±10000)")
	}
}

func TestUpdateSkippedEdges(t *testing.T) {
	// PPS count jumps from 1 to 4 (3 edges skipped due to slow polling).
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 4, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 4, CounterLo: 400_000_000}, wall2, true)

	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000 (averaged over 3 intervals)", ref.MeasuredTicks)
	}
	if !ref.Degraded {
		t.Error("should be degraded (skipped edges)")
	}
	if ref.GpsSync {
		t.Error("GpsSync should be false (skipped edges)")
	}
}

func TestUpdateChronyNotSynced(t *testing.T) {
	// PPS is healthy but chrony is not synced → no GPS sync.
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, false)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 200_000_000}, wall2, false)

	if ref.GpsSync {
		t.Error("GpsSync should be false when chrony is not synced")
	}
	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks should still be computed: got %d", ref.MeasuredTicks)
	}
}

func TestUpdateInsaneCarryover(t *testing.T) {
	// Carryover exceeds ±10000 threshold → not synced.
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	// 15000 ticks off → 150 ppm, over the 100 ppm threshold
	ref := w.update(regs.PpsState{Count: 2, CounterLo: 200_015_000}, wall2, true)

	if ref.GpsSync {
		t.Error("GpsSync should be false when |carryover| > 10000")
	}
}

func TestUpdateNoPpsChange(t *testing.T) {
	// Same PPS count on consecutive polls — no update, return previous ref.
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	ref1 := w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 1, 500_000_000, time.UTC)
	ref2 := w.update(regs.PpsState{Count: 1, CounterLo: 100_000_000}, wall2, true)

	if ref2.Count != ref1.Count {
		t.Error("ref should not change when PPS count is unchanged")
	}
}

func TestUpdateCounterWraps64Bit(t *testing.T) {
	// Counter values use full 64 bits via CounterHi.
	w := newWatcherState()
	wall1 := time.Date(2026, 3, 29, 12, 0, 1, 0, time.UTC)
	w.update(regs.PpsState{Count: 1, CounterLo: 0xFFFFFF00, CounterHi: 2}, wall1, true)

	wall2 := time.Date(2026, 3, 29, 12, 0, 2, 0, time.UTC)
	// Wraps: new 64-bit value = 3<<32 + 0x05F5E000, old = 2<<32 + 0xFFFFFF00
	// Delta should be 100_000_000 ticks
	newLo := uint32(0xFFFFFF00 + 100_000_000) // wraps: 0x05F5FE00 + carry
	newHi := uint32(3)
	ref := w.update(regs.PpsState{Count: 2, CounterLo: newLo, CounterHi: newHi}, wall2, true)

	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000", ref.MeasuredTicks)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ps && go test ./internal/pps/ -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement the pps package types and state logic**

Create `ps/internal/pps/pps.go`:

```go
package pps

import (
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

const (
	nominalTicksPerSec = 100_000_000
	maxCarryover       = 10_000 // ~100 ppm
)

// ClockRef is the PPS-disciplined clock reference consumed by the Beast encoder.
type ClockRef struct {
	Count         uint32    // PPS count at which this ref was computed
	CounterAtPps  uint64    // 64-bit FPGA counter at last PPS edge
	MeasuredTicks uint64    // ticks in last PPS interval (0 if unknown)
	Carryover     int64     // measuredTicks - nominalTicksPerSec
	WallUTC       time.Time // system clock when we noticed this PPS edge
	GpsSync       bool      // all sync conditions met
	Degraded      bool      // true if skipped edges (averaged ticks)
}

// Stats holds rolling statistics for dashboard display.
type Stats struct {
	GpsSync        bool    `json:"gps_sync"`
	OscillatorPPM  float64 `json:"oscillator_ppm"`
	Carryover      int64   `json:"carryover"`
	AvgCarryover   float64 `json:"avg_carryover"`
	CarryoverMin   int64   `json:"carryover_min"`
	CarryoverMax   int64   `json:"carryover_max"`
	MeasuredTicks  uint64  `json:"pps_interval_ticks"`
	SkippedEdges   uint64  `json:"skipped_edges"`
}

const rollingWindowSize = 60

// watcherState holds the internal state for PPS update computation.
// Separated from the goroutine for testability.
type watcherState struct {
	prev            regs.PpsState
	prevWall        time.Time
	lastPpsAdvance  time.Time
	hasPrev         bool
	ref             ClockRef

	carryoverMin    int64
	carryoverMax    int64
	skippedEdges    uint64
	carryoverWindow [rollingWindowSize]int64
	carryoverSum    int64
	windowIdx       int
	windowLen       int
}

func newWatcherState() *watcherState {
	return &watcherState{}
}

// update processes a new PPS register snapshot and returns the updated ClockRef.
// chronySynced indicates whether chrony reports synchronized status.
func (w *watcherState) update(pps regs.PpsState, wall time.Time, chronySynced bool) ClockRef {
	counter := pps.CounterAtPps()

	if !w.hasPrev {
		w.prev = pps
		w.prevWall = wall
		w.lastPpsAdvance = wall
		w.hasPrev = true
		w.ref = ClockRef{
			Count:        pps.Count,
			CounterAtPps: counter,
			WallUTC:      wall,
		}
		return w.ref
	}

	if pps.Count == w.prev.Count {
		// PPS hasn't advanced. Re-evaluate sync: PPS may be stale.
		ppsStale := wall.Sub(w.lastPpsAdvance) > 2*time.Second
		w.ref.GpsSync = w.ref.GpsSync && !ppsStale && chronySynced
		return w.ref
	}

	w.lastPpsAdvance = wall
	prevCounter := w.prev.CounterAtPps()
	delta := uint32(pps.Count - w.prev.Count)
	totalTicks := counter - prevCounter

	var measuredTicks uint64
	var degraded bool

	if delta == 1 {
		measuredTicks = totalTicks
	} else {
		measuredTicks = totalTicks / uint64(delta)
		degraded = true
		w.skippedEdges += uint64(delta - 1)
	}

	carryover := int64(measuredTicks) - nominalTicksPerSec

	if w.windowLen == 0 {
		w.carryoverMin = carryover
		w.carryoverMax = carryover
	}
	if carryover < w.carryoverMin {
		w.carryoverMin = carryover
	}
	if carryover > w.carryoverMax {
		w.carryoverMax = carryover
	}

	// Rolling window: track last 60 samples for smoothed PPM.
	if w.windowLen < rollingWindowSize {
		w.carryoverWindow[w.windowLen] = carryover
		w.windowLen++
	} else {
		w.carryoverSum -= w.carryoverWindow[w.windowIdx]
		w.carryoverWindow[w.windowIdx] = carryover
		w.windowIdx = (w.windowIdx + 1) % rollingWindowSize
	}
	w.carryoverSum += carryover

	saneCarryover := carryover >= -maxCarryover && carryover <= maxCarryover
	gpsSync := !degraded && saneCarryover && chronySynced

	w.prev = pps
	w.prevWall = wall
	w.ref = ClockRef{
		Count:         pps.Count,
		CounterAtPps:  counter,
		MeasuredTicks: measuredTicks,
		Carryover:     carryover,
		WallUTC:       wall,
		GpsSync:       gpsSync,
		Degraded:      degraded,
	}
	return w.ref
}

// stats returns the current rolling statistics.
func (w *watcherState) stats() Stats {
	var ppm, avgCO float64
	if w.windowLen > 0 {
		avgCO = float64(w.carryoverSum) / float64(w.windowLen)
		ppm = avgCO / float64(nominalTicksPerSec) * 1e6
	}
	return Stats{
		GpsSync:        w.ref.GpsSync,
		OscillatorPPM:  ppm,
		Carryover:      w.ref.Carryover,
		AvgCarryover:   avgCO,
		CarryoverMin:   w.carryoverMin,
		CarryoverMax:   w.carryoverMax,
		MeasuredTicks:  w.ref.MeasuredTicks,
		SkippedEdges:   w.skippedEdges,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ps && go test ./internal/pps/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd ps && git add internal/pps/
git commit -m "feat(pps): add ClockRef type and state-transition logic

Pure computation for PPS carryover tracking, skipped-edge detection,
and GPS sync gating. Goroutine wiring follows in a separate commit.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: PPS Watcher Goroutine

**Files:**
- Modify: `ps/internal/pps/pps.go`
- Modify: `ps/internal/pps/pps_test.go`

Add the `Watcher` struct with `Start()`/`Stop()` and atomic `ClockRef` publication.

- [ ] **Step 1: Write test for watcher goroutine lifecycle**

Add to `ps/internal/pps/pps_test.go`:

```go
func TestWatcherPublishesRef(t *testing.T) {
	mock := regs.NewMockReader()
	mock.SetPps(regs.PpsState{Count: 0})

	syncer := &mockSyncer{synced: true}
	w := NewWatcher(mock, syncer)
	w.Start()
	defer w.Stop()

	// Simulate first PPS edge.
	mock.SetPps(regs.PpsState{Count: 1, CounterLo: 100_000_000})
	time.Sleep(600 * time.Millisecond) // watcher ticks at 500ms

	ref := w.Ref()
	if ref == nil {
		t.Fatal("expected non-nil ClockRef after first PPS")
	}
	if ref.Count != 1 {
		t.Errorf("Count: got %d, want 1", ref.Count)
	}

	// Simulate second PPS edge.
	mock.SetPps(regs.PpsState{Count: 2, CounterLo: 200_000_000})
	time.Sleep(600 * time.Millisecond)

	ref = w.Ref()
	if ref.MeasuredTicks != 100_000_000 {
		t.Errorf("MeasuredTicks: got %d, want 100000000", ref.MeasuredTicks)
	}
	if !ref.GpsSync {
		t.Error("GpsSync should be true")
	}
}

func TestWatcherChronyExpiry(t *testing.T) {
	mock := regs.NewMockReader()
	syncer := &mockSyncer{synced: true}
	w := NewWatcher(mock, syncer)
	w.Start()
	defer w.Stop()

	// Advance PPS normally to reach synced state.
	mock.SetPps(regs.PpsState{Count: 1, CounterLo: 100_000_000})
	time.Sleep(600 * time.Millisecond)
	mock.SetPps(regs.PpsState{Count: 2, CounterLo: 200_000_000})
	time.Sleep(600 * time.Millisecond)
	mock.SetPps(regs.PpsState{Count: 3, CounterLo: 300_000_000})
	time.Sleep(600 * time.Millisecond)

	ref := w.Ref()
	if !ref.GpsSync {
		t.Fatal("expected GpsSync=true before chrony loss")
	}

	// Chrony loses sync. Keep advancing PPS by exactly 1 each second
	// so the degraded path is never triggered — this isolates the
	// chrony expiry logic from the skipped-edge logic.
	syncer.synced = false

	// Advance PPS one-by-one through the 4-second expiry window.
	for i := uint32(4); i <= 12; i++ {
		mock.SetPps(regs.PpsState{Count: i, CounterLo: i * 100_000_000})
		time.Sleep(600 * time.Millisecond)
	}

	ref = w.Ref()
	if ref.Degraded {
		t.Fatal("test bug: PPS should not be degraded (delta==1 throughout)")
	}
	if ref.GpsSync {
		t.Error("GpsSync should be false after chrony expiry (PPS healthy, chrony lost)")
	}
}

type mockSyncer struct {
	synced bool
}

func (m *mockSyncer) IsSynced() bool {
	return m.synced
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ps && go test ./internal/pps/ -run TestWatcher -v -timeout 30s`
Expected: FAIL — `NewWatcher` undefined

- [ ] **Step 3: Implement the Watcher goroutine**

Add to `ps/internal/pps/pps.go`:

```go
import (
	"log"
	"sync/atomic"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/regs"
)

// SyncChecker reports whether the system clock is synchronized to UTC.
type SyncChecker interface {
	IsSynced() bool
}

// Watcher polls PPS registers and chrony status, publishing a ClockRef.
type Watcher struct {
	reader regs.RegisterReader
	sync   SyncChecker
	ref    atomic.Pointer[ClockRef]
	st     atomic.Pointer[Stats]
	stopCh chan struct{}
}

// NewWatcher creates a Watcher. Call Start() to begin polling.
func NewWatcher(reader regs.RegisterReader, sync SyncChecker) *Watcher {
	return &Watcher{
		reader: reader,
		sync:   sync,
		stopCh: make(chan struct{}),
	}
}

// Ref returns the latest ClockRef, or nil if no PPS has been seen.
func (w *Watcher) Ref() *ClockRef {
	return w.ref.Load()
}

// Stats returns the latest rolling statistics.
func (w *Watcher) Stats() Stats {
	if s := w.st.Load(); s != nil {
		return *s
	}
	return Stats{}
}

const (
	pollInterval   = 500 * time.Millisecond
	chronyInterval = 4 // check chrony every 4th tick (2 seconds)
	chronyExpiry   = 4 * time.Second
)

// Start begins the PPS polling goroutine.
func (w *Watcher) Start() {
	go w.run()
}

// Stop signals the watcher goroutine to exit.
func (w *Watcher) Stop() {
	close(w.stopCh)
}

func (w *Watcher) run() {
	state := newWatcherState()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var tickCount int
	var chronySynced bool
	var lastChronyOK time.Time

	for {
		select {
		case <-w.stopCh:
			return
		case now := <-ticker.C:
			// Check chrony every 4th tick (2 seconds).
			tickCount++
			if tickCount%chronyInterval == 0 {
				if w.sync.IsSynced() {
					chronySynced = true
					lastChronyOK = now
				}
			}

			// Expire chrony status after 4 seconds with no confirmation.
			if chronySynced && now.Sub(lastChronyOK) > chronyExpiry {
				chronySynced = false
				log.Printf("pps: chrony sync expired (no confirmation for %v)", now.Sub(lastChronyOK))
			}

			pps := regs.ReadPpsStable(w.reader)
			ref := state.update(pps, now, chronySynced)
			w.ref.Store(&ref)
			st := state.stats()
			w.st.Store(&st)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ps && go test ./internal/pps/ -v -timeout 30s`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd ps && git add internal/pps/pps.go internal/pps/pps_test.go
git commit -m "feat(pps): add Watcher goroutine with chrony expiry

Polls PPS registers at 2 Hz, queries chrony sync every 2s with 4s
expiry. Publishes ClockRef via atomic pointer for the FIFO loop.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Updated Beast Encoder

**Files:**
- Modify: `ps/internal/beast/beast.go`
- Modify: `ps/internal/beast/beast_test.go`
- Modify: `ps/internal/beast/golden_test.go`

Add `EncodeV2()` that accepts a `*pps.ClockRef` instead of the old `(regs.PpsState, *PpsTimeRef)` tuple. Uses `measuredTicks` for corrected nanos. Gates GPS sync bit on `ClockRef.GpsSync`. Keeps existing `Encode()` unchanged until main.go is updated (Task 7), then removes it.

- [ ] **Step 1: Write tests for EncodeV2 with corrected nanos**

Add to `ps/internal/beast/beast_test.go`:

```go
import (
	"github.com/plane-watcher/plane-feeder/internal/pps"
)

func TestEncodeV2CorrectedNanos(t *testing.T) {
	// Oscillator at -15 ppm: measuredTicks = 99,998,500.
	// Message 50,000,000 ticks after PPS (half of NOMINAL 100M).
	// Uncorrected nanos = 50M * 1e9 / 100M       = 500,000,000
	// Corrected nanos   = 50M * 1e9 / 99,998,500  = 500,007,500
	// The correction reveals the 7.5 us systematic error.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 99_998_500,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 100_000_000 + 50_000_000}

	frame := EncodeV2(msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	nanos := ts & 0x3FFFFFFF
	seconds := (ts >> 30) & 0x1FFFF
	gpsSync := (ts >> 47) & 1

	if gpsSync != 1 {
		t.Error("GPS sync bit not set")
	}
	if seconds != 3605 {
		t.Errorf("seconds: got %d, want 3605", seconds)
	}
	// 50,000,000 * 1,000,000,000 / 99,998,500 = 500,007,500 (integer division)
	if nanos != 500_007_500 {
		t.Errorf("nanos: got %d, want 500007500", nanos)
	}
}

func TestEncodeV2NotSyncedFallsBackToStandard(t *testing.T) {
	// PPS is present but GpsSync is false → standard 12 MHz fallback.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       false,
	}
	msg := regs.Message{Len: 14, TOA: 100_000_000}

	frame := EncodeV2(msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	// Should be standard 12 MHz: floor(100M * 12 / 100) = 12,000,000
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000 (standard fallback)", ts)
	}
}

func TestEncodeV2NilRefFallback(t *testing.T) {
	// No PPS reference → standard Beast timestamp.
	msg := regs.Message{Len: 14, TOA: 100_000_000}
	frame := EncodeV2(msg, nil)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 12_000_000 {
		t.Errorf("fallback timestamp: got %d, want 12000000", ts)
	}
}

func TestEncodeV2NoSyncFallsBackToStandard(t *testing.T) {
	// First PPS edge, no delta, GpsSync=false → standard 12 MHz fallback.
	ref := &pps.ClockRef{
		Count:         1,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 0,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 1, 0, time.UTC),
		GpsSync:       false,
	}
	msg := regs.Message{Len: 14, TOA: 100_000_000}

	frame := EncodeV2(msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000 (standard fallback)", ts)
	}
}

func TestEncodeV2PpsBoundaryCrossing(t *testing.T) {
	// Message TOA < counterAtPps → belongs to previous second.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  200_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 200_000_000 - 1_000_000}

	frame := EncodeV2(msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF

	// Should roll back one second: 3605 - 1 = 3604
	if seconds != 3604 {
		t.Errorf("seconds: got %d, want 3604", seconds)
	}
	// 1M ticks before edge → 990,000,000 ns with nominal rate
	if nanos != 990_000_000 {
		t.Errorf("nanos: got %d, want 990000000", nanos)
	}
}

func TestEncodeV2MidnightRollover(t *testing.T) {
	// PPS at 23:59:59, message after PPS → seconds = 86399.
	ref := &pps.ClockRef{
		Count:         100,
		CounterAtPps:  1_000_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 1_050_000_000}

	frame := EncodeV2(msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	if seconds != 86399 {
		t.Errorf("seconds: got %d, want 86399", seconds)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ps && go test ./internal/beast/ -run TestEncodeV2 -v`
Expected: FAIL — `EncodeV2` undefined

- [ ] **Step 3: Implement EncodeV2**

Add to `ps/internal/beast/beast.go`:

```go
import (
	"github.com/plane-watcher/plane-feeder/internal/pps"
)

// EncodeV2 produces a Beast binary frame using a PPS-disciplined ClockRef.
// When ref is non-nil with GpsSync true, encodes Radarcape-format timestamps
// with measured tick rate correction and GPS sync bit set. Falls back to
// standard 12 MHz timestamps when ref is nil or GpsSync is false.
func EncodeV2(msg regs.Message, ref *pps.ClockRef) []byte {
	var ts48 uint64
	if ref != nil && ref.GpsSync {
		secOfDay := uint64(ref.WallUTC.UTC().Hour())*3600 +
			uint64(ref.WallUTC.UTC().Minute())*60 +
			uint64(ref.WallUTC.UTC().Second())

		counterAtPps := ref.CounterAtPps
		tickRate := ref.MeasuredTicks
		if tickRate == 0 {
			tickRate = fpgaCounterHz
		}

		var nanos uint64
		if msg.TOA >= counterAtPps {
			nanos = (msg.TOA - counterAtPps) * 1_000_000_000 / tickRate
		} else {
			secOfDay = (secOfDay + 86400 - 1) % 86400
			ticksInPrevSec := counterAtPps - msg.TOA
			nanos = 1_000_000_000 - ticksInPrevSec*1_000_000_000/tickRate
		}

		ts48 = ((secOfDay % 86400) << 30) | (nanos & 0x3FFFFFFF)
		if ref.GpsSync {
			ts48 |= 1 << 47
		}
	} else {
		q := msg.TOA / 25
		r := msg.TOA % 25
		ts48 = (q*3 + (r*3)/25) & 0xFFFFFFFFFFFF
	}

	signal := regs.RplToSignalByte(msg.RPL)

	msgType := TypeLong
	if msg.Len == 7 {
		msgType = TypeShort
	}

	payload := make([]byte, 0, 1+6+1+msg.Len)
	payload = append(payload, byte(msgType))
	for i := 5; i >= 0; i-- {
		payload = append(payload, byte(ts48>>(i*8)))
	}
	payload = append(payload, signal)
	payload = append(payload, msg.Bytes[:msg.Len]...)

	frame := make([]byte, 0, 1+len(payload)*2)
	frame = append(frame, Escape)
	for _, b := range payload {
		frame = append(frame, b)
		if b == Escape {
			frame = append(frame, Escape)
		}
	}

	return frame
}
```

- [ ] **Step 4: Run all beast tests to verify pass**

Run: `cd ps && go test ./internal/beast/ -v`
Expected: all PASS (existing tests untouched, new EncodeV2 tests pass)

- [ ] **Step 5: Commit**

```bash
cd ps && git add internal/beast/beast.go internal/beast/beast_test.go
git commit -m "feat(beast): add EncodeV2 with measured tick rate and conditional sync bit

Uses ClockRef.MeasuredTicks for corrected nanosecond computation and
gates GPS sync bit on ClockRef.GpsSync. Falls back to nominal 100 MHz
when measuredTicks is unknown. Old Encode() remains for now.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Web Stats — GPS/Clock Fields

**Files:**
- Modify: `ps/internal/web/web.go`
- Modify: `ps/internal/web/web_test.go`
- Modify: `ps/internal/web/static/index.html`

Add GPS/clock fields to `StatsData` so the dashboard can display sync status and oscillator health.

- [ ] **Step 1: Add GPS fields to StatsData**

In `ps/internal/web/web.go`, add to the `StatsData` struct (after the `Overflow` field):

```go
	GpsSync        bool    `json:"gps_sync"`
	OscillatorPPM  float64 `json:"oscillator_ppm"`
	Carryover      int64   `json:"carryover"`
	AvgCarryover   float64 `json:"avg_carryover"`
	CarryoverMin   int64   `json:"carryover_min"`
	CarryoverMax   int64   `json:"carryover_max"`
	PpsTickRate    uint64  `json:"pps_interval_ticks"`
	SkippedEdges   uint64  `json:"skipped_edges"`
```

- [ ] **Step 2: Add GPS stats to dashboard HTML**

In `ps/internal/web/static/index.html`, add rows to the stats table for the new GPS fields (after the existing PPS count row). Read the file first to find the exact insertion point. Add rows for:
- GPS Sync (boolean, green/red indicator)
- Oscillator PPM
- Carryover (current / avg / min / max)
- PPS Tick Rate
- Skipped Edges

These should only render when `pps_interval_ticks > 0` (meaning PPS is active). `gps_sync` is always present in the JSON (non-omitempty bool), so it cannot be used as a visibility gate.

- [ ] **Step 3: Add explicit web tests for the new GPS fields**

In `ps/internal/web/web_test.go`:
- Extend `mockStats.Stats()` to return non-zero GPS values so the JSON and dashboard code paths are exercised.
- Add `TestStatsEndpointIncludesGPSFields` to assert the `/api/stats` response includes the new fields with the expected values:
  `gps_sync`, `oscillator_ppm`, `carryover`, `avg_carryover`, `carryover_min`, `carryover_max`, `pps_interval_ticks`, `skipped_edges`.
- Strengthen `TestDashboardServed` (or add a new test) to assert the served HTML contains the new dashboard labels:
  `GPS Sync`, `Oscillator PPM`, `Carryover`, `PPS Tick Rate`, `Skipped Edges`.

- [ ] **Step 4: Run web tests**

Run: `cd ps && go test ./internal/web/ -v`
Expected: PASS

- [ ] **Step 5: Optional manual smoke check**

Run plane-feeder locally, open `/`, and verify:
- GPS rows are hidden when `pps_interval_ticks == 0`
- GPS rows appear when PPS stats are populated
- GPS Sync renders with the intended green/red indicator

- [ ] **Step 6: Commit**

```bash
cd ps && git add internal/web/web.go internal/web/web_test.go internal/web/static/index.html
git commit -m "feat(web): add GPS clock stats to StatsData and dashboard

New fields: gps_sync, oscillator_ppm, carryover, avg_carryover,
carryover_min/max, pps_interval_ticks, skipped_edges. Dashboard
renders GPS stats when PPS data is available and adds coverage for
the new API/dashboard surface.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Wire Into plane-feeder main.go

**Files:**
- Modify: `ps/cmd/plane-feeder/main.go`

Replace the inline PPS handling with the Watcher goroutine. Switch from `beast.Encode()` to `beast.EncodeV2()`. Surface watcher stats in the web handler. Then remove the old `Encode()` and `PpsTimeRef`.

- [ ] **Step 1: Add chrony and watcher imports, create watcher**

In `main.go`, update the import block to add:

```go
	"github.com/plane-watcher/plane-feeder/internal/chrony"
	"github.com/plane-watcher/plane-feeder/internal/pps"
```

After the reset and enable section (after "--- 4. Reset and enable decoder ---", before "--- 5. Beast TCP server ---"), add:

```go
	// --- 3b. PPS watcher ---
	var ppsWatcher *pps.Watcher
	if !*mock {
		chronyChecker := chrony.NewChecker("", "")
		ppsWatcher = pps.NewWatcher(reader, chronyChecker)
		ppsWatcher.Start()
		defer ppsWatcher.Stop()
		log.Printf("PPS watcher started (polling at 2 Hz, chrony check every 2s)")
	}
```

- [ ] **Step 2: Update statsSource to include PPS watcher**

Add a `ppsWatcher *pps.Watcher` field to the `statsSource` struct:

```go
type statsSource struct {
	startTime   time.Time
	reader      regs.RegisterReader
	deepDebug   bool
	filter      *icao.Filter
	beastSrv    *server.Server
	rd          *radio.Radio
	ppsWatcher  *pps.Watcher
	msgCount    *atomic.Uint64
	dropCount   *atomic.Uint64
	msgRate     *atomic.Int64
	crcPassRate *atomic.Int64
}
```

Update the `Stats()` method to populate GPS fields:

```go
func (s *statsSource) Stats(debug bool) web.StatsData {
	status := s.reader.Read32(regs.RegStatus)
	ppsReg := regs.ReadPps(s.reader)

	d := web.StatsData{
		Uptime:      int64(time.Since(s.startTime).Seconds()),
		MsgCount:    s.msgCount.Load(),
		MsgRate:     float64(s.msgRate.Load()) / 10.0,
		CrcPassRate: float64(s.crcPassRate.Load()) / 10.0,
		DropCount:   s.dropCount.Load(),
		ICAOCount:   s.filter.Count(),
		ClientCount: s.beastSrv.ClientCount(),
		PPSCount:    ppsReg.Count,
		Overflow:    status&regs.StatusOverflow != 0,
	}

	if s.ppsWatcher != nil {
		ps := s.ppsWatcher.Stats()
		d.GpsSync = ps.GpsSync
		d.OscillatorPPM = ps.OscillatorPPM
		d.Carryover = ps.Carryover
		d.AvgCarryover = ps.AvgCarryover
		d.CarryoverMin = ps.CarryoverMin
		d.CarryoverMax = ps.CarryoverMax
		d.PpsTickRate = ps.MeasuredTicks
		d.SkippedEdges = ps.SkippedEdges
	}

	if s.rd != nil {
		d.Radio = s.rd.ReadStatus()
	}

	if debug {
		d.Debug = readDebugCounters(s.reader, s.deepDebug)
	}

	return d
}
```

Set `ppsWatcher` in the statsSource initialization:

```go
	ss := &statsSource{
		startTime:   time.Now(),
		reader:      reader,
		deepDebug:   deepDebug,
		filter:      filter,
		beastSrv:    beastSrv,
		rd:          rd,
		ppsWatcher:  ppsWatcher,
		msgCount:    &msgCount,
		dropCount:   &dropCount,
		msgRate:     &msgRate,
		crcPassRate: &crcPassRate,
	}
```

- [ ] **Step 3: Replace inline PPS handling with watcher ref in the FIFO loop**

Remove the `ppsRef` variable from the declarations:

```go
	var (
		lastStats       time.Time
		lastMsgSnap     uint64
		lastCrcPassSnap uint32
		// ppsRef removed — now comes from ppsWatcher
	)
```

Remove the stats-tick PPS read (the watcher handles this now). The `ppsSnap` in the stats log line should use the register read already there.

Replace the burst-level PPS block:

```go
		// Read PPS once per burst, not per message.
		pps := regs.ReadPps(reader)
		if pps.Count != ppsRef.Count && pps.Count > 0 {
			ppsRef = beast.PpsTimeRef{
				Count:   pps.Count,
				WallUTC: time.Now(),
			}
		}
```

With:

```go
		// Get latest PPS-disciplined clock reference from watcher.
		var clockRef *pps.ClockRef
		if ppsWatcher != nil {
			clockRef = ppsWatcher.Ref()
		}
```

Replace the `beast.Encode` call:

```go
				frame := beast.Encode(msg, pps, *radarcape, &ppsRef)
```

With:

```go
				var frame []byte
				if *radarcape {
					frame = beast.EncodeV2(msg, clockRef)
				} else {
					frame = beast.EncodeV2(msg, nil)
				}
```

- [ ] **Step 4: Build to verify compilation**

Run: `cd ps && go build ./cmd/plane-feeder/`
Expected: compiles without errors

- [ ] **Step 5: Run all tests**

Run: `cd ps && go test ./... -timeout 60s`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
cd ps && git add cmd/plane-feeder/main.go
git commit -m "feat(plane-feeder): wire PPS watcher and EncodeV2 into main loop

Replaces inline PPS handling with dedicated watcher goroutine.
Uses EncodeV2 for measured-tick-rate correction and conditional
GPS sync bit. Surfaces GPS/clock stats in the web dashboard.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 7: Remove old Encode() and PpsTimeRef**

Delete the `PpsTimeRef` struct and `Encode()` function from `ps/internal/beast/beast.go`. Remove the old tests that referenced them from `beast_test.go` (all the `TestRadarcape*` tests that use `PpsTimeRef`). The golden tests in `golden_test.go` that call `Encode()` with standard mode need to be updated to use `EncodeV2(msg, nil)`.

In `ps/internal/beast/beast.go`, remove:
- The `PpsTimeRef` struct (lines 23-26)
- The `Encode` function (lines 39-109)

In `ps/internal/beast/beast_test.go`, remove:
- `TestRadarcapeTimestamp`
- `TestRadarcapeWithElapsedPps`
- `TestRadarcapePpsBoundaryCrossing`
- `TestRadarcapeNoPpsFallsBackToStandard`

Update the remaining tests that called `Encode()`:
- `TestEncodeLongMessage`: change `Encode(msg, pps, false, nil)` → `EncodeV2(msg, nil)`
- `TestEncodeShortMessage`: same
- `TestEscaping`: same
- `TestStandardTimestamp`: same

In `ps/internal/beast/golden_test.go`:
- `TestGoldenLongDF17`: change `beast.Encode(msg, regs.PpsState{}, false, nil)` → `beast.EncodeV2(msg, nil)`
- `TestGoldenShortDF0`: same
- `TestGoldenEscaping`: same

In `ps/cmd/replay/golden_test.go`:
- `TestGoldenSimLogReplay`: change `beast.Encode(msg, regs.PpsState{}, false, nil)` → `beast.EncodeV2(msg, nil)`

In `ps/cmd/replay/main.go`:
- Change `beast.Encode(e.msg, e.pps, false, nil)` → `beast.EncodeV2(e.msg, nil)`

In `ps/internal/server/e2e_test.go`:
- Change `beast.Encode(msg, pps, false, nil)` → `beast.EncodeV2(msg, nil)`

- [ ] **Step 8: Run all tests to verify cleanup is clean**

Run: `cd ps && go test ./... -timeout 60s`
Expected: all PASS

- [ ] **Step 9: Commit**

```bash
cd ps && git add internal/beast/ internal/server/e2e_test.go cmd/replay/golden_test.go cmd/replay/main.go
git commit -m "refactor(beast): remove old Encode() and PpsTimeRef

EncodeV2 is now the sole encoder. All call sites and tests migrated.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: FPGA — Export pps_in in Hybrid Build

**Files:**
- Modify: `hdl/vivado/build_vendor.tcl:221`
- Modify: `hdl/vivado/system_top.v`
- Modify: `hdl/vivado/constr/plane_watcher_integration.xdc`

These changes enable the PPS pin and EMIO GPIO routing in the hybrid vendor build path. Ball assignment for 3V3_IO1 must be confirmed from the schematic before this task.

- [ ] **Step 1: Replace GND tie-off in build_vendor.tcl**

In `hdl/vivado/build_vendor.tcl`, replace line 221:

```tcl
ad_connect GND adsb_vendor_wrapper_0/pps_in
```

With:

```tcl
create_bd_port -dir I pps_in
ad_connect pps_in adsb_vendor_wrapper_0/pps_in
```

- [ ] **Step 2: Add pps_in port to system_top.v**

In `hdl/vivado/system_top.v`, add to the port declaration (after `input spi_miso` on line 91). Add a trailing comma to `spi_miso` (making it no longer the last port), then add `pps_in` as the new last port (no trailing comma until Task 10 adds more ports):

```verilog
  input           spi_miso,

  input           pps_in
```

Add the EMIO GPIO[17] wiring. Replace line 112:

```verilog
  assign gpio_i[16:14] = gpio_o[16:14];
```

With:

```verilog
  assign gpio_i[16:14] = gpio_o[16:14];
  assign gpio_i[17]    = pps_in;  // PPS routed to PS via EMIO GPIO[17]
```

Add to the system_wrapper instantiation (after the `.spi_sdo_o (pl_spi_mosi),` line):

```verilog
    .pps_in (pps_in),
```

- [ ] **Step 3: Add PPS pin constraint**

In `hdl/vivado/constr/plane_watcher_integration.xdc`, replace the pps_in comment block (lines 10-11):

```
# - "pps_in" is intentionally *not* constrained here because the current board
#   revision has no real PPS source. The hybrid BD ties PPS inactive for now.
```

With:

```
# PPS input from F9P GPS module on JP5 pin 5 / 3V3_IO1 (PL Bank 13, FPGA ball V10).
set_property -dict {PACKAGE_PIN V10 IOSTANDARD LVCMOS33} [get_ports pps_in]
```

- [ ] **Step 4: Commit**

```bash
git add hdl/vivado/build_vendor.tcl hdl/vivado/system_top.v hdl/vivado/constr/plane_watcher_integration.xdc
git commit -m "feat(fpga): export pps_in port in hybrid build, wire EMIO GPIO[17]

Replaces GND tie-off with external pps_in BD port in build_vendor.tcl.
Adds pps_in to system_top.v port list and routes it to gpio_i[17] for
the Linux PPS GPIO driver (GPIO 71).

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Device Tree — PPS GPIO and UART0

**Files:**
- Modify: `linux-dts/zynq-pluto-sdr-plane-watcher-hybrid.dtsi`

Add the pps-gpio node and enable UART0 for the F9P GPS UART.

- [ ] **Step 1: Add pps-gpio and uart0 nodes to the dtsi**

In `linux-dts/zynq-pluto-sdr-plane-watcher-hybrid.dtsi`, append after the existing content:

```dts
/ {
	pps {
		compatible = "pps-gpio";
		gpios = <&gpio0 71 0>;  /* EMIO GPIO[17] = MIO base 54 + 17, active high */
		status = "okay";
	};
};

&uart0 {
	status = "okay";
};
```

- [ ] **Step 2: Note for boot args**

The kernel boot args must be updated from `console=ttyPS0,115200` to `console=ttyPS1,115200` in the boot environment. This is a runtime configuration change, not a file edit — apply via U-Boot `setenv` or the SD card boot script on the target.

- [ ] **Step 3: Verify kernel config has PPS GPIO support**

On the target or in the kernel defconfig, check:
```
grep CONFIG_PPS_CLIENT_GPIO <kernel_config_path>
```
Expected: `CONFIG_PPS_CLIENT_GPIO=y` or `=m`. If not present, enable it and rebuild the kernel.

- [ ] **Step 4: Commit**

```bash
git add linux-dts/zynq-pluto-sdr-plane-watcher-hybrid.dtsi
git commit -m "feat(dts): add pps-gpio node and enable uart0 for GPS

PPS on EMIO GPIO[17] (Linux GPIO 71) creates /dev/pps0.
UART0 via EMIO creates /dev/ttyPS0 for gpsd.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: EMIO UART0 in BD Script

**Files:**
- Modify: `hdl/vivado/build_vendor.tcl`
- Modify: `hdl/vivado/system_top.v`
- Modify: `hdl/vivado/constr/plane_watcher_integration.xdc`

Enable PS UART0 via EMIO and route TX/RX through the PL to JP5 for the F9P GPS UART.

- [ ] **Step 1: Enable UART0 via EMIO in build_vendor.tcl**

In `hdl/vivado/build_vendor.tcl`, add to the PS7 config block (after the UART1 line at ~107):

```tcl
    CONFIG.PCW_UART0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_UART0_UART0_IO {EMIO} \
```

After the `create_bd_port -dir I pps_in` added in Task 8, add the UART port creation and connections:

```tcl
create_bd_port -dir O uart0_tx
create_bd_port -dir I uart0_rx
ad_connect processing_system7_0/UART0_TX uart0_tx
ad_connect uart0_rx processing_system7_0/UART0_RX
```

- [ ] **Step 2: Add UART ports to system_top.v**

In `hdl/vivado/system_top.v`, add to the port declaration (after `pps_in`). Add a trailing comma to `pps_in` (no longer last port), then add the UART ports with `uart0_rx` as the new last port (no trailing comma):

```verilog
  input           pps_in,

  output          uart0_tx,
  input           uart0_rx
```

Add to the system_wrapper instantiation:

```verilog
    .uart0_tx (uart0_tx),
    .uart0_rx (uart0_rx),
```

- [ ] **Step 3: Add UART pin constraints**

In `hdl/vivado/constr/plane_watcher_integration.xdc`, add after the PPS constraint:

```
# F9P GPS UART via EMIO UART0 on JP5 (Bank 13, 3.3V).
# uart0_rx: JP5 pin 7 / 3V3_IO2 (FPGA ball U9)
# uart0_tx: JP5 pin 9 / 3V3_IO3 (FPGA ball U10)
set_property -dict {PACKAGE_PIN U10 IOSTANDARD LVCMOS33} [get_ports uart0_tx]
set_property -dict {PACKAGE_PIN U9  IOSTANDARD LVCMOS33} [get_ports uart0_rx]
```

- [ ] **Step 4: Commit**

```bash
git add hdl/vivado/build_vendor.tcl hdl/vivado/system_top.v hdl/vivado/constr/plane_watcher_integration.xdc
git commit -m "feat(fpga): enable EMIO UART0 for F9P GPS serial

Routes PS UART0 TX/RX through PL to JP5 pins. Creates /dev/ttyPS0
for gpsd. Ball assignments TBD pending schematic confirmation.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review Notes

**Spec coverage check:** All items from the design doc's implementation checklist (§8) are covered:
- Hardware/FPGA: Tasks 8, 10
- Linux/Device Tree: Task 9 (kernel boot args noted as runtime config, not a file edit)
- plane-feeder Software: Tasks 1-5, 7
- Tests: Tasks 1-5 (unit tests), Task 7 (integration build)
- GPS stack config (gpsd, chrony, F9P init scripts): Not covered — these are target-side runtime configuration, not repo code changes. Document as manual steps in the design doc.

**Not in scope (by design):**
- gpsd/chrony cross-compilation and init scripts — these are rootfs packaging, not repo code
- F9P ubxtool configuration script — one-time hardware setup
- End-to-end on-hardware test — requires physical GPS + board

**Type consistency verified:**
- `ClockRef` used consistently: defined in `pps.go`, consumed by `beast.EncodeV2()`, published by `Watcher.Ref()`
- `Stats` fields match between `pps.Stats` and `web.StatsData` GPS fields
- `SyncChecker` interface matches `chrony.Checker.IsSynced()` signature
- `ReadPpsStable` used in watcher (Task 4) and defined in Task 1
