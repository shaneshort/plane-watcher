package gpsmon

import (
	"sync"
	"time"
)

// Sample is one point in the time-series history published at the end of
// every Collector cycle. JSON tags mirror the on-the-wire format the
// dashboard charts expect. Units are kept per-field rather than packed
// into tuples so the JS can reference them by name.
type Sample struct {
	// TsMS is the Unix timestamp in milliseconds. The JS charts use it
	// as the x-axis directly; ms avoids second-granularity jitter.
	TsMS int64 `json:"ts_ms"`
	// OscillatorPPM is the PPS-disciplined oscillator offset in
	// parts-per-million, sampled from pps.Watcher via the PPMFn
	// callback. Zero if no PPM source is wired up.
	OscillatorPPM float64 `json:"oscillator_ppm"`
	// UsedSats is the number of satellites contributing to the
	// current fix, read from the gpsd SKY message cache.
	UsedSats int `json:"used_sats"`
}

// History is a time-bounded ring buffer of Samples. The retention window
// is enforced on every Append (samples older than Retention are dropped
// from the front) rather than by a fixed slot count, so an operator who
// bumps --gpsmon-interval doesn't accidentally get a shorter visible
// window on the chart.
//
// History is safe for concurrent use: one writer (the Collector cycle)
// and one or more readers (HTTP handlers). The mutex is held only
// briefly for append/snapshot, never during JSON encoding.
type History struct {
	mu        sync.Mutex
	samples   []Sample
	Retention time.Duration
}

// NewHistory returns a History that keeps samples for the supplied
// retention window. A retention of 0 or less defaults to one hour.
func NewHistory(retention time.Duration) *History {
	if retention <= 0 {
		retention = time.Hour
	}
	return &History{
		Retention: retention,
	}
}

// Append records a new Sample and trims any samples older than the
// retention window. Safe to call from the Collector goroutine.
func (h *History) Append(s Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, s)
	h.trimLocked(s.TsMS)
}

// trimLocked drops samples whose timestamp is older than
// nowMS - Retention. Caller must hold h.mu.
func (h *History) trimLocked(nowMS int64) {
	cutoff := nowMS - h.Retention.Milliseconds()
	// Walk from the front and find the first sample that's still
	// within the retention window. Common case: drop at most a few
	// samples per call.
	drop := 0
	for drop < len(h.samples) && h.samples[drop].TsMS < cutoff {
		drop++
	}
	if drop == 0 {
		return
	}
	// Re-slice so the dropped samples become GC-collectable.
	h.samples = append(h.samples[:0], h.samples[drop:]...)
}

// Snapshot returns a copy of the current samples slice. The copy lets
// callers marshal to JSON without holding the lock or racing an append.
func (h *History) Snapshot() []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Sample, len(h.samples))
	copy(out, h.samples)
	return out
}

// Len returns the current number of stored samples. Useful for tests.
func (h *History) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.samples)
}
