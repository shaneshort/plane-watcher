package beast

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/pps"
	"github.com/plane-watcher/plane-feeder/internal/regs"
)

// unescapePayload strips the leading 0x1A and reverses escape doubling.
func unescapePayload(frame []byte) []byte {
	if len(frame) == 0 || frame[0] != Escape {
		return frame
	}
	var out []byte
	for i := 1; i < len(frame); i++ {
		out = append(out, frame[i])
		if frame[i] == Escape && i+1 < len(frame) && frame[i+1] == Escape {
			i++
		}
	}
	return out
}

func TestEncodeLongMessage(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000, RPL: 50000}
	for i := range msg.Bytes {
		msg.Bytes[i] = byte(i + 0x80)
	}

	frame := EncodeData(ModeBeast12MHz, msg, nil)

	if frame[0] != 0x1A {
		t.Errorf("start byte: got 0x%02X, want 0x1A", frame[0])
	}
	if frame[1] != TypeLong {
		t.Errorf("type: got 0x%02X, want 0x%02X", frame[1], TypeLong)
	}
	// 1A + type + 6 ts + 1 signal + 14 msg = 23
	if len(frame) != 23 {
		t.Errorf("length: got %d, want 23", len(frame))
	}
	if frame[8] != regs.RplToSignalByte(msg.RPL) {
		t.Errorf("signal: got 0x%02X, want 0x%02X", frame[8], regs.RplToSignalByte(msg.RPL))
	}
}

func TestEncodeShortMessage(t *testing.T) {
	msg := regs.Message{Len: 7}
	frame := EncodeData(ModeBeast12MHz, msg, nil)

	// 1A + type + 6 ts + 1 signal + 7 msg = 16
	if len(frame) != 16 {
		t.Errorf("length: got %d, want 16", len(frame))
	}
	if frame[1] != TypeShort {
		t.Errorf("type: got 0x%02X, want 0x%02X", frame[1], TypeShort)
	}
}

func TestEscaping(t *testing.T) {
	msg := regs.Message{Len: 7}
	msg.Bytes[0] = 0x1A
	msg.Bytes[1] = 0x1A
	frame := EncodeData(ModeBeast12MHz, msg, nil)

	if len(frame) != 16+2 {
		t.Errorf("length: got %d, want 18 (2 escaped bytes)", len(frame))
	}
}

func TestStandardTimestamp(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000}
	frame := EncodeData(ModeBeast12MHz, msg, nil)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000", ts)
	}
}

func TestEncodeV2CorrectedNanos(t *testing.T) {
	// Oscillator at -15 ppm: measuredTicks = 99,998,500.
	// Message 50,000,000 ticks after PPS (half of NOMINAL 100M).
	// Corrected nanos = 50M * 1e9 / 99,998,500 = 500,007,500
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 99_998_500,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 100_000_000 + 50_000_000}

	frame := EncodeData(ModeRadarcapeGPS, msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	nanos := ts & 0x3FFFFFFF
	seconds := (ts >> 30) & 0x1FFFF
	gpsSync := (ts >> 47) & 1

	if gpsSync != 0 {
		t.Error("GPS sync bit should not be set (mlat-client includes it in seconds extraction)")
	}
	if seconds != 3605 {
		t.Errorf("seconds: got %d, want 3605", seconds)
	}
	// 50,000,000 * 1,000,000,000 / 99,998,500 = 500,007,500 (integer division)
	if nanos != 500_007_500 {
		t.Errorf("nanos: got %d, want 500007500", nanos)
	}
}

func TestEncodeV2NotSyncedFallsBackToStandard(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000}

	frame := EncodeData(ModeBeast12MHz, msg, nil)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000 (standard fallback)", ts)
	}
}

func TestEncodeV2NilRefFallback(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000}
	frame := EncodeData(ModeBeast12MHz, msg, nil)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 12_000_000 {
		t.Errorf("fallback timestamp: got %d, want 12000000", ts)
	}
}

func TestEncodeV2NoSyncFallsBackToStandard(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000}

	frame := EncodeData(ModeBeast12MHz, msg, nil)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000 (standard fallback)", ts)
	}
}

func TestEncodeV2PpsBoundaryCrossing(t *testing.T) {
	// Message TOA < counterAtPps → belongs to previous second.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  200_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 200_000_000 - 1_000_000}

	frame := EncodeData(ModeRadarcapeGPS, msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF

	if seconds != 3604 {
		t.Errorf("seconds: got %d, want 3604", seconds)
	}
	if nanos != 990_000_000 {
		t.Errorf("nanos: got %d, want 990000000", nanos)
	}
}

func TestEncodeV2MidnightRollover(t *testing.T) {
	ref := &pps.ClockRef{
		Count:         100,
		CounterAtPps:  1_000_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 1_050_000_000}

	frame := EncodeData(ModeRadarcapeGPS, msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	if seconds != 86399 {
		t.Errorf("seconds: got %d, want 86399", seconds)
	}
}

func TestEncodeV2ForwardRolloverFullSecond(t *testing.T) {
	// Stale ClockRef: message arrives 1.5 seconds after PPS.
	// delta = 150M, tickRate = 100M → secOfDay advances by 1, nanos = 500_000_000.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 100_000_000 + 150_000_000}

	frame := EncodeData(ModeRadarcapeGPS, msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF

	if seconds != 3606 {
		t.Errorf("seconds: got %d, want 3606", seconds)
	}
	if nanos != 500_000_000 {
		t.Errorf("nanos: got %d, want 500000000", nanos)
	}
}

func TestEncodeV2ForwardRolloverMidnight(t *testing.T) {
	// Stale ClockRef: PPS at 23:59:59, message 1.2 seconds later → wraps to 00:00:00.
	// delta = 120M, tickRate = 100M → secOfDay = (86399+1) % 86400 = 0, nanos = 200_000_000.
	ref := &pps.ClockRef{
		Count:         1,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 23, 59, 59, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 100_000_000 + 120_000_000}

	frame := EncodeData(ModeRadarcapeGPS, msg, ref)

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(frame[2+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF

	if seconds != 0 {
		t.Errorf("seconds: got %d, want 0 (midnight rollover)", seconds)
	}
	if nanos != 200_000_000 {
		t.Errorf("nanos: got %d, want 200000000", nanos)
	}
}

// --- New Task 1 tests: EncodeData with explicit OutputMode ---

func TestEncodeDataBeast12MHz(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000, RPL: 50000}
	frame := EncodeData(ModeBeast12MHz, msg, nil)

	payload := unescapePayload(frame)
	if payload[0] != TypeLong {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypeLong)
	}

	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	if ts != 12_000_000 {
		t.Errorf("12 MHz timestamp: got %d, want 12000000", ts)
	}
	// Verify sync bit is NOT set.
	if (ts>>47)&1 != 0 {
		t.Error("sync bit should not be set in Beast 12 MHz mode")
	}
}

func TestEncodeDataLegacy12MHzSameAsBeast(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000, RPL: 50000}
	beastFrame := EncodeData(ModeBeast12MHz, msg, nil)
	legacyFrame := EncodeData(ModeRadarcapeLegacy12MHz, msg, nil)

	if !bytes.Equal(beastFrame, legacyFrame) {
		t.Error("ModeRadarcapeLegacy12MHz should produce the same frame as ModeBeast12MHz")
	}
}

func TestEncodeDataRadarcapeGPS(t *testing.T) {
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	msg := regs.Message{Len: 14, TOA: 150_000_000}

	frame := EncodeData(ModeRadarcapeGPS, msg, ref)

	payload := unescapePayload(frame)
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}

	gpsSync := (ts >> 47) & 1
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF

	if gpsSync != 0 {
		t.Error("GPS sync bit should not be set (mlat-client includes it in seconds extraction)")
	}
	if seconds != 3605 {
		t.Errorf("seconds: got %d, want 3605", seconds)
	}
	if nanos != 500_000_000 {
		t.Errorf("nanos: got %d, want 500000000", nanos)
	}
}

// --- Task 2 tests: EncodeStatus for 0x34 Radarcape status frames ---

func TestEncodeStatusGPSMode(t *testing.T) {
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}

	frame := EncodeStatus(ModeRadarcapeGPS, ModeRadarcapeGPS, 150_000_000, ref, 0)
	payload := unescapePayload(frame)

	// Verify frame type is 0x34 (status).
	if payload[0] != TypeStatus {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypeStatus)
	}

	// Extract the 48-bit timestamp and verify the sync bit is cleared.
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	if (ts>>47)&1 != 0 {
		t.Error("sync bit should be cleared on status frames")
	}

	// Verify GPS-format timestamp (seconds and nanos still encoded).
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF
	if seconds != 3605 {
		t.Errorf("seconds: got %d, want 3605", seconds)
	}
	if nanos != 500_000_000 {
		t.Errorf("nanos: got %d, want 500000000", nanos)
	}

	// Signal byte is unused for status frames.
	if payload[7] != 0x00 {
		t.Errorf("signal byte: got 0x%02X, want 0x00", payload[7])
	}

	// Status payload starts at payload[8].
	// Byte 0 (settings): binary + GPS timestamp = 0x11.
	if payload[8] != 0x11 {
		t.Errorf("settings byte: got 0x%02X, want 0x11", payload[8])
	}
	// Byte 1 (PPS delta): 0.
	if payload[9] != 0x00 {
		t.Errorf("pps delta byte: got 0x%02X, want 0x00", payload[9])
	}
	// Byte 2 (GPS status): 0xFF for synced.
	if payload[10] != 0xFF {
		t.Errorf("gps status byte: got 0x%02X, want 0xFF", payload[10])
	}

	// Total unescaped payload: 1 type + 6 ts + 1 signal + 14 status = 22.
	if len(payload) != 22 {
		t.Errorf("payload length: got %d, want 22", len(payload))
	}
}

func TestEncodeStatusLegacyMode(t *testing.T) {
	frame := EncodeStatus(ModeRadarcapeLegacy12MHz, ModeRadarcapeLegacy12MHz, 100_000_000, nil, 0)
	payload := unescapePayload(frame)

	if payload[0] != TypeStatus {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypeStatus)
	}

	// 12 MHz timestamp for toa=100_000_000 should be 12_000_000.
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000", ts)
	}

	// Settings: binary only (no GPS timestamp) = 0x01.
	if payload[8] != 0x01 {
		t.Errorf("settings byte: got 0x%02X, want 0x01", payload[8])
	}
	// GPS status: unsynced = 0x00.
	if payload[10] != 0x00 {
		t.Errorf("gps status byte: got 0x%02X, want 0x00", payload[10])
	}
}

func TestEncodeStatusTransition(t *testing.T) {
	// Mode transition: timestamp still GPS-format, but reported mode is legacy.
	// This models the case where we switch modes mid-stream: the timestamp
	// must remain parseable by the old format, but the settings byte
	// advertises the new mode.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}

	frame := EncodeStatus(ModeRadarcapeGPS, ModeRadarcapeLegacy12MHz, 150_000_000, ref, 0)
	payload := unescapePayload(frame)

	// Settings byte should reflect the reported (new) mode: binary only = 0x01.
	if payload[8] != 0x01 {
		t.Errorf("settings byte: got 0x%02X, want 0x01 (legacy reported)", payload[8])
	}

	// GPS status byte should be unsynced (legacy mode).
	if payload[10] != 0x00 {
		t.Errorf("gps status byte: got 0x%02X, want 0x00 (legacy reported)", payload[10])
	}

	// Timestamp should still be GPS-format (but with sync bit cleared for status).
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	nanos := ts & 0x3FFFFFFF

	if seconds != 3605 {
		t.Errorf("seconds: got %d, want 3605 (GPS timestamp format)", seconds)
	}
	if nanos != 500_000_000 {
		t.Errorf("nanos: got %d, want 500000000 (GPS timestamp format)", nanos)
	}
}

func TestEncodeStatusPpsDelta(t *testing.T) {
	// Verify the PPS delta byte is at the correct offset. v1 always passes 0,
	// but verify the plumbing is correct by confirming the byte is zero.
	frame := EncodeStatus(ModeBeast12MHz, ModeBeast12MHz, 0, nil, 0)
	payload := unescapePayload(frame)

	// PPS delta is at status payload byte 1 → unescaped payload offset 9.
	if payload[9] != 0x00 {
		t.Errorf("pps delta byte: got 0x%02X, want 0x00", payload[9])
	}
}

// --- Task 3 tests: EncodePosition for 0x35 Radarcape position frames ---

func TestEncodePositionLayout(t *testing.T) {
	// Perth coordinates: -31.9505°S, 115.8605°E, 25m altitude.
	lat := -31.9505
	lon := 115.8605
	alt := float32(25.0)

	frame := EncodePosition(lat, lon, alt)
	payload := unescapePayload(frame)

	// Verify frame type is 0x35 (position).
	if payload[0] != TypePosition {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypePosition)
	}

	// Total unescaped payload: 1 type + 21 data = 22.
	if len(payload) != 22 {
		t.Errorf("payload length: got %d, want 22", len(payload))
	}

	// Bytes 1-4 (data[0:4]) are reserved, must be zero.
	for i := 1; i <= 4; i++ {
		if payload[i] != 0x00 {
			t.Errorf("reserved byte at payload[%d]: got 0x%02X, want 0x00", i, payload[i])
		}
	}

	// Latitude at payload[5:9] (data[4:8]), little-endian float32.
	gotLat := math.Float32frombits(binary.LittleEndian.Uint32(payload[5:9]))
	wantLat := float32(lat)
	if gotLat != wantLat {
		t.Errorf("latitude: got %f, want %f", gotLat, wantLat)
	}

	// Longitude at payload[9:13] (data[8:12]), little-endian float32.
	gotLon := math.Float32frombits(binary.LittleEndian.Uint32(payload[9:13]))
	wantLon := float32(lon)
	if gotLon != wantLon {
		t.Errorf("longitude: got %f, want %f", gotLon, wantLon)
	}

	// Altitude at payload[13:17] (data[12:16]), little-endian float32.
	gotAlt := math.Float32frombits(binary.LittleEndian.Uint32(payload[13:17]))
	if gotAlt != alt {
		t.Errorf("altitude: got %f, want %f", gotAlt, alt)
	}

	// Bytes 17-21 (data[16:20]) are reserved, must be zero.
	for i := 17; i <= 21; i++ {
		if payload[i] != 0x00 {
			t.Errorf("trailing reserved byte at payload[%d]: got 0x%02X, want 0x00", i, payload[i])
		}
	}
}
