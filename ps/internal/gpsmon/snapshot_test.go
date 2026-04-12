package gpsmon

import (
	"strings"
	"testing"
)

func TestSnapshot_AppendReceiverErr(t *testing.T) {
	var s Snapshot
	s.appendReceiverErr("MON-VER failed")
	if s.ReceiverErr != "MON-VER failed" {
		t.Errorf("first err wrong: %q", s.ReceiverErr)
	}
	s.appendReceiverErr("CFG-TMODE failed")
	if !strings.Contains(s.ReceiverErr, "MON-VER failed") ||
		!strings.Contains(s.ReceiverErr, "CFG-TMODE failed") {
		t.Errorf("multi-error wrong: %q", s.ReceiverErr)
	}
}

func TestSnapshot_AppendPeriodicErr(t *testing.T) {
	var s Snapshot
	s.appendPeriodicErr("MON-HW timeout")
	s.appendPeriodicErr("NAV-CLOCK timeout")
	if !strings.Contains(s.PeriodicErr, "MON-HW timeout") ||
		!strings.Contains(s.PeriodicErr, "NAV-CLOCK timeout") {
		t.Errorf("PeriodicErr wrong: %q", s.PeriodicErr)
	}
}

func TestCollectorNew_Defaults(t *testing.T) {
	c := New(Options{})
	if c.opts.GpsdAddr != "localhost:2947" {
		t.Errorf("default GpsdAddr = %q", c.opts.GpsdAddr)
	}
	if c.opts.PollInterval <= 0 {
		t.Errorf("default PollInterval = %v", c.opts.PollInterval)
	}
	if c.opts.Logger == nil {
		t.Error("default Logger should not be nil")
	}
	if got := c.Snapshot(); got == nil {
		t.Error("Snapshot should never return nil")
	}
}
