package gps

import (
	"encoding/binary"
	"testing"
)

func TestParseMonHW(t *testing.T) {
	// Build a 60-byte MON-HW payload with known fields.
	pl := make([]byte, 60)
	binary.LittleEndian.PutUint16(pl[16:18], 42)    // noisePerMS
	binary.LittleEndian.PutUint16(pl[18:20], 4100)  // agcCnt (50% of 8191)
	pl[20] = AntennaStatusOK
	pl[21] = AntennaPowerOn
	// flags: rtcCalib (bit 0) + jammingState=2 (warning, bits 2-3)
	pl[22] = 0x01 | (0x02 << 2) // 0x01 | 0x08 = 0x09
	pl[45] = 17                  // cwSuppression

	got, err := ParseMonHW(pl)
	if err != nil {
		t.Fatalf("ParseMonHW: %v", err)
	}
	if got.NoisePerMS != 42 {
		t.Errorf("NoisePerMS = %d, want 42", got.NoisePerMS)
	}
	if got.AGCCnt != 4100 {
		t.Errorf("AGCCnt = %d, want 4100", got.AGCCnt)
	}
	if got.AntennaStatus != AntennaStatusOK {
		t.Errorf("AntennaStatus = %d, want OK(%d)", got.AntennaStatus, AntennaStatusOK)
	}
	if got.AntennaPower != AntennaPowerOn {
		t.Errorf("AntennaPower = %d, want on", got.AntennaPower)
	}
	if got.JammingState != JammingStateWarning {
		t.Errorf("JammingState = %d, want warning(%d)", got.JammingState, JammingStateWarning)
	}
	if !got.RTCCalibrated {
		t.Errorf("RTCCalibrated = false, want true (flags bit 0 set)")
	}
	if got.CWSuppression != 17 {
		t.Errorf("CWSuppression = %d, want 17", got.CWSuppression)
	}
}

func TestParseMonHW_TooShort(t *testing.T) {
	if _, err := ParseMonHW(make([]byte, 40)); err == nil {
		t.Error("expected error for too-short payload")
	}
}

func TestAntennaStatusName(t *testing.T) {
	cases := map[uint8]string{
		AntennaStatusInit:     "init",
		AntennaStatusDontKnow: "unknown",
		AntennaStatusOK:       "ok",
		AntennaStatusShort:    "short",
		AntennaStatusOpen:     "open",
	}
	for code, want := range cases {
		if got := AntennaStatusName(code); got != want {
			t.Errorf("AntennaStatusName(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestJammingStateExtraction(t *testing.T) {
	// Verify the flags-byte bit extraction for each jamming state value.
	for state := uint8(0); state <= 3; state++ {
		pl := make([]byte, 60)
		pl[22] = state << 2 // bits 2-3
		got, err := ParseMonHW(pl)
		if err != nil {
			t.Fatal(err)
		}
		if got.JammingState != state {
			t.Errorf("flags=0x%02x → JammingState=%d, want %d", pl[22], got.JammingState, state)
		}
	}
}
