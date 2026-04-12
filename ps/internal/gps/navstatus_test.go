package gps

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestParseNavClock(t *testing.T) {
	pl := make([]byte, 20)
	clkB := int32(-500) // −500 ns (signed)
	clkD := int32(17)   // +17 ns/s
	binary.LittleEndian.PutUint32(pl[0:4], 123456)        // iTOW
	binary.LittleEndian.PutUint32(pl[4:8], uint32(clkB))  // clkB
	binary.LittleEndian.PutUint32(pl[8:12], uint32(clkD)) // clkD
	binary.LittleEndian.PutUint32(pl[12:16], 42)          // tAcc
	binary.LittleEndian.PutUint32(pl[16:20], 9000)        // fAcc
	got, err := ParseNavClock(pl)
	if err != nil {
		t.Fatal(err)
	}
	if got.ITOW != 123456 {
		t.Errorf("ITOW = %d", got.ITOW)
	}
	if got.ClockBiasNs != -500 {
		t.Errorf("ClockBiasNs = %d, want -500", got.ClockBiasNs)
	}
	if got.ClockDriftNsPerS != 17 {
		t.Errorf("ClockDriftNsPerS = %d, want 17", got.ClockDriftNsPerS)
	}
	if got.TimeAccuracyNs != 42 {
		t.Errorf("TimeAccuracyNs = %d, want 42", got.TimeAccuracyNs)
	}
	if got.FreqAccuracyPsPerS != 9000 {
		t.Errorf("FreqAccuracyPsPerS = %d, want 9000", got.FreqAccuracyPsPerS)
	}
}

func TestParseNavClock_TooShort(t *testing.T) {
	if _, err := ParseNavClock(make([]byte, 10)); err == nil {
		t.Error("expected error")
	}
}

func TestParseNavDOP(t *testing.T) {
	pl := make([]byte, 18)
	binary.LittleEndian.PutUint32(pl[0:4], 7890)
	// Each DOP value is a uint16 scaled by 0.01 on the wire. 150 → 1.50
	binary.LittleEndian.PutUint16(pl[4:6], 150)
	binary.LittleEndian.PutUint16(pl[6:8], 130)
	binary.LittleEndian.PutUint16(pl[8:10], 85)
	binary.LittleEndian.PutUint16(pl[10:12], 110)
	binary.LittleEndian.PutUint16(pl[12:14], 72)
	binary.LittleEndian.PutUint16(pl[14:16], 55)
	binary.LittleEndian.PutUint16(pl[16:18], 48)

	got, err := ParseNavDOP(pl)
	if err != nil {
		t.Fatal(err)
	}
	if got.ITOW != 7890 {
		t.Errorf("ITOW = %d", got.ITOW)
	}
	want := []struct {
		name string
		got  float64
		want float64
	}{
		{"GDOP", got.GDOP, 1.50},
		{"PDOP", got.PDOP, 1.30},
		{"TDOP", got.TDOP, 0.85},
		{"VDOP", got.VDOP, 1.10},
		{"HDOP", got.HDOP, 0.72},
		{"NDOP", got.NDOP, 0.55},
		{"EDOP", got.EDOP, 0.48},
	}
	for _, c := range want {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %f, want %f", c.name, c.got, c.want)
		}
	}
}

func TestParseNavDOP_TooShort(t *testing.T) {
	if _, err := ParseNavDOP(make([]byte, 10)); err == nil {
		t.Error("expected error")
	}
}
