package gps

import (
	"context"
	"encoding/binary"
	"fmt"
)

// CFG-TP5 (0x06 0x31) helpers. See u-blox 8/M8 Receiver Description
// (UBX-13003221) §32.10.38. Payload is 32 bytes for version 1.
//
// Layout (32 bytes, version 1):
//
//	 0 U1 tpIdx               time pulse index (0=TIMEPULSE)
//	 1 U1 version             0x01
//	 2 U1[2] reserved1
//	 4 I2 antCableDelay       ns
//	 6 I2 rfGroupDelay        ns
//	 8 U4 freqPeriod          Hz or us (depends on isFreq flag)
//	12 U4 freqPeriodLock      Hz or us (used when locked to GNSS)
//	16 U4 pulseLenRatio       us or 2^-32 (depends on isLength flag)
//	20 U4 pulseLenRatioLock   us or 2^-32 (used when locked to GNSS)
//	24 I4 userConfigDelay     ns
//	28 X4 flags

// TP5 flag bits (u-blox UBX-13003221 §32.10.38).
const (
	tp5FlagActive         = 1 << 0 // enable time pulse
	tp5FlagLockGpsFreq    = 1 << 1 // synchronize to GNSS time when valid
	tp5FlagLockedOtherSet = 1 << 2 // use *Lock fields when GNSS time is valid
	tp5FlagIsFreq         = 1 << 3 // freqPeriod is interpreted as frequency (Hz)
	tp5FlagIsLength       = 1 << 4 // pulseLenRatio is interpreted as length (us)
	tp5FlagAlignToTow     = 1 << 5 // align pulse to top of second
	tp5FlagPolarityRising = 1 << 6 // rising edge at top of second (0=falling)
	tp5FlagGridGPS        = 1 << 7 // 0=UTC grid, 1=GPS grid
)

// BuildTP5_1HzUTC returns a 32-byte CFG-TP5 payload that configures
// timepulse 0 for a 1 Hz / 50% duty / UTC-aligned / rising-edge PPS
// signal. This is the known-good config for MLAT timestamping.
//
// Specifically:
//   - freqPeriod=1 (with isFreq set → 1 Hz)
//   - pulseLenRatio=500000 (with isLength set → 500 ms = 50% duty)
//   - flags: active, lockGpsFreq, lockedOtherSet, isFreq, isLength,
//     alignToTow, polarity=rising, grid=UTC (gridGPS bit NOT set)
func BuildTP5_1HzUTC() []byte {
	pl := make([]byte, 32)
	pl[0] = 0x00 // tpIdx = TIMEPULSE
	pl[1] = 0x01 // version
	// reserved1, antCableDelay, rfGroupDelay stay zero
	binary.LittleEndian.PutUint32(pl[8:12], 1)       // freqPeriod     = 1 Hz
	binary.LittleEndian.PutUint32(pl[12:16], 1)      // freqPeriodLock = 1 Hz
	binary.LittleEndian.PutUint32(pl[16:20], 500000) // pulseLenRatio     = 500 ms
	binary.LittleEndian.PutUint32(pl[20:24], 500000) // pulseLenRatioLock = 500 ms
	// userConfigDelay = 0
	flags := uint32(tp5FlagActive |
		tp5FlagLockGpsFreq |
		tp5FlagLockedOtherSet |
		tp5FlagIsFreq |
		tp5FlagIsLength |
		tp5FlagAlignToTow |
		tp5FlagPolarityRising)
	// gridGPS bit stays 0 → UTC grid
	binary.LittleEndian.PutUint32(pl[28:32], flags)
	return pl
}

// WriteTP5_1HzUTC applies the known-good 1 Hz UTC PPS config and verifies
// by reading CFG-TP5 back. Verify compares the on-wire fields we set;
// antCableDelay and rfGroupDelay are NOT compared because the receiver
// may leave them at previously-configured non-zero values (we only set
// the frequency/duty/alignment portion of the record).
func WriteTP5_1HzUTC(ctx context.Context, dc *DeviceClient) error {
	payload := BuildTP5_1HzUTC()
	frame := Frame{Class: ClassCFG, ID: IDCfgTP5, Payload: payload}
	verify := func(got []byte) error {
		if len(got) < 32 {
			return fmt.Errorf("CFG-TP5 response too short: %d bytes", len(got))
		}
		if got[0] != 0 {
			return fmt.Errorf("tpIdx = %d, expected 0", got[0])
		}
		gotFreq := binary.LittleEndian.Uint32(got[8:12])
		gotPulse := binary.LittleEndian.Uint32(got[16:20])
		gotFlags := binary.LittleEndian.Uint32(got[28:32])
		wantFreq := binary.LittleEndian.Uint32(payload[8:12])
		wantPulse := binary.LittleEndian.Uint32(payload[16:20])
		wantFlags := binary.LittleEndian.Uint32(payload[28:32])
		if gotFreq != wantFreq {
			return fmt.Errorf("freqPeriod = %d, expected %d", gotFreq, wantFreq)
		}
		if gotPulse != wantPulse {
			return fmt.Errorf("pulseLenRatio = %d, expected %d", gotPulse, wantPulse)
		}
		// Compare only the low byte of flags — that's where all the
		// fields we care about live (active, lockGpsFreq, isFreq,
		// isLength, alignToTow, polarity, grid). Higher bytes carry
		// syncMode etc. that newer firmware may set and we don't
		// want to clobber on verify.
		if gotFlags&0xFF != wantFlags&0xFF {
			return fmt.Errorf("flags low byte = 0x%02x, expected 0x%02x",
				gotFlags&0xFF, wantFlags&0xFF)
		}
		return nil
	}
	return WriteAndVerify(ctx, dc, frame, ClassCFG, IDCfgTP5, verify)
}

// PollTP5 polls and returns the raw 32-byte CFG-TP5 payload for timepulse 0.
func PollTP5(ctx context.Context, dc *DeviceClient) ([]byte, error) {
	// Send CFG-TP5 poll with 1-byte payload containing tpIdx=0 to
	// explicitly request timepulse 0. (The empty-payload form returns
	// the older TP message format on some firmwares.)
	pollFrame := Frame{Class: ClassCFG, ID: IDCfgTP5, Payload: []byte{0x00}}
	resp, err := dc.SendAndAwaitResponse(ctx, pollFrame, ClassCFG, IDCfgTP5)
	if err != nil {
		return nil, fmt.Errorf("CFG-TP5 poll: %w", err)
	}
	return append([]byte(nil), resp.Payload...), nil
}

// TP5Summary is a human-readable snapshot of the CFG-TP5 fields the status
// subcommand cares about.
type TP5Summary struct {
	TpIdx            uint8
	FreqPeriod       uint32
	FreqPeriodLock   uint32
	PulseLenRatio    uint32
	Active           bool
	LockGpsFreq      bool
	IsFreq           bool
	IsLength         bool
	AlignToTow       bool
	RisingAtTop      bool
	GridUTC          bool
}

// ParseTP5 decodes the subset of CFG-TP5 used for status reporting.
func ParseTP5(payload []byte) (TP5Summary, error) {
	if len(payload) < 32 {
		return TP5Summary{}, fmt.Errorf("CFG-TP5 payload too short: %d", len(payload))
	}
	flags := binary.LittleEndian.Uint32(payload[28:32])
	return TP5Summary{
		TpIdx:          payload[0],
		FreqPeriod:     binary.LittleEndian.Uint32(payload[8:12]),
		FreqPeriodLock: binary.LittleEndian.Uint32(payload[12:16]),
		PulseLenRatio:  binary.LittleEndian.Uint32(payload[16:20]),
		Active:         flags&tp5FlagActive != 0,
		LockGpsFreq:    flags&tp5FlagLockGpsFreq != 0,
		IsFreq:         flags&tp5FlagIsFreq != 0,
		IsLength:       flags&tp5FlagIsLength != 0,
		AlignToTow:     flags&tp5FlagAlignToTow != 0,
		RisingAtTop:    flags&tp5FlagPolarityRising != 0,
		GridUTC:        flags&tp5FlagGridGPS == 0,
	}, nil
}
