package gps

import (
	"context"
	"encoding/binary"
	"fmt"
)

// CFG-NAV5 (0x06 0x24) helpers. See u-blox 8/M8 Receiver Description
// (UBX-13003221) §32.10.19. Payload is 36 bytes; the 2-byte mask at the
// start selects which fields are applied on a write — setting only the
// mask bits we care about lets us change one setting without disturbing
// the rest.
//
// Layout (36 bytes):
//
//	 0 X2 mask              which fields to apply
//	 2 U1 dynModel           0=portable, 2=stationary, 3=pedestrian, ...
//	 3 U1 fixMode            1=2D, 2=3D, 3=auto
//	 4 I4 fixedAlt           cm (2D fix)
//	 8 U4 fixedAltVar        cm^2
//	12 I1 minElev            deg
//	13 U1 drLimit            s (reserved)
//	14 U2 pDop               0.1
//	16 U2 tDop               0.1
//	18 U2 pAcc               m
//	20 U2 tAcc               m
//	22 U1 staticHoldThresh   cm/s
//	23 U1 dgnssTimeout       s
//	24 U1 cnoThreshNumSVs
//	25 U1 cnoThresh          dBHz
//	26 U1[2] reserved1
//	28 U2 staticHoldMaxDist  m
//	30 U1 utcStandard
//	31 U1[5] reserved2

// Dynamic model values used by CFG-NAV5.dynModel.
const (
	DynModelPortable   uint8 = 0
	DynModelStationary uint8 = 2
	DynModelPedestrian uint8 = 3
	DynModelAutomotive uint8 = 4
	DynModelSea        uint8 = 5
	DynModelAirborne1g uint8 = 6
)

// CFG-NAV5 mask bits (u-blox UBX-13003221 §32.10.19).
const (
	nav5MaskDyn            = 1 << 0
	nav5MaskMinEl          = 1 << 1
	nav5MaskPosFixMode     = 1 << 2
	nav5MaskPosMask        = 1 << 4
	nav5MaskTimeMask       = 1 << 5
	nav5MaskStaticHoldMask = 1 << 6
	nav5MaskDgpsMask       = 1 << 7
	nav5MaskCnoThreshold   = 1 << 8
	nav5MaskUtc            = 1 << 10
)

// BuildNAV5DynModelOnly returns a 36-byte CFG-NAV5 payload that applies
// ONLY the dynamic model. All other fields are zero but are ignored by
// the receiver because their mask bits are not set.
func BuildNAV5DynModelOnly(dynModel uint8) []byte {
	pl := make([]byte, 36)
	binary.LittleEndian.PutUint16(pl[0:2], nav5MaskDyn)
	pl[2] = dynModel
	return pl
}

// WriteNAV5DynModel applies a dynamic model to the receiver and verifies
// the change by reading CFG-NAV5 back. Uses the shared two-slot verify
// path so stale poll responses cannot falsely satisfy the check.
func WriteNAV5DynModel(ctx context.Context, dc *DeviceClient, dynModel uint8) error {
	frame := Frame{
		Class:   ClassCFG,
		ID:      IDCfgNAV5,
		Payload: BuildNAV5DynModelOnly(dynModel),
	}
	verify := func(got []byte) error {
		if len(got) < 3 {
			return fmt.Errorf("CFG-NAV5 response too short: %d bytes", len(got))
		}
		if got[2] != dynModel {
			return fmt.Errorf("dynModel = %d, expected %d", got[2], dynModel)
		}
		return nil
	}
	return WriteAndVerify(ctx, dc, frame, ClassCFG, IDCfgNAV5, verify)
}

// PollNAV5 polls and returns the raw 36-byte CFG-NAV5 payload.
func PollNAV5(ctx context.Context, dc *DeviceClient) ([]byte, error) {
	pollFrame := PollFrame(ClassCFG, IDCfgNAV5)
	resp, err := dc.SendAndAwaitResponse(ctx, pollFrame, ClassCFG, IDCfgNAV5)
	if err != nil {
		return nil, fmt.Errorf("CFG-NAV5 poll: %w", err)
	}
	return append([]byte(nil), resp.Payload...), nil
}

// NAV5Summary extracts the fields operators usually care about from a
// CFG-NAV5 payload. Used by the status subcommand.
type NAV5Summary struct {
	DynModel    uint8
	FixMode     uint8
	MinElev     int8
	UtcStandard uint8
}

// ParseNAV5 decodes the subset of CFG-NAV5 used for status reporting.
func ParseNAV5(payload []byte) (NAV5Summary, error) {
	if len(payload) < 36 {
		return NAV5Summary{}, fmt.Errorf("CFG-NAV5 payload too short: %d", len(payload))
	}
	return NAV5Summary{
		DynModel:    payload[2],
		FixMode:     payload[3],
		MinElev:     int8(payload[12]),
		UtcStandard: payload[30],
	}, nil
}

// DynModelName returns a human-readable string for a dynModel value.
func DynModelName(dm uint8) string {
	switch dm {
	case DynModelPortable:
		return "portable"
	case DynModelStationary:
		return "stationary"
	case DynModelPedestrian:
		return "pedestrian"
	case DynModelAutomotive:
		return "automotive"
	case DynModelSea:
		return "sea"
	case DynModelAirborne1g:
		return "airborne<1g"
	default:
		return fmt.Sprintf("unknown(%d)", dm)
	}
}
