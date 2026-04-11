package gps

import (
	"encoding/binary"
	"testing"
)

func TestBuildSurveyInTMODE2(t *testing.T) {
	// M8 svinAccLimit is in mm (not 0.1 mm like F9). For 2m we expect 2000.
	pl := BuildSurveyInTMODE2(300, 2000)
	if len(pl) != 28 {
		t.Fatalf("len = %d, want 28", len(pl))
	}
	if pl[0] != 1 {
		t.Errorf("timeMode = %d, want 1 (survey-in)", pl[0])
	}
	if got := binary.LittleEndian.Uint32(pl[20:24]); got != 300 {
		t.Errorf("svinMinDur = %d, want 300", got)
	}
	if got := binary.LittleEndian.Uint32(pl[24:28]); got != 2000 {
		t.Errorf("svinAccLimit = %d, want 2000 (mm)", got)
	}
}

func TestBuildSurveyInTMODE3(t *testing.T) {
	pl := BuildSurveyInTMODE3(600, 5000) // 600s, 0.5m
	if len(pl) != 40 {
		t.Fatalf("len = %d, want 40", len(pl))
	}
	if pl[2] != 1 {
		t.Errorf("flags low byte = %d, want 1 (mode=survey-in)", pl[2])
	}
	if got := binary.LittleEndian.Uint32(pl[24:28]); got != 600 {
		t.Errorf("svinMinDur = %d, want 600", got)
	}
	if got := binary.LittleEndian.Uint32(pl[28:32]); got != 5000 {
		t.Errorf("svinAccLimit = %d, want 5000", got)
	}
}

func TestSurveyInFrame_Generation(t *testing.T) {
	// 2.0 metres → M8 expects 2000 mm, F9 expects 20000 (0.1 mm units).
	m8, err := SurveyInFrame(GenM8, 300, 2.0)
	if err != nil {
		t.Fatal(err)
	}
	if m8.Class != ClassCFG || m8.ID != IDCfgTMODE2 || len(m8.Payload) != 28 {
		t.Errorf("M8 frame wrong: class=%02x id=%02x len=%d", m8.Class, m8.ID, len(m8.Payload))
	}
	if got := binary.LittleEndian.Uint32(m8.Payload[24:28]); got != 2000 {
		t.Errorf("M8 svinAccLimit = %d, want 2000 mm", got)
	}

	f9, err := SurveyInFrame(GenF9, 300, 2.0)
	if err != nil {
		t.Fatal(err)
	}
	if f9.Class != ClassCFG || f9.ID != IDCfgTMODE3 || len(f9.Payload) != 40 {
		t.Errorf("F9 frame wrong: class=%02x id=%02x len=%d", f9.Class, f9.ID, len(f9.Payload))
	}
	if got := binary.LittleEndian.Uint32(f9.Payload[28:32]); got != 20000 {
		t.Errorf("F9 svinAccLimit = %d, want 20000 (0.1 mm)", got)
	}

	if _, err := SurveyInFrame(GenUnknown, 300, 2.0); err == nil {
		t.Error("expected error for GenUnknown")
	}
}

// TestSurveyInFrame_M8AccuracyIsMillimetres is a targeted regression test
// for the code-review finding that M8 CFG-TMODE2 uses millimetres, not
// 0.1 mm. Previously the code treated both generations the same, causing
// M8 surveys to terminate 10× too loose.
func TestSurveyInFrame_M8AccuracyIsMillimetres(t *testing.T) {
	// 5.0 metres → M8 expects 5000 mm, F9 expects 50000 (0.1 mm).
	// If they're ever equal for the same metres input, the bug is back.
	m8, _ := SurveyInFrame(GenM8, 300, 5.0)
	f9, _ := SurveyInFrame(GenF9, 300, 5.0)
	m8Acc := binary.LittleEndian.Uint32(m8.Payload[24:28])
	f9Acc := binary.LittleEndian.Uint32(f9.Payload[28:32])
	if m8Acc == f9Acc {
		t.Fatalf("M8 and F9 must encode accuracy differently (mm vs 0.1 mm); both = %d", m8Acc)
	}
	if m8Acc != 5000 {
		t.Errorf("M8 accuracy = %d, want 5000 (mm)", m8Acc)
	}
	if f9Acc != 50000 {
		t.Errorf("F9 accuracy = %d, want 50000 (0.1 mm)", f9Acc)
	}
}

func TestRestoreFrame(t *testing.T) {
	raw28 := make([]byte, 28)
	for i := range raw28 {
		raw28[i] = byte(i)
	}
	f, err := RestoreFrame(TMODEPayload{Generation: GenM8, Raw: raw28})
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != IDCfgTMODE2 || len(f.Payload) != 28 || f.Payload[5] != 5 {
		t.Errorf("restore M8 wrong: id=%02x payload=%x", f.ID, f.Payload)
	}

	// Wrong length should error.
	if _, err := RestoreFrame(TMODEPayload{Generation: GenM8, Raw: make([]byte, 27)}); err == nil {
		t.Error("expected length error")
	}
	if _, err := RestoreFrame(TMODEPayload{Generation: GenF9, Raw: make([]byte, 39)}); err == nil {
		t.Error("expected length error")
	}
}

func TestParseSVIN_M8(t *testing.T) {
	pl := make([]byte, 28)
	binary.LittleEndian.PutUint32(pl[0:4], 312) // dur
	// meanV variance: 2_250_000 mm^2 → sqrt = 1500 mm = 1.5 m
	binary.LittleEndian.PutUint32(pl[16:20], 2_250_000)
	binary.LittleEndian.PutUint32(pl[20:24], 6240) // obs
	pl[24] = 1                                     // valid
	pl[25] = 0                                     // active

	st, err := ParseSVIN(GenM8, pl)
	if err != nil {
		t.Fatal(err)
	}
	if st.DurationSec != 312 || st.Observations != 6240 || !st.Valid || st.Active {
		t.Errorf("status wrong: %+v", st)
	}
	if st.MeanAccMeters < 1.49 || st.MeanAccMeters > 1.51 {
		t.Errorf("MeanAccMeters = %f, want ~1.5", st.MeanAccMeters)
	}
}

func TestParseSVIN_F9(t *testing.T) {
	pl := make([]byte, 40)
	binary.LittleEndian.PutUint32(pl[8:12], 312)    // dur
	binary.LittleEndian.PutUint32(pl[28:32], 18470) // meanAcc 0.1mm → 1.847 m
	binary.LittleEndian.PutUint32(pl[32:36], 6240)  // obs
	pl[36] = 1                                      // valid
	pl[37] = 0                                      // active

	st, err := ParseSVIN(GenF9, pl)
	if err != nil {
		t.Fatal(err)
	}
	if st.DurationSec != 312 || st.Observations != 6240 || !st.Valid || st.Active {
		t.Errorf("status wrong: %+v", st)
	}
	if st.MeanAccMeters < 1.846 || st.MeanAccMeters > 1.848 {
		t.Errorf("MeanAccMeters = %f, want ~1.847", st.MeanAccMeters)
	}
}


func TestBuildFixedModeTMODE2(t *testing.T) {
	// Example coords (1 cm ECEF): arbitrary
	pl := BuildFixedModeTMODE2(-237026900, 487138600, -335500900, 2000)
	if len(pl) != 28 {
		t.Fatalf("len = %d, want 28", len(pl))
	}
	if pl[0] != 2 {
		t.Errorf("timeMode = %d, want 2 (fixed)", pl[0])
	}
	x := int32(binary.LittleEndian.Uint32(pl[4:8]))
	y := int32(binary.LittleEndian.Uint32(pl[8:12]))
	z := int32(binary.LittleEndian.Uint32(pl[12:16]))
	acc := binary.LittleEndian.Uint32(pl[16:20])
	if x != -237026900 || y != 487138600 || z != -335500900 {
		t.Errorf("ECEF = (%d, %d, %d)", x, y, z)
	}
	if acc != 2000 {
		t.Errorf("fixedPosAcc = %d, want 2000 mm", acc)
	}
	// Survey-in fields at offsets 20-27 must be zero.
	if binary.LittleEndian.Uint32(pl[20:24]) != 0 || binary.LittleEndian.Uint32(pl[24:28]) != 0 {
		t.Errorf("svinMinDur/svinAccLimit must be zero in fixed mode")
	}
}

func TestBuildFixedModeTMODE3(t *testing.T) {
	pl := BuildFixedModeTMODE3(-237026900, 487138600, -335500900, 12, -3, 5, 18470)
	if len(pl) != 40 {
		t.Fatalf("len = %d, want 40", len(pl))
	}
	// flags.mode lives in low 3 bits of pl[2].
	if pl[2]&0x07 != 2 {
		t.Errorf("flags.mode = %d, want 2 (fixed)", pl[2]&0x07)
	}
	x := int32(binary.LittleEndian.Uint32(pl[4:8]))
	if x != -237026900 {
		t.Errorf("ecefX = %d", x)
	}
	if int8(pl[16]) != 12 || int8(pl[17]) != -3 || int8(pl[18]) != 5 {
		t.Errorf("HP parts = (%d,%d,%d)", int8(pl[16]), int8(pl[17]), int8(pl[18]))
	}
	if binary.LittleEndian.Uint32(pl[20:24]) != 18470 {
		t.Errorf("fixedPosAcc wrong")
	}
	// Survey-in fields at 24-31 must be zero.
	if binary.LittleEndian.Uint32(pl[24:28]) != 0 || binary.LittleEndian.Uint32(pl[28:32]) != 0 {
		t.Errorf("svinMinDur/svinAccLimit must be zero in fixed mode")
	}
}

func TestFixedModeFrameFromSVIN(t *testing.T) {
	st := SVINStatus{
		Valid:         true,
		Active:        false,
		MeanXCm:       -237026900,
		MeanYCm:       487138600,
		MeanZCm:       -335500900,
		MeanAccMeters: 1.5,
	}
	m8, err := FixedModeFrameFromSVIN(GenM8, st)
	if err != nil {
		t.Fatal(err)
	}
	if m8.Class != ClassCFG || m8.ID != IDCfgTMODE2 || len(m8.Payload) != 28 {
		t.Errorf("M8 frame wrong")
	}
	acc := binary.LittleEndian.Uint32(m8.Payload[16:20])
	if acc != 1500 {
		t.Errorf("M8 fixedPosAcc = %d, want 1500 mm (= 1.5 m)", acc)
	}

	st.MeanXHP = 7
	f9, err := FixedModeFrameFromSVIN(GenF9, st)
	if err != nil {
		t.Fatal(err)
	}
	if f9.Class != ClassCFG || f9.ID != IDCfgTMODE3 || len(f9.Payload) != 40 {
		t.Errorf("F9 frame wrong")
	}
	acc = binary.LittleEndian.Uint32(f9.Payload[20:24])
	if acc != 15000 {
		t.Errorf("F9 fixedPosAcc = %d, want 15000 (0.1 mm, = 1.5 m)", acc)
	}
	if int8(f9.Payload[16]) != 7 {
		t.Errorf("F9 HP X = %d, want 7", int8(f9.Payload[16]))
	}
}

func TestTMODEMode(t *testing.T) {
	m8Fixed := BuildFixedModeTMODE2(1, 2, 3, 100)
	if m := TMODEMode(GenM8, m8Fixed); m != 2 {
		t.Errorf("M8 fixed TMODEMode = %d, want 2", m)
	}
	m8Survey := BuildSurveyInTMODE2(300, 2000)
	if m := TMODEMode(GenM8, m8Survey); m != 1 {
		t.Errorf("M8 survey TMODEMode = %d, want 1", m)
	}
	f9Fixed := BuildFixedModeTMODE3(1, 2, 3, 0, 0, 0, 100)
	if m := TMODEMode(GenF9, f9Fixed); m != 2 {
		t.Errorf("F9 fixed TMODEMode = %d, want 2", m)
	}
}

func TestPollFrames(t *testing.T) {
	tm8, _ := PollTMODEFrame(GenM8)
	if tm8.Class != ClassCFG || tm8.ID != IDCfgTMODE2 || len(tm8.Payload) != 0 {
		t.Errorf("M8 TMODE poll wrong: %+v", tm8)
	}
	tf9, _ := PollTMODEFrame(GenF9)
	if tf9.Class != ClassCFG || tf9.ID != IDCfgTMODE3 || len(tf9.Payload) != 0 {
		t.Errorf("F9 TMODE poll wrong: %+v", tf9)
	}
	sm8, _ := SVINPollFrame(GenM8)
	if sm8.Class != ClassTIM || sm8.ID != IDTimSVIN {
		t.Errorf("M8 SVIN poll wrong: %+v", sm8)
	}
	sf9, _ := SVINPollFrame(GenF9)
	if sf9.Class != ClassNAV || sf9.ID != IDNavSVIN {
		t.Errorf("F9 SVIN poll wrong: %+v", sf9)
	}
}
