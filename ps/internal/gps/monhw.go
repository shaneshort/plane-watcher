package gps

import (
	"context"
	"encoding/binary"
	"fmt"
)

// UBX-MON-HW (0x0A 0x09) parser. Layout from u-blox 8 / u-blox M8 Receiver
// Description (UBX-13003221) §32.16.4. The payload is 60 bytes; we only
// decode the subset the dashboard needs (antenna state, interference
// indicators, automatic gain control).

// Antenna supervisor states (MON-HW.aStatus).
const (
	AntennaStatusInit     uint8 = 0
	AntennaStatusDontKnow uint8 = 1
	AntennaStatusOK       uint8 = 2
	AntennaStatusShort    uint8 = 3
	AntennaStatusOpen     uint8 = 4
)

// Antenna power states (MON-HW.aPower).
const (
	AntennaPowerOff      uint8 = 0
	AntennaPowerOn       uint8 = 1
	AntennaPowerDontKnow uint8 = 2
)

// Jamming state (MON-HW.flags bits 2..3). The value tells the operator
// whether the receiver's interference monitor has detected jamming.
const (
	JammingStateUnknown  uint8 = 0 // feature disabled or unavailable
	JammingStateOK       uint8 = 1 // no significant jamming
	JammingStateWarning  uint8 = 2 // interference visible, fix still OK
	JammingStateCritical uint8 = 3 // interference visible, no fix
)

// HWStatus is the subset of UBX-MON-HW the dashboard cares about.
type HWStatus struct {
	// NoisePerMS is the noise level as measured by the GNSS core.
	// Higher numbers mean noisier reception.
	NoisePerMS uint16
	// AGCCnt is the automatic gain control monitor, range 0..8191 (100%).
	AGCCnt uint16
	// AntennaStatus — see AntennaStatus* constants.
	AntennaStatus uint8
	// AntennaPower — see AntennaPower* constants.
	AntennaPower uint8
	// JammingState — extracted from flags bits 2..3. See
	// JammingState* constants. Note: this field is deprecated on
	// newer firmware in favour of UBX-SEC-SIG, but the LEA-M8T still
	// emits it.
	JammingState uint8
	// RTCCalibrated is true if the RTC has been calibrated
	// (flags bit 0). Useful as a "receiver has been running long
	// enough to trust" heuristic.
	RTCCalibrated bool
	// CWSuppression is the continuous-wave jamming suppression level,
	// range 0..255 (0 = no CW jamming detected, 255 = strong).
	CWSuppression uint8
}

// ParseMonHW decodes a UBX-MON-HW payload.
func ParseMonHW(payload []byte) (HWStatus, error) {
	if len(payload) < 60 {
		return HWStatus{}, fmt.Errorf("ubx: MON-HW payload too short: %d bytes (need 60)", len(payload))
	}
	flags := payload[22]
	return HWStatus{
		NoisePerMS:    binary.LittleEndian.Uint16(payload[16:18]),
		AGCCnt:        binary.LittleEndian.Uint16(payload[18:20]),
		AntennaStatus: payload[20],
		AntennaPower:  payload[21],
		JammingState:  (flags >> 2) & 0x03,
		RTCCalibrated: flags&0x01 != 0,
		CWSuppression: payload[45],
	}, nil
}

// PollMonHW polls UBX-MON-HW and returns a parsed HWStatus.
func PollMonHW(ctx context.Context, dc *DeviceClient) (HWStatus, error) {
	poll := PollFrame(ClassMON, IDMonHW)
	resp, err := dc.SendAndAwaitResponse(ctx, poll, ClassMON, IDMonHW)
	if err != nil {
		return HWStatus{}, fmt.Errorf("MON-HW poll: %w", err)
	}
	return ParseMonHW(resp.Payload)
}

// AntennaStatusName returns a human-readable name for an antenna status value.
func AntennaStatusName(s uint8) string {
	switch s {
	case AntennaStatusInit:
		return "init"
	case AntennaStatusDontKnow:
		return "unknown"
	case AntennaStatusOK:
		return "ok"
	case AntennaStatusShort:
		return "short"
	case AntennaStatusOpen:
		return "open"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// AntennaPowerName returns a human-readable name for an antenna power value.
func AntennaPowerName(p uint8) string {
	switch p {
	case AntennaPowerOff:
		return "off"
	case AntennaPowerOn:
		return "on"
	case AntennaPowerDontKnow:
		return "unknown"
	default:
		return fmt.Sprintf("unknown(%d)", p)
	}
}

// JammingStateName returns a human-readable name for a jamming state value.
func JammingStateName(s uint8) string {
	switch s {
	case JammingStateUnknown:
		return "unknown"
	case JammingStateOK:
		return "ok"
	case JammingStateWarning:
		return "warning"
	case JammingStateCritical:
		return "critical"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}
