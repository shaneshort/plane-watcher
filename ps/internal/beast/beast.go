package beast

import (
	"encoding/binary"
	"math"

	"github.com/plane-watcher/plane-feeder/internal/pps"
	"github.com/plane-watcher/plane-feeder/internal/regs"
)

// OutputMode determines the timestamp format and Radarcape personality.
type OutputMode int

const (
	// ModeBeast12MHz is pure Beast: 12 MHz freerunning timestamps, no status frames.
	ModeBeast12MHz OutputMode = iota
	// ModeRadarcapeGPS is Radarcape with GPS: 1 GHz secs+nanos, sync bit set on data frames.
	ModeRadarcapeGPS
	// ModeRadarcapeLegacy12MHz is Radarcape without GPS: 12 MHz timestamps, still sends 0x34.
	ModeRadarcapeLegacy12MHz
)

const (
	Escape       = 0x1A
	TypeShort    = 0x32 // Mode-S short (7 bytes)
	TypeLong     = 0x33 // Mode-S long (14 bytes)
	TypeStatus   = 0x34 // Radarcape status (14 bytes)
	TypePosition = 0x35 // Radarcape position (21 bytes, no timestamp/signal)

	fpgaCounterHz = 100_000_000 // 100 MHz FPGA timestamp counter clock

	// settingsBinary is Beast binary output protocol (bit 0).
	settingsBinary = 0x01
	// settingsGPSTimestamp enables GPS timestamp mode (bit 4).
	settingsGPSTimestamp = 0x10
	// gpsStatusSynced is the GPS status byte for GPS-synced mode.
	// 0xFF matches observed real Radarcape behaviour. See design doc
	// "Documented Decision: GPS Status Byte" in
	// docs/plans/2026-04-01-radarcape-protocol-compliance.md for full rationale.
	gpsStatusSynced = 0xFF
	// gpsStatusUnsynced is the GPS status byte when GPS is not available.
	gpsStatusUnsynced = 0x00
)

// EncodeData produces a 0x32 or 0x33 Beast data frame.
// Mode determines timestamp format. For ModeRadarcapeGPS, ref must be non-nil
// with valid timing fields — this is a programmer error if violated.
func EncodeData(mode OutputMode, msg regs.Message, ref *pps.ClockRef) []byte {
	ts48 := encodeTimestamp(mode, msg.TOA, ref)
	signal := regs.RplToSignalByte(msg.RPL)

	msgType := TypeLong
	if msg.Len == 7 {
		msgType = TypeShort
	}

	payload := make([]byte, 0, 1+6+1+msg.Len)
	payload = append(payload, byte(msgType))
	payload = appendTimestamp(payload, ts48)
	payload = append(payload, signal)
	payload = append(payload, msg.Bytes[:msg.Len]...)

	return escapeFrame(payload)
}

// EncodeV2 is the legacy encoder. Deprecated: use EncodeData with an explicit mode.
func EncodeV2(msg regs.Message, ref *pps.ClockRef) []byte {
	if ref != nil && ref.GpsSync {
		return EncodeData(ModeRadarcapeGPS, msg, ref)
	}
	return EncodeData(ModeBeast12MHz, msg, nil)
}

// EncodeStatus produces a 0x34 Radarcape status frame.
//
// timestampMode controls how the 6-byte timestamp is encoded. reportedMode
// determines the settings byte (the mode being announced to clients). During
// mode transitions these differ: timestamp uses the old mode so mlat-client
// can parse it, settings byte advertises the new mode.
//
// toa is the current FPGA counter value for the timestamp. ref is required
// when timestampMode is ModeRadarcapeGPS. ppsDelta is the PPS timing offset
// byte — currently always 0 (see design doc "PPS delta byte: UNRESOLVED"
// section). Parameter retained for future use.
func EncodeStatus(timestampMode, reportedMode OutputMode, toa uint64, ref *pps.ClockRef, ppsDelta int8) []byte {
	ts48 := encodeTimestamp(timestampMode, toa, ref)
	// Status frames clear the sync bit (bit 47), matching observed Radarcape behaviour.
	ts48 &^= 1 << 47

	var settings byte = settingsBinary
	var gpsStatus byte = gpsStatusUnsynced
	if reportedMode == ModeRadarcapeGPS {
		settings |= settingsGPSTimestamp
		gpsStatus = gpsStatusSynced
	}

	// Status payload: 14 bytes matching a long message.
	var statusMsg [14]byte
	statusMsg[0] = settings
	statusMsg[1] = byte(ppsDelta)
	statusMsg[2] = gpsStatus
	// Bytes 3-13 are reserved (zero).

	payload := make([]byte, 0, 1+6+1+14)
	payload = append(payload, TypeStatus)
	payload = appendTimestamp(payload, ts48)
	payload = append(payload, 0x00) // signal byte unused for status
	payload = append(payload, statusMsg[:]...)

	return escapeFrame(payload)
}

// EncodePosition produces a 0x35 Radarcape position frame.
// lat, lon in degrees; alt in metres. No timestamp or signal byte — just
// a 21-byte payload with IEEE 754 float32 little-endian coordinates.
func EncodePosition(lat, lon float64, alt float32) []byte {
	var data [21]byte
	// Bytes 0-3: reserved (zero).
	binary.LittleEndian.PutUint32(data[4:8], math.Float32bits(float32(lat)))
	binary.LittleEndian.PutUint32(data[8:12], math.Float32bits(float32(lon)))
	binary.LittleEndian.PutUint32(data[12:16], math.Float32bits(alt))
	// Bytes 16-20: reserved (zero).

	payload := make([]byte, 0, 1+21)
	payload = append(payload, TypePosition)
	payload = append(payload, data[:]...)

	return escapeFrame(payload)
}

// encodeTimestamp produces the 48-bit timestamp for the given mode and FPGA counter value.
func encodeTimestamp(mode OutputMode, toa uint64, ref *pps.ClockRef) uint64 {
	if mode == ModeRadarcapeGPS {
		return encodeGPSTimestamp(toa, ref)
	}
	// ModeBeast12MHz and ModeRadarcapeLegacy12MHz both use 12 MHz.
	q := toa / 25
	r := toa % 25
	return (q*3 + (r*3)/25) & 0xFFFFFFFFFFFF
}

// encodeGPSTimestamp produces the Radarcape GPS 48-bit timestamp.
// Bit 47 = sync, bits 46:30 = seconds of day, bits 29:0 = nanoseconds.
func encodeGPSTimestamp(toa uint64, ref *pps.ClockRef) uint64 {
	secOfDay := uint64(ref.WallUTC.UTC().Hour())*3600 +
		uint64(ref.WallUTC.UTC().Minute())*60 +
		uint64(ref.WallUTC.UTC().Second())

	counterAtPps := ref.CounterAtPps
	tickRate := ref.MeasuredTicks
	if tickRate == 0 {
		tickRate = fpgaCounterHz
	}

	var nanos uint64
	if toa >= counterAtPps {
		delta := toa - counterAtPps
		secOfDay = (secOfDay + delta/tickRate) % 86400
		nanos = (delta % tickRate) * 1_000_000_000 / tickRate
	} else {
		secOfDay = (secOfDay + 86400 - 1) % 86400
		ticksInPrevSec := counterAtPps - toa
		nanos = 1_000_000_000 - ticksInPrevSec*1_000_000_000/tickRate
	}

	ts48 := (secOfDay << 30) | (nanos & 0x3FFFFFFF)
	// Bit 47 (sync bit) is NOT set. mlat-client extracts seconds via
	// timestamp >> 30 without masking, so a set bit 47 adds 131072 to the
	// seconds value, causing timestamp outlier detection. Real Radarcape
	// data frames were observed with sync=False. See design doc
	// "Sync bit (bit 47) in GPS mode" for the full investigation.
	return ts48
}

// appendTimestamp appends a 48-bit timestamp as 6 big-endian bytes.
func appendTimestamp(buf []byte, ts48 uint64) []byte {
	for i := 5; i >= 0; i-- {
		buf = append(buf, byte(ts48>>(i*8)))
	}
	return buf
}

// escapeFrame wraps a payload with the 0x1A leader and doubles any 0x1A in the payload.
func escapeFrame(payload []byte) []byte {
	frame := make([]byte, 0, 1+len(payload)*2)
	frame = append(frame, Escape)
	for _, b := range payload {
		frame = append(frame, b)
		if b == Escape {
			frame = append(frame, Escape)
		}
	}
	return frame
}
