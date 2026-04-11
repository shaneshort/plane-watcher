package gps

import (
	"testing"
)

func TestParseMonVer_M8(t *testing.T) {
	payload := buildMonVerPayload("EXT CORE 3.01 (107900)", "00080000",
		"FWVER=SPG 3.01", "PROTVER=18.00", "MOD=NEO-M8T")
	mv, err := ParseMonVer(payload)
	if err != nil {
		t.Fatalf("ParseMonVer: %v", err)
	}
	if mv.SwVersion != "EXT CORE 3.01 (107900)" {
		t.Errorf("SwVersion = %q", mv.SwVersion)
	}
	if mv.HwVersion != "00080000" {
		t.Errorf("HwVersion = %q", mv.HwVersion)
	}
	if mv.Generation() != GenM8 {
		t.Errorf("Generation = %s, want M8", mv.Generation())
	}
	if len(mv.Extensions) != 3 {
		t.Errorf("Extensions count = %d, want 3", len(mv.Extensions))
	}
}

func TestParseMonVer_F9(t *testing.T) {
	payload := buildMonVerPayload("EXT CORE 1.00 (61b9c5)", "00190000",
		"FWVER=HPG 1.13", "PROTVER=27.12", "MOD=ZED-F9P")
	mv, err := ParseMonVer(payload)
	if err != nil {
		t.Fatalf("ParseMonVer: %v", err)
	}
	if mv.Generation() != GenF9 {
		t.Errorf("Generation = %s, want F9", mv.Generation())
	}
}

func TestParseMonVer_Unknown(t *testing.T) {
	payload := buildMonVerPayload("EXT CORE 9.99", "DEADBEEF")
	mv, err := ParseMonVer(payload)
	if err != nil {
		t.Fatalf("ParseMonVer: %v", err)
	}
	if mv.Generation() != GenUnknown {
		t.Errorf("Generation = %s, want unknown", mv.Generation())
	}
}

func TestParseMonVer_TooShort(t *testing.T) {
	if _, err := ParseMonVer([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for short payload")
	}
}

// buildMonVerPayload constructs a MON-VER payload in the wire layout the
// receiver would produce: 30-byte sw, 10-byte hw, then N * 30-byte extensions.
func buildMonVerPayload(sw, hw string, extensions ...string) []byte {
	out := make([]byte, 0, 40+30*len(extensions))
	out = append(out, padField(sw, 30)...)
	out = append(out, padField(hw, 10)...)
	for _, e := range extensions {
		out = append(out, padField(e, 30)...)
	}
	return out
}

func padField(s string, n int) []byte {
	out := make([]byte, n)
	copy(out, s)
	return out
}
