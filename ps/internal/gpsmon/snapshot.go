package gpsmon

import (
	"time"

	"github.com/plane-watcher/plane-feeder/internal/gps"
)

// Snapshot is the atomic state the Collector publishes after each cycle.
// HTTP handlers call Collector.Snapshot() to get a pointer to the most
// recent one; pointers are never mutated after being stored, so the
// caller can read any field without locks.
type Snapshot struct {
	// Static-ish fields (written once per cycle, but usually the same
	// value across cycles unless the operator reconfigures the receiver).
	Receiver    ReceiverInfo
	ReceiverAt  time.Time
	ReceiverErr string

	// Dynamic fields — updated every PollInterval tick.
	Clock       gps.ClockStatus
	DOP         gps.DOPStatus
	HW          gps.HWStatus
	Survey      gps.SVINStatus
	PeriodicAt  time.Time
	PeriodicErr string

	// Live fields from the gpsd TPV/SKY atomic cache. Updated at the
	// same cadence as the cycle, but the underlying data is refreshed
	// by the Client reader goroutine at gpsd's 1 Hz pace.
	Fix    gps.TPV
	Sats   []gps.Satellite
	SkyDOP gps.SKY
	FixAt  time.Time
	SatsAt time.Time

	// TransportErr is set when the collector can't even reach gpsd.
	// When this is non-empty the other fields carry stale data from
	// the previous successful cycle (or zero values on first failure).
	TransportErr string
}

// ReceiverInfo is the "who is this receiver" block of the snapshot.
// Populated from MON-VER plus the static CFG polls.
type ReceiverInfo struct {
	Generation string   // "M8", "F9", or "unknown"
	SwVersion  string
	HwVersion  string
	Extensions []string // MON-VER extension strings
	TMODEMode  int      // 0=disabled, 1=survey-in, 2=fixed, -1 unknown
	NAV5       gps.NAV5Summary
	TP5        gps.TP5Summary
}

// appendReceiverErr accumulates a receiver-section error, separating
// multiple errors with "; ".
func (s *Snapshot) appendReceiverErr(msg string) {
	if s.ReceiverErr == "" {
		s.ReceiverErr = msg
		return
	}
	s.ReceiverErr += "; " + msg
}

// appendPeriodicErr is the same for the periodic-poll section.
func (s *Snapshot) appendPeriodicErr(msg string) {
	if s.PeriodicErr == "" {
		s.PeriodicErr = msg
		return
	}
	s.PeriodicErr += "; " + msg
}
