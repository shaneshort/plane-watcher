package pps

import (
	"log"
	"sync/atomic"
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
	GpsSync       bool    `json:"gps_sync"`
	OscillatorPPM float64 `json:"oscillator_ppm"`
	Carryover     int64   `json:"carryover"`
	AvgCarryover  float64 `json:"avg_carryover"`
	CarryoverMin  int64   `json:"carryover_min"`
	CarryoverMax  int64   `json:"carryover_max"`
	MeasuredTicks uint64  `json:"pps_interval_ticks"`
	SkippedEdges  uint64  `json:"skipped_edges"`
}

const rollingWindowSize = 60

// watcherState holds the internal state for PPS update computation.
// Separated from the goroutine for testability.
type watcherState struct {
	prev           regs.PpsState
	prevWall       time.Time
	lastPpsAdvance time.Time
	hasPrev        bool
	ref            ClockRef

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

	// Rolling window for smoothed PPM.
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
		GpsSync:       w.ref.GpsSync,
		OscillatorPPM: ppm,
		Carryover:     w.ref.Carryover,
		AvgCarryover:  avgCO,
		CarryoverMin:  w.carryoverMin,
		CarryoverMax:  w.carryoverMax,
		MeasuredTicks: w.ref.MeasuredTicks,
		SkippedEdges:  w.skippedEdges,
	}
}

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
			tickCount++
			if tickCount == 1 || tickCount%chronyInterval == 0 {
				if w.sync.IsSynced() {
					chronySynced = true
					lastChronyOK = now
				}
			}

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
