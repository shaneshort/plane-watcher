package gps

import (
	"encoding/binary"
	"testing"
)

func TestBuildNAV5DynModelOnly(t *testing.T) {
	pl := BuildNAV5DynModelOnly(DynModelStationary)
	if len(pl) != 36 {
		t.Fatalf("len = %d, want 36", len(pl))
	}
	mask := binary.LittleEndian.Uint16(pl[0:2])
	if mask != nav5MaskDyn {
		t.Errorf("mask = 0x%04x, want 0x%04x (dyn only)", mask, nav5MaskDyn)
	}
	if pl[2] != DynModelStationary {
		t.Errorf("dynModel = %d, want %d", pl[2], DynModelStationary)
	}
	// All other bytes zero — they're ignored by the receiver because
	// their mask bits are not set, so leaving them zero is safe.
	for i := 3; i < len(pl); i++ {
		if pl[i] != 0 {
			t.Errorf("byte[%d] = 0x%02x, want 0", i, pl[i])
		}
	}
}

func TestParseNAV5(t *testing.T) {
	pl := make([]byte, 36)
	pl[2] = DynModelStationary
	pl[3] = 3 // auto 2D/3D
	pl[12] = 10
	pl[30] = 3 // USNO UTC
	got, err := ParseNAV5(pl)
	if err != nil {
		t.Fatal(err)
	}
	if got.DynModel != DynModelStationary || got.FixMode != 3 || got.MinElev != 10 || got.UtcStandard != 3 {
		t.Errorf("ParseNAV5 = %+v", got)
	}
}

func TestDynModelName(t *testing.T) {
	if DynModelName(DynModelStationary) != "stationary" {
		t.Error("stationary")
	}
	if DynModelName(99) == "" {
		t.Error("unknown name should not be empty")
	}
}
