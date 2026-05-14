package steps

import (
	"path/filepath"
	"testing"
)

func TestParseTimingMet(t *testing.T) {
	r, err := ParseTimingReport(filepath.Join("testdata", "timing_met.rpt"))
	if err != nil {
		t.Fatalf("ParseTimingReport: %v", err)
	}
	if !r.MetConstraints {
		t.Errorf("expected MetConstraints true, got false")
	}
	if r.WNS != 0.234 {
		t.Errorf("WNS = %v, want 0.234", r.WNS)
	}
	if r.TNS != 0.000 {
		t.Errorf("TNS = %v, want 0.000", r.TNS)
	}
	if r.WHS != 0.045 {
		t.Errorf("WHS = %v, want 0.045", r.WHS)
	}
}

func TestParseTimingNotMet(t *testing.T) {
	r, err := ParseTimingReport(filepath.Join("testdata", "timing_not_met.rpt"))
	if err != nil {
		t.Fatalf("ParseTimingReport: %v", err)
	}
	if r.MetConstraints {
		t.Errorf("expected MetConstraints false, got true")
	}
	if r.WNS != -0.084 {
		t.Errorf("WNS = %v, want -0.084", r.WNS)
	}
}
