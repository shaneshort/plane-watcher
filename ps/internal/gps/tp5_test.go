package gps

import (
	"encoding/binary"
	"testing"
)

func TestBuildTP5_1HzUTC(t *testing.T) {
	pl := BuildTP5_1HzUTC()
	if len(pl) != 32 {
		t.Fatalf("len = %d, want 32", len(pl))
	}
	if pl[0] != 0 {
		t.Errorf("tpIdx = %d, want 0", pl[0])
	}
	if pl[1] != 0x01 {
		t.Errorf("version = 0x%02x, want 0x01", pl[1])
	}
	if got := binary.LittleEndian.Uint32(pl[8:12]); got != 1 {
		t.Errorf("freqPeriod = %d, want 1 Hz", got)
	}
	if got := binary.LittleEndian.Uint32(pl[12:16]); got != 1 {
		t.Errorf("freqPeriodLock = %d, want 1 Hz", got)
	}
	if got := binary.LittleEndian.Uint32(pl[16:20]); got != 500000 {
		t.Errorf("pulseLenRatio = %d, want 500000 us", got)
	}
	if got := binary.LittleEndian.Uint32(pl[20:24]); got != 500000 {
		t.Errorf("pulseLenRatioLock = %d, want 500000 us", got)
	}
	flags := binary.LittleEndian.Uint32(pl[28:32])
	// Expected: active, lockGpsFreq, lockedOtherSet, isFreq, isLength,
	// alignToTow, polarity=rising. gridGPS NOT set (UTC).
	want := uint32(tp5FlagActive | tp5FlagLockGpsFreq | tp5FlagLockedOtherSet |
		tp5FlagIsFreq | tp5FlagIsLength | tp5FlagAlignToTow | tp5FlagPolarityRising)
	if flags != want {
		t.Errorf("flags = 0x%08x, want 0x%08x", flags, want)
	}
	if flags&tp5FlagGridGPS != 0 {
		t.Errorf("gridGPS bit must not be set (UTC grid), flags = 0x%08x", flags)
	}
}

func TestParseTP5(t *testing.T) {
	pl := BuildTP5_1HzUTC()
	got, err := ParseTP5(pl)
	if err != nil {
		t.Fatal(err)
	}
	if got.TpIdx != 0 || got.FreqPeriod != 1 || got.PulseLenRatio != 500000 {
		t.Errorf("parse wrong fields: %+v", got)
	}
	if !got.Active || !got.LockGpsFreq || !got.IsFreq || !got.IsLength ||
		!got.AlignToTow || !got.RisingAtTop || !got.GridUTC {
		t.Errorf("parse wrong flags: %+v", got)
	}
}
