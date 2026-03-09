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
