package gps

import (
	"context"
	"encoding/binary"
	"fmt"
)

// UBX-NAV-CLOCK (0x01 0x22) and UBX-NAV-DOP (0x01 0x04) parsers. Layouts
// from u-blox 8 / u-blox M8 Receiver Description (UBX-13003221) §32.17.3
// and §32.17.6 respectively.

// ClockStatus is a parsed UBX-NAV-CLOCK payload. It reports the
// receiver's self-assessment of its clock quality — used in the GPS
// dashboard as the closest UBX analogue to the Trimble "oscillator ppm"
// and "clock accuracy" fields.
type ClockStatus struct {
	ITOW        uint32 // GPS time of week in ms
	ClockBiasNs int32  // clock bias in ns
	ClockDriftNsPerS int32  // clock drift in ns/s
	TimeAccuracyNs   uint32 // time accuracy estimate in ns
	FreqAccuracyPsPerS uint32 // frequency accuracy estimate in ps/s
}

// ParseNavClock decodes a UBX-NAV-CLOCK payload (20 bytes).
func ParseNavClock(payload []byte) (ClockStatus, error) {
	if len(payload) < 20 {
		return ClockStatus{}, fmt.Errorf("ubx: NAV-CLOCK payload too short: %d bytes (need 20)", len(payload))
	}
	return ClockStatus{
		ITOW:               binary.LittleEndian.Uint32(payload[0:4]),
		ClockBiasNs:        int32(binary.LittleEndian.Uint32(payload[4:8])),
		ClockDriftNsPerS:   int32(binary.LittleEndian.Uint32(payload[8:12])),
		TimeAccuracyNs:     binary.LittleEndian.Uint32(payload[12:16]),
		FreqAccuracyPsPerS: binary.LittleEndian.Uint32(payload[16:20]),
	}, nil
}

// PollNavClock polls UBX-NAV-CLOCK and returns a parsed ClockStatus.
func PollNavClock(ctx context.Context, dc *DeviceClient) (ClockStatus, error) {
	poll := PollFrame(ClassNAV, IDNavClock)
	resp, err := dc.SendAndAwaitResponse(ctx, poll, ClassNAV, IDNavClock)
	if err != nil {
		return ClockStatus{}, fmt.Errorf("NAV-CLOCK poll: %w", err)
	}
	return ParseNavClock(resp.Payload)
}

// DOPStatus is a parsed UBX-NAV-DOP payload. All DOP values are returned
// as floats with the ×0.01 wire scaling already applied — the receiver
// sends them as uint16.
type DOPStatus struct {
	ITOW uint32  // GPS time of week in ms
	GDOP float64 // geometric DOP
	PDOP float64 // position DOP
	TDOP float64 // time DOP
	VDOP float64 // vertical DOP
	HDOP float64 // horizontal DOP
	NDOP float64 // northing DOP
	EDOP float64 // easting DOP
}

// ParseNavDOP decodes a UBX-NAV-DOP payload (18 bytes).
func ParseNavDOP(payload []byte) (DOPStatus, error) {
	if len(payload) < 18 {
		return DOPStatus{}, fmt.Errorf("ubx: NAV-DOP payload too short: %d bytes (need 18)", len(payload))
	}
	scale := func(off int) float64 {
		return float64(binary.LittleEndian.Uint16(payload[off:off+2])) * 0.01
	}
	return DOPStatus{
		ITOW: binary.LittleEndian.Uint32(payload[0:4]),
		GDOP: scale(4),
		PDOP: scale(6),
		TDOP: scale(8),
		VDOP: scale(10),
		HDOP: scale(12),
		NDOP: scale(14),
		EDOP: scale(16),
	}, nil
}

// PollNavDOP polls UBX-NAV-DOP and returns a parsed DOPStatus.
func PollNavDOP(ctx context.Context, dc *DeviceClient) (DOPStatus, error) {
	poll := PollFrame(ClassNAV, IDNavDOP)
	resp, err := dc.SendAndAwaitResponse(ctx, poll, ClassNAV, IDNavDOP)
	if err != nil {
		return DOPStatus{}, fmt.Errorf("NAV-DOP poll: %w", err)
	}
	return ParseNavDOP(resp.Payload)
}
