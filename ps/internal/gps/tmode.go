package gps

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

// TMODE2 / TMODE3 helpers.
//
// Field layouts and units come from the u-blox 8 / u-blox M8 Receiver
// Description (UBX-13003221) §32.10.36 and §32.10.37. Units MATTER: M8
// TMODE2 uses mm for accuracy fields, F9 TMODE3 uses 0.1 mm. Do not
// conflate the two.
//
// CFG-TMODE2 (M8):  28-byte payload — §32.10.36
//   u8  timeMode      0=disabled, 1=survey-in, 2=fixed
//   u8  reserved1
//   u16 flags         bit0: lla (1=LLH, 0=ECEF); bit1: altInv
//   i32 ecefXOrLat    cm or deg*1e-7
//   i32 ecefYOrLon    cm or deg*1e-7
//   i32 ecefZOrAlt    cm
//   u32 fixedPosAcc   mm                 <-- millimetres, NOT 0.1 mm
//   u32 svinMinDur    seconds
//   u32 svinAccLimit  mm                 <-- millimetres, NOT 0.1 mm
//
// CFG-TMODE3 (F9):  40-byte payload — §32.10.37
//   u8  version
//   u8  reserved1
//   u16 flags         low byte: mode (0=disabled, 1=survey-in, 2=fixed)
//                     bit8:    lla
//   i32 ecefXOrLat    cm or deg*1e-7
//   i32 ecefYOrLon    cm or deg*1e-7
//   i32 ecefZOrAlt    cm
//   i8  ecefXOrLatHP  0.1 mm or deg*1e-9 (sub-cm precision)
//   i8  ecefYOrLonHP
//   i8  ecefZOrAltHP
//   u8  reserved2
//   u32 fixedPosAcc   0.1 mm             <-- 0.1 millimetres
//   u32 svinMinDur    seconds
//   u32 svinAccLimit  0.1 mm             <-- 0.1 millimetres
//   u8[8] reserved3

// TMODEPayload is the union of fields needed to round-trip both TMODE2 and
// TMODE3 enough for our needs (read prior config, write survey-in, restore
// prior config). It is generation-tagged so callers know which encoder to
// invoke.
type TMODEPayload struct {
	Generation Generation
	Raw        []byte // exact bytes returned by the receiver — restored verbatim on rollback
}

// BuildSurveyInTMODE2 returns a 28-byte CFG-TMODE2 payload that puts an M8
// receiver into survey-in mode with the given thresholds. The accuracy
// limit is in MILLIMETRES (native M8 units per UBX-13003221 §32.10.36).
func BuildSurveyInTMODE2(minDurSec uint32, accLimitMM uint32) []byte {
	pl := make([]byte, 28)
	pl[0] = 0x01 // timeMode = survey-in
	// flags = 0 (lla=0, but unused for survey-in mode anyway)
	binary.LittleEndian.PutUint32(pl[20:24], minDurSec)
	binary.LittleEndian.PutUint32(pl[24:28], accLimitMM)
	return pl
}

// BuildFixedModeTMODE2 returns a 28-byte CFG-TMODE2 payload that puts an
// M8 receiver into FIXED time mode at the supplied ECEF coordinates.
// ecefX/Y/Z are in cm; fixedPosAccMM is in millimetres (M8 native units
// per UBX-13003221 §32.10.36). Survey-in fields are zeroed.
func BuildFixedModeTMODE2(ecefXCm, ecefYCm, ecefZCm int32, fixedPosAccMM uint32) []byte {
	pl := make([]byte, 28)
	pl[0] = 0x02 // timeMode = fixed
	// flags = 0 (lla=0 → ECEF coordinates)
	binary.LittleEndian.PutUint32(pl[4:8], uint32(ecefXCm))
	binary.LittleEndian.PutUint32(pl[8:12], uint32(ecefYCm))
	binary.LittleEndian.PutUint32(pl[12:16], uint32(ecefZCm))
	binary.LittleEndian.PutUint32(pl[16:20], fixedPosAccMM)
	// svinMinDur/svinAccLimit = 0 (unused in fixed mode)
	return pl
}

// BuildSurveyInTMODE3 returns a 40-byte CFG-TMODE3 payload that puts an F9
// receiver into survey-in mode with the given thresholds. The accuracy
// limit is in 0.1 MILLIMETRES (native F9 units per UBX-22008968 §3.11).
func BuildSurveyInTMODE3(minDurSec uint32, accLimit01mm uint32) []byte {
	pl := make([]byte, 40)
	// version = 0, reserved1 = 0
	// flags: low byte = mode (1 = survey-in)
	pl[2] = 0x01
	pl[3] = 0x00
	binary.LittleEndian.PutUint32(pl[24:28], minDurSec)
	binary.LittleEndian.PutUint32(pl[28:32], accLimit01mm)
	return pl
}

// BuildFixedModeTMODE3 returns a 40-byte CFG-TMODE3 payload that puts an
// F9 receiver into FIXED time mode at the supplied ECEF coordinates.
// ecefX/Y/Z are in cm; ecefXHP/YHP/ZHP are sub-cm high-precision parts
// in 0.1 mm units (from NAV-SVIN). fixedPosAcc01mm is in 0.1 mm units.
// Survey-in fields are zeroed.
func BuildFixedModeTMODE3(ecefXCm, ecefYCm, ecefZCm int32, ecefXHP, ecefYHP, ecefZHP int8, fixedPosAcc01mm uint32) []byte {
	pl := make([]byte, 40)
	// version = 0, reserved1 = 0
	// flags: low byte = mode (2 = fixed)
	pl[2] = 0x02
	pl[3] = 0x00
	binary.LittleEndian.PutUint32(pl[4:8], uint32(ecefXCm))
	binary.LittleEndian.PutUint32(pl[8:12], uint32(ecefYCm))
	binary.LittleEndian.PutUint32(pl[12:16], uint32(ecefZCm))
	pl[16] = byte(ecefXHP)
	pl[17] = byte(ecefYHP)
	pl[18] = byte(ecefZHP)
	// reserved2 = 0
	binary.LittleEndian.PutUint32(pl[20:24], fixedPosAcc01mm)
	// svinMinDur/svinAccLimit = 0 (unused in fixed mode)
	return pl
}

// FixedModeFrameFromSVIN builds a TMODE2/TMODE3 frame that locks the
// receiver into fixed mode at the position captured by a converged
// survey-in. The caller is expected to have already verified st.Valid
// && !st.Active — this function doesn't enforce that (so it can also be
// used to switch into fixed mode from a running survey if you really
// want to, e.g. after a previous session already converged).
//
// fixedPosAcc is derived from st.MeanAccMeters:
//   - M8: metres × 1000 → mm
//   - F9: metres × 10000 → 0.1 mm
func FixedModeFrameFromSVIN(gen Generation, st SVINStatus) (Frame, error) {
	switch gen {
	case GenM8:
		accMM := uint32(st.MeanAccMeters * 1000)
		if accMM == 0 {
			accMM = 1 // floor at 1 mm to avoid "accept anywhere" semantics
		}
		return Frame{
			Class:   ClassCFG,
			ID:      IDCfgTMODE2,
			Payload: BuildFixedModeTMODE2(st.MeanXCm, st.MeanYCm, st.MeanZCm, accMM),
		}, nil
	case GenF9:
		acc01mm := uint32(st.MeanAccMeters * 10000)
		if acc01mm == 0 {
			acc01mm = 1
		}
		return Frame{
			Class:   ClassCFG,
			ID:      IDCfgTMODE3,
			Payload: BuildFixedModeTMODE3(st.MeanXCm, st.MeanYCm, st.MeanZCm, st.MeanXHP, st.MeanYHP, st.MeanZHP, acc01mm),
		}, nil
	default:
		return Frame{}, fmt.Errorf("ubx: cannot build fixed-mode frame for generation %s", gen)
	}
}

// ExtractFixedModeECEF pulls the fixed-mode ECEF coordinates out of a
// CFG-TMODE2 or CFG-TMODE3 payload. Only meaningful when the payload's
// mode field is 2 (fixed); returns ok=false for truncated payloads.
// Coordinates are in cm. Used by the gpsmon collector so the dashboard
// can show where the receiver is locked to — TIM-SVIN returns zeros in
// fixed mode because it only describes survey-in progress.
//
// For TMODE3 (F9), the sub-cm high-precision parts are also returned.
// For TMODE2 (M8) those outputs are always zero.
// fixedPosAccMM is returned in millimetres regardless of the receiver
// generation (TMODE2 is already mm; TMODE3 is 0.1 mm and gets converted).
func ExtractFixedModeECEF(gen Generation, payload []byte) (xCm, yCm, zCm int32, xHP, yHP, zHP int8, fixedPosAccMM uint32, ok bool) {
	switch gen {
	case GenM8:
		// CFG-TMODE2: ecefX@4, ecefY@8, ecefZ@12 (i4 cm),
		//             fixedPosAcc@16 (u4 mm)
		if len(payload) < 20 {
			return 0, 0, 0, 0, 0, 0, 0, false
		}
		return int32(binary.LittleEndian.Uint32(payload[4:8])),
			int32(binary.LittleEndian.Uint32(payload[8:12])),
			int32(binary.LittleEndian.Uint32(payload[12:16])),
			0, 0, 0,
			binary.LittleEndian.Uint32(payload[16:20]),
			true
	case GenF9:
		// CFG-TMODE3: ecefX@4, ecefY@8, ecefZ@12 (i4 cm),
		//             HP parts@16-18 (i1 0.1mm), fixedPosAcc@20 (u4 0.1mm)
		if len(payload) < 24 {
			return 0, 0, 0, 0, 0, 0, 0, false
		}
		return int32(binary.LittleEndian.Uint32(payload[4:8])),
			int32(binary.LittleEndian.Uint32(payload[8:12])),
			int32(binary.LittleEndian.Uint32(payload[12:16])),
			int8(payload[16]), int8(payload[17]), int8(payload[18]),
			binary.LittleEndian.Uint32(payload[20:24]) / 10, // 0.1 mm → mm
			true
	}
	return 0, 0, 0, 0, 0, 0, 0, false
}

// TMODEMode extracts the timeMode/flags.mode field from a CFG-TMODE2 or
// CFG-TMODE3 payload. Returns 0=disabled, 1=survey-in, 2=fixed, or -1
// on a malformed payload. Used by status and the "already in fixed mode?"
// check in the deploy pipeline.
func TMODEMode(gen Generation, payload []byte) int {
	switch gen {
	case GenM8:
		if len(payload) < 1 {
			return -1
		}
		return int(payload[0])
	case GenF9:
		if len(payload) < 3 {
			return -1
		}
		// mode lives in the low 3 bits of the 2-byte flags field at
		// offset 2.
		return int(payload[2] & 0x07)
	}
	return -1
}

// SurveyInFrame returns the appropriate CFG-TMODE2 or CFG-TMODE3 frame for
// the given generation, populated with survey-in mode and the supplied
// thresholds. accuracyMeters is in human-friendly metres; this function
// converts to the correct native units per generation (mm for M8, 0.1 mm
// for F9). THIS IS THE ONLY CORRECT WAY TO BUILD A SURVEY-IN FRAME — do
// not call the lower-level BuildSurveyInTMODE* helpers directly from
// caller code unless you know exactly what unit you are passing.
func SurveyInFrame(g Generation, minDurSec uint32, accuracyMeters float64) (Frame, error) {
	switch g {
	case GenM8:
		// M8 TMODE2 svinAccLimit is in mm → metres × 1000.
		accLimitMM := metersToUint(accuracyMeters, 1000)
		return Frame{
			Class:   ClassCFG,
			ID:      IDCfgTMODE2,
			Payload: BuildSurveyInTMODE2(minDurSec, accLimitMM),
		}, nil
	case GenF9:
		// F9 TMODE3 svinAccLimit is in 0.1 mm → metres × 10000.
		accLimit01mm := metersToUint(accuracyMeters, 10000)
		return Frame{
			Class:   ClassCFG,
			ID:      IDCfgTMODE3,
			Payload: BuildSurveyInTMODE3(minDurSec, accLimit01mm),
		}, nil
	default:
		return Frame{}, fmt.Errorf("ubx: cannot build survey-in for generation %s", g)
	}
}

// metersToUint converts a metres value to the supplied native-unit scale
// (e.g. 1000 for mm, 10000 for 0.1 mm), clamping negatives to zero.
func metersToUint(m float64, scale float64) uint32 {
	if m < 0 {
		return 0
	}
	return uint32(m * scale)
}

// RestoreFrame returns a CFG-TMODE2 or CFG-TMODE3 frame that writes the
// cached payload back to the receiver verbatim. Used by the rollback handler.
func RestoreFrame(p TMODEPayload) (Frame, error) {
	switch p.Generation {
	case GenM8:
		if len(p.Raw) != 28 {
			return Frame{}, fmt.Errorf("ubx: TMODE2 payload must be 28 bytes, got %d", len(p.Raw))
		}
		return Frame{Class: ClassCFG, ID: IDCfgTMODE2, Payload: append([]byte(nil), p.Raw...)}, nil
	case GenF9:
		if len(p.Raw) != 40 {
			return Frame{}, fmt.Errorf("ubx: TMODE3 payload must be 40 bytes, got %d", len(p.Raw))
		}
		return Frame{Class: ClassCFG, ID: IDCfgTMODE3, Payload: append([]byte(nil), p.Raw...)}, nil
	default:
		return Frame{}, fmt.Errorf("ubx: cannot restore TMODE for generation %s", p.Generation)
	}
}

// PollTMODEFrame returns the empty-payload poll frame appropriate for the
// generation. Sending this and waiting for a matching response gives us the
// current TMODE config (which we cache for rollback).
func PollTMODEFrame(g Generation) (Frame, error) {
	switch g {
	case GenM8:
		return PollFrame(ClassCFG, IDCfgTMODE2), nil
	case GenF9:
		return PollFrame(ClassCFG, IDCfgTMODE3), nil
	default:
		return Frame{}, fmt.Errorf("ubx: cannot poll TMODE for generation %s", g)
	}
}

// SVINPollFrame returns the empty-payload poll frame for survey-in status.
func SVINPollFrame(g Generation) (Frame, error) {
	switch g {
	case GenM8:
		return PollFrame(ClassTIM, IDTimSVIN), nil
	case GenF9:
		return PollFrame(ClassNAV, IDNavSVIN), nil
	default:
		return Frame{}, fmt.Errorf("ubx: cannot build SVIN poll for generation %s", g)
	}
}

// SVINStatus is the parsed survey-in progress payload.
type SVINStatus struct {
	DurationSec   uint32
	Observations  uint32
	MeanAccMeters float64
	Active        bool
	Valid         bool
	// ECEF position of the surveyed mean, in cm. Available on both
	// TIM-SVIN (M8) and NAV-SVIN (F9). Used to switch into fixed mode
	// with the just-surveyed coordinates.
	MeanXCm int32
	MeanYCm int32
	MeanZCm int32
	// High-precision sub-cm parts from NAV-SVIN (F9 only). Units are
	// 0.1 mm. For M8 these are always zero.
	MeanXHP int8
	MeanYHP int8
	MeanZHP int8
}

// ParseSVIN decodes a TIM-SVIN (M8) or NAV-SVIN (F9) payload into a
// generation-agnostic status struct.
func ParseSVIN(g Generation, payload []byte) (SVINStatus, error) {
	switch g {
	case GenM8:
		// TIM-SVIN: 28 bytes — u-blox UBX-13003221 §32.20.5.
		//   0  u32 dur
		//   4  i32 meanX (cm)
		//   8  i32 meanY (cm)
		//  12  i32 meanZ (cm)
		//  16  u32 meanV (mm^2, 3D variance)
		//  20  u32 obs
		//  24  u8  valid
		//  25  u8  active
		//  26  u8[2] reserved
		if len(payload) < 28 {
			return SVINStatus{}, fmt.Errorf("ubx: TIM-SVIN payload too short: %d", len(payload))
		}
		dur := binary.LittleEndian.Uint32(payload[0:4])
		meanX := int32(binary.LittleEndian.Uint32(payload[4:8]))
		meanY := int32(binary.LittleEndian.Uint32(payload[8:12]))
		meanZ := int32(binary.LittleEndian.Uint32(payload[12:16]))
		meanV := binary.LittleEndian.Uint32(payload[16:20])
		obs := binary.LittleEndian.Uint32(payload[20:24])
		valid := payload[24] != 0
		active := payload[25] != 0
		// meanV is variance in mm^2; report sqrt as accuracy in metres.
		acc := 0.0
		if meanV > 0 {
			acc = math.Sqrt(float64(meanV)) / 1000.0
		}
		return SVINStatus{
			DurationSec:   dur,
			Observations:  obs,
			MeanAccMeters: acc,
			Active:        active,
			Valid:         valid,
			MeanXCm:       meanX,
			MeanYCm:       meanY,
			MeanZCm:       meanZ,
		}, nil

	case GenF9:
		// NAV-SVIN: 40 bytes — u-blox F9 HPG Interface Description.
		//   0  u1  version
		//   1  u1[3] reserved1
		//   4  u4  iTOW (ms)
		//   8  u4  dur (s)
		//  12  i4  meanX (cm)
		//  16  i4  meanY (cm)
		//  20  i4  meanZ (cm)
		//  24  i1  meanXHP (0.1 mm)
		//  25  i1  meanYHP (0.1 mm)
		//  26  i1  meanZHP (0.1 mm)
		//  27  u1  reserved2
		//  28  u4  meanAcc (0.1 mm)
		//  32  u4  obs
		//  36  u1  valid
		//  37  u1  active
		//  38  u1[2] reserved3
		if len(payload) < 40 {
			return SVINStatus{}, fmt.Errorf("ubx: NAV-SVIN payload too short: %d", len(payload))
		}
		dur := binary.LittleEndian.Uint32(payload[8:12])
		meanX := int32(binary.LittleEndian.Uint32(payload[12:16]))
		meanY := int32(binary.LittleEndian.Uint32(payload[16:20]))
		meanZ := int32(binary.LittleEndian.Uint32(payload[20:24]))
		meanXHP := int8(payload[24])
		meanYHP := int8(payload[25])
		meanZHP := int8(payload[26])
		meanAcc01mm := binary.LittleEndian.Uint32(payload[28:32])
		obs := binary.LittleEndian.Uint32(payload[32:36])
		valid := payload[36] != 0
		active := payload[37] != 0
		return SVINStatus{
			DurationSec:   dur,
			Observations:  obs,
			MeanAccMeters: float64(meanAcc01mm) / 10000.0, // 0.1 mm → m
			Active:        active,
			Valid:         valid,
			MeanXCm:       meanX,
			MeanYCm:       meanY,
			MeanZCm:       meanZ,
			MeanXHP:       meanXHP,
			MeanYHP:       meanYHP,
			MeanZHP:       meanZHP,
		}, nil
	}
	return SVINStatus{}, fmt.Errorf("ubx: cannot parse SVIN for generation %s", g)
}

// compareTMODEByMode compares two CFG-TMODE2 (M8) or CFG-TMODE3 (F9)
// payloads, restricting the comparison to the fields the receiver firmware
// actually applies for the active timeMode. Bytes that the firmware ignores
// in the current mode (e.g. ECEF coordinates and fixedPosAcc in Survey-in
// mode, svinMinDur and svinAccLimit in Fixed mode) are skipped — the M8
// retains the prior-mode values in those slots, so insisting on full
// byte-equality would reject correctly-applied mode-switch writes.
//
// `expected` defines the intended state; the comparator extracts the
// expected timeMode from it and rejects any reply whose timeMode does not
// match. Beyond the timeMode check, only the mode-relevant field set is
// byte-compared. See `tmode.go` payload comments for the field layouts.
func compareTMODEByMode(gen Generation, got, expected []byte) error {
	expectedMode := TMODEMode(gen, expected)
	if expectedMode < 0 {
		return fmt.Errorf("malformed expected payload (length %d)", len(expected))
	}
	gotMode := TMODEMode(gen, got)
	if gotMode < 0 {
		return fmt.Errorf("malformed receiver payload (length %d)", len(got))
	}
	if gotMode != expectedMode {
		return fmt.Errorf("timeMode = %d, expected %d", gotMode, expectedMode)
	}
	switch gen {
	case GenM8:
		if len(got) != 28 || len(expected) != 28 {
			return fmt.Errorf("TMODE2 payload must be 28 bytes (got %d, expected %d)",
				len(got), len(expected))
		}
		return compareTMODE2ByMode(expectedMode, got, expected)
	case GenF9:
		if len(got) != 40 || len(expected) != 40 {
			return fmt.Errorf("TMODE3 payload must be 40 bytes (got %d, expected %d)",
				len(got), len(expected))
		}
		return compareTMODE3ByMode(expectedMode, got, expected)
	}
	return fmt.Errorf("ubx: cannot compare TMODE for generation %s", gen)
}

// compareTMODE2ByMode is the per-mode comparator for M8 CFG-TMODE2.
//
//   - Disabled (timeMode=0): only the timeMode byte matters; the rest of
//     the payload is unused by the firmware.
//   - Survey-in (timeMode=1): the receiver applies svinMinDur (bytes
//     20..24) and svinAccLimit (bytes 24..28). ECEF and fixedPosAcc are
//     ignored — typically retained from a prior Fixed-mode config.
//   - Fixed (timeMode=2): flags (byte 2 bit0 selects LLA vs ECEF), the
//     ECEF coordinates (bytes 4..16), and fixedPosAcc (bytes 16..20) are
//     applied. svinMinDur/svinAccLimit are ignored.
func compareTMODE2ByMode(mode int, got, expected []byte) error {
	switch mode {
	case 0:
		return nil
	case 1:
		if !bytesEqual(got[20:28], expected[20:28]) {
			return fmt.Errorf("svin fields differ: got %s, expected %s",
				hex.EncodeToString(got[20:28]), hex.EncodeToString(expected[20:28]))
		}
		return nil
	case 2:
		// flags (1..4) decide LLA vs ECEF; coords (4..16); fixedPosAcc (16..20).
		if !bytesEqual(got[1:20], expected[1:20]) {
			return fmt.Errorf("fixed-mode fields differ: got %s, expected %s",
				hex.EncodeToString(got[1:20]), hex.EncodeToString(expected[1:20]))
		}
		return nil
	}
	return fmt.Errorf("unsupported timeMode %d", mode)
}

// compareTMODE3ByMode is the per-mode comparator for F9 CFG-TMODE3.
// Same semantics as the M8 variant; the field offsets differ because
// TMODE3 carries a version byte at offset 0, the mode in a 2-byte flags
// field at offset 2, sub-cm high-precision ECEF parts at bytes 16..19,
// and svin fields at bytes 24..32.
func compareTMODE3ByMode(mode int, got, expected []byte) error {
	switch mode {
	case 0:
		return nil
	case 1:
		// svinMinDur (24..28), svinAccLimit (28..32).
		if !bytesEqual(got[24:32], expected[24:32]) {
			return fmt.Errorf("svin fields differ: got %s, expected %s",
				hex.EncodeToString(got[24:32]), hex.EncodeToString(expected[24:32]))
		}
		return nil
	case 2:
		// flags high byte (byte 3) carries the lla bit — required for
		// the receiver to interpret bytes 4..16 as ECEF vs LLH.
		if got[3] != expected[3] {
			return fmt.Errorf("flags high byte differs: got %02x, expected %02x",
				got[3], expected[3])
		}
		if !bytesEqual(got[4:16], expected[4:16]) {
			return fmt.Errorf("ECEF coords differ: got %s, expected %s",
				hex.EncodeToString(got[4:16]), hex.EncodeToString(expected[4:16]))
		}
		if !bytesEqual(got[16:19], expected[16:19]) {
			return fmt.Errorf("ECEF HP parts differ: got %s, expected %s",
				hex.EncodeToString(got[16:19]), hex.EncodeToString(expected[16:19]))
		}
		if !bytesEqual(got[20:24], expected[20:24]) {
			return fmt.Errorf("fixedPosAcc differs: got %s, expected %s",
				hex.EncodeToString(got[20:24]), hex.EncodeToString(expected[20:24]))
		}
		return nil
	}
	return fmt.Errorf("unsupported timeMode %d", mode)
}
