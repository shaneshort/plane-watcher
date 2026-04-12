package gpsmon

import (
	"testing"
	"time"
)

func TestHistory_AppendRetainsRecent(t *testing.T) {
	h := NewHistory(5 * time.Second)
	now := time.Now().UnixMilli()
	// 10 samples, 1 second apart.
	for i := 0; i < 10; i++ {
		h.Append(Sample{TsMS: now + int64(i)*1000, OscillatorPPM: float64(i), UsedSats: i + 5})
	}
	// With a 5 s retention, only the last 6 samples (index 4..9 — those
	// with ts >= now+4000 because the cutoff is the LATEST sample's
	// ts minus 5 s = now+9000-5000 = now+4000) should be retained.
	got := h.Snapshot()
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6 (5 s retention from the latest ts)", len(got))
	}
	if got[0].OscillatorPPM != 4 {
		t.Errorf("oldest retained sample PPM = %f, want 4", got[0].OscillatorPPM)
	}
	if got[len(got)-1].OscillatorPPM != 9 {
		t.Errorf("newest sample PPM = %f, want 9", got[len(got)-1].OscillatorPPM)
	}
}

func TestHistory_DefaultRetention(t *testing.T) {
	h := NewHistory(0)
	if h.Retention != time.Hour {
		t.Errorf("default retention = %v, want 1h", h.Retention)
	}
}

func TestHistory_EmptySnapshot(t *testing.T) {
	h := NewHistory(0)
	if got := h.Snapshot(); len(got) != 0 {
		t.Errorf("empty history Snapshot len = %d, want 0", len(got))
	}
}

func TestHistory_SnapshotIsCopy(t *testing.T) {
	h := NewHistory(time.Hour)
	h.Append(Sample{TsMS: 1, OscillatorPPM: 1, UsedSats: 10})
	snap := h.Snapshot()
	// Mutate the returned slice — the internal storage must be unaffected.
	snap[0].OscillatorPPM = 999
	snap2 := h.Snapshot()
	if snap2[0].OscillatorPPM == 999 {
		t.Error("Snapshot returned internal storage, not a copy")
	}
}
