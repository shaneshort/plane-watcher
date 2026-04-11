package gps

import (
	"encoding/binary"
	"testing"
)

func TestBuildCfgCfgSaveAll(t *testing.T) {
	pl := BuildCfgCfgSaveAll()
	if len(pl) != 13 {
		t.Fatalf("len = %d, want 13", len(pl))
	}
	clearMask := binary.LittleEndian.Uint32(pl[0:4])
	saveMask := binary.LittleEndian.Uint32(pl[4:8])
	loadMask := binary.LittleEndian.Uint32(pl[8:12])
	deviceMask := pl[12]
	if clearMask != 0 {
		t.Errorf("clearMask = 0x%08x, want 0", clearMask)
	}
	if saveMask != 0x00001F1F {
		t.Errorf("saveMask = 0x%08x, want 0x00001F1F", saveMask)
	}
	if loadMask != 0 {
		t.Errorf("loadMask = 0x%08x, want 0", loadMask)
	}
	// devBBR (bit 0) and devFlash (bit 1) set, rest zero.
	if deviceMask != 0x03 {
		t.Errorf("deviceMask = 0x%02x, want 0x03", deviceMask)
	}
}
