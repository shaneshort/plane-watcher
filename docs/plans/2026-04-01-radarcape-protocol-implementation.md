# Radarcape Protocol Compliance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Radarcape 0x34 status frames, 0x35 position frames, and explicit output mode model so mlat-client autodetects timestamp modes correctly.

**Architecture:** Stateless frame encoders in `beast` package, state machine in `plane-feeder`. Server gains an `OnConnect` callback for welcome frames to late-joining clients. Design spec: `docs/plans/2026-04-01-radarcape-protocol-compliance.md`.

**Tech Stack:** Go 1.25, module `github.com/plane-watcher/plane-feeder`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `ps/internal/beast/beast.go` | Add `OutputMode` type, `EncodeData`, `EncodeStatus`, `EncodePosition`; deprecate `EncodeV2` |
| Modify | `ps/internal/beast/beast_test.go` | Update all tests for new `EncodeData` API, add status/position tests |
| Modify | `ps/internal/beast/golden_test.go` | Update golden tests from `EncodeV2` to `EncodeData` |
| Modify | `ps/internal/server/server.go` | Add `OnConnect` callback for welcome frames |
| Modify | `ps/internal/server/e2e_test.go` | Update for `EncodeData`, add welcome-frame and status-ordering tests |
| Modify | `ps/cmd/plane-feeder/main.go` | Add radarcape state machine, `--alt` flag, gpsd altitude, welcome frames |
| Modify | `ps/cmd/beast-client/main.go` | Add 0x34/0x35 frame recognition and printing |
| Modify | `tools/inspect_live_timestamps.py` | Add settings byte decoding, 0x35 position parsing |

---

## Task 1: OutputMode Type and EncodeData

**Files:**
- Modify: `ps/internal/beast/beast.go:1-77`
- Modify: `ps/internal/beast/beast_test.go:1-268`

- [ ] **Step 1: Write test for EncodeData with ModeBeast12MHz**

Add to `ps/internal/beast/beast_test.go`, replacing `TestStandardTimestamp`:

```go
func TestEncodeDataBeast12MHz(t *testing.T) {
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
```

- [ ] **Step 2: Write test for EncodeData with ModeRadarcapeLegacy12MHz produces same output**

```go
func TestEncodeDataLegacy12MHzSameAsBeast(t *testing.T) {
	msg := regs.Message{Len: 14, TOA: 100_000_000}
	beastFrame := EncodeData(ModeBeast12MHz, msg, nil)
	legacyFrame := EncodeData(ModeRadarcapeLegacy12MHz, msg, nil)

	if !bytes.Equal(beastFrame, legacyFrame) {
		t.Errorf("Beast and Legacy12MHz frames differ:\n beast:  %s\n legacy: %s",
			fmtHex(beastFrame), fmtHex(legacyFrame))
	}
}
```

(Add `"bytes"` to imports in beast_test.go.)

- [ ] **Step 3: Write test for EncodeData with ModeRadarcapeGPS**

```go
func TestEncodeDataRadarcapeGPS(t *testing.T) {
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

	if gpsSync != 1 {
		t.Error("GPS sync bit not set")
	}
	if seconds != 3605 {
		t.Errorf("seconds: got %d, want 3605", seconds)
	}
	if nanos != 500_007_500 {
		t.Errorf("nanos: got %d, want 500007500", nanos)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd ps && go test ./internal/beast/ -run 'TestEncodeData' -v 2>&1 | tee /tmp/beast-mode-fail.log`

Expected: compilation errors — `EncodeData` and `OutputMode` not defined yet.

- [ ] **Step 5: Add OutputMode type and EncodeData to beast.go**

Replace the contents of `ps/internal/beast/beast.go` with:

```go
package beast

import (
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
	ts48 |= 1 << 47
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
```

- [ ] **Step 6: Run new tests to verify they pass**

Run: `cd ps && go test ./internal/beast/ -run 'TestEncodeData' -v 2>&1 | tee /tmp/beast-mode-pass.log`

Expected: all three `TestEncodeData*` tests PASS.

- [ ] **Step 7: Update remaining tests to use EncodeData where they tested EncodeV2 directly**

In `beast_test.go`, update `TestEncodeLongMessage`, `TestEncodeShortMessage`, `TestEscaping` to call `EncodeData(ModeBeast12MHz, msg, nil)` instead of `EncodeV2(msg, nil)`.

Update `TestEncodeV2CorrectedNanos` to call `EncodeData(ModeRadarcapeGPS, msg, ref)`.

Update `TestEncodeV2NotSyncedFallsBackToStandard`, `TestEncodeV2NilRefFallback`, `TestEncodeV2NoSyncFallsBackToStandard` to call `EncodeData(ModeBeast12MHz, msg, nil)` (since these tested the fallback path, which is now explicit).

Update `TestEncodeV2PpsBoundaryCrossing` through `TestEncodeV2ForwardRolloverMidnight` to call `EncodeData(ModeRadarcapeGPS, msg, ref)`.

- [ ] **Step 8: Update golden tests**

In `golden_test.go`, change all three `beast.EncodeV2(msg, nil)` calls to `beast.EncodeData(beast.ModeBeast12MHz, msg, nil)`.

- [ ] **Step 9: Run full test suite**

Run: `cd ps && go test ./internal/beast/ -v 2>&1 | tee /tmp/beast-all.log`

Expected: ALL tests pass. Grep for `FAIL` — should be zero.

- [ ] **Step 10: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/internal/beast/beast.go ps/internal/beast/beast_test.go ps/internal/beast/golden_test.go
git commit -m "feat(beast): add explicit OutputMode and EncodeData replacing implicit mode selection

EncodeData takes an explicit OutputMode argument instead of inferring mode
from ref != nil && ref.GpsSync. Three modes: ModeBeast12MHz,
ModeRadarcapeGPS, ModeRadarcapeLegacy12MHz. EncodeV2 retained as
deprecated wrapper.

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 2: EncodeStatus for 0x34 Frames

**Files:**
- Modify: `ps/internal/beast/beast.go`
- Modify: `ps/internal/beast/beast_test.go`

- [ ] **Step 1: Write test for GPS-mode status frame**

Add to `beast_test.go`:

```go
func TestEncodeStatusGPSMode(t *testing.T) {
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	toa := uint64(150_000_000)
	frame := EncodeStatus(ModeRadarcapeGPS, ModeRadarcapeGPS, toa, ref, 0)

	// Unescape the frame to get raw payload.
	payload := unescapePayload(frame)
	if payload[0] != TypeStatus {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypeStatus)
	}

	// Settings byte = 0x11 (binary + GPS).
	settings := payload[8]
	if settings != 0x11 {
		t.Errorf("settings: got 0x%02X, want 0x11", settings)
	}

	// PPS delta byte.
	if payload[9] != 0 {
		t.Errorf("pps delta: got %d, want 0", payload[9])
	}

	// GPS status byte = 0xFF.
	if payload[10] != 0xFF {
		t.Errorf("gps status: got 0x%02X, want 0xFF", payload[10])
	}

	// Timestamp should be GPS-format (check sync bit is NOT set on status frames,
	// matching observed Radarcape behaviour — status frames clear bit 47).
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	// GPS format: seconds + nanos fields should be populated.
	seconds := (ts >> 30) & 0x1FFFF
	if seconds == 0 && toa > 0 {
		t.Errorf("expected non-zero seconds in GPS timestamp, got 0")
	}
}
```

- [ ] **Step 2: Write test for legacy-mode status frame**

```go
func TestEncodeStatusLegacyMode(t *testing.T) {
	frame := EncodeStatus(ModeRadarcapeLegacy12MHz, ModeRadarcapeLegacy12MHz, 100_000_000, nil, 0)

	payload := unescapePayload(frame)
	if payload[0] != TypeStatus {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypeStatus)
	}

	// Settings byte = 0x01 (binary, no GPS).
	if payload[8] != 0x01 {
		t.Errorf("settings: got 0x%02X, want 0x01", payload[8])
	}

	// GPS status byte = 0x00.
	if payload[10] != 0x00 {
		t.Errorf("gps status: got 0x%02X, want 0x00", payload[10])
	}

	// Timestamp should be 12 MHz format.
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	if ts != 12_000_000 {
		t.Errorf("timestamp: got %d, want 12000000", ts)
	}
}
```

- [ ] **Step 3: Write test for transition status frame (mixed modes)**

```go
func TestEncodeStatusTransition(t *testing.T) {
	// Transition from GPS to legacy: timestamp in GPS format, settings advertise legacy.
	ref := &pps.ClockRef{
		Count:         5,
		CounterAtPps:  100_000_000,
		MeasuredTicks: 100_000_000,
		WallUTC:       time.Date(2026, 3, 29, 1, 0, 5, 0, time.UTC),
		GpsSync:       true,
	}
	frame := EncodeStatus(ModeRadarcapeGPS, ModeRadarcapeLegacy12MHz, 150_000_000, ref, 0)

	payload := unescapePayload(frame)

	// Settings byte advertises legacy mode (0x01, bit 4 clear).
	if payload[8] != 0x01 {
		t.Errorf("settings: got 0x%02X, want 0x01 (legacy)", payload[8])
	}

	// GPS status = 0x00 (legacy mode advertised).
	if payload[10] != 0x00 {
		t.Errorf("gps status: got 0x%02X, want 0x00", payload[10])
	}

	// Timestamp is GPS-format (from timestampMode = ModeRadarcapeGPS).
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	seconds := (ts >> 30) & 0x1FFFF
	if seconds == 0 {
		t.Errorf("expected GPS-format timestamp with non-zero seconds")
	}
}
```

- [ ] **Step 4: Write test for PPS delta byte placement**

```go
func TestEncodeStatusPpsDelta(t *testing.T) {
	frame := EncodeStatus(ModeRadarcapeLegacy12MHz, ModeRadarcapeLegacy12MHz, 0, nil, 0)
	payload := unescapePayload(frame)

	// v1: PPS delta always 0.
	if payload[9] != 0 {
		t.Errorf("pps delta: got %d, want 0", int8(payload[9]))
	}
}
```

- [ ] **Step 5: Add unescapePayload helper to beast_test.go**

```go
// unescapePayload strips the leading 0x1A and reverses escape doubling.
func unescapePayload(frame []byte) []byte {
	if len(frame) == 0 || frame[0] != Escape {
		return frame
	}
	var out []byte
	for i := 1; i < len(frame); i++ {
		out = append(out, frame[i])
		if frame[i] == Escape && i+1 < len(frame) && frame[i+1] == Escape {
			i++ // skip doubled escape
		}
	}
	return out
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `cd ps && go test ./internal/beast/ -run 'TestEncodeStatus' -v 2>&1 | tee /tmp/beast-status-fail.log`

Expected: compilation error — `EncodeStatus` not defined.

- [ ] **Step 7: Implement EncodeStatus**

Add to `ps/internal/beast/beast.go`:

```go
const (
	// settingsBinary is Beast binary output protocol (bit 0).
	settingsBinary = 0x01
	// settingsGPSTimestamp enables GPS timestamp mode (bit 4).
	settingsGPSTimestamp = 0x10
	// gpsStatusSynced is the GPS status byte for GPS-synced mode.
	// 0xFF matches observed real Radarcape behaviour. See design doc
	// "Documented Decision: GPS Status Byte" for full rationale.
	gpsStatusSynced = 0xFF
	// gpsStatusUnsynced is the GPS status byte when GPS is not available.
	gpsStatusUnsynced = 0x00
)

// EncodeStatus produces a 0x34 Radarcape status frame.
//
// timestampMode controls how the 6-byte timestamp is encoded. reportedMode
// determines the settings byte (the mode being announced to clients). During
// mode transitions these differ: timestamp uses the old mode so mlat-client
// can parse it, settings byte advertises the new mode.
//
// toa is the current FPGA counter value for the timestamp. ref is required
// when timestampMode is ModeRadarcapeGPS. ppsDelta is the PPS timing offset
// byte — currently always 0 (see design doc "PPS delta byte: UNRESOLVED").
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
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `cd ps && go test ./internal/beast/ -run 'TestEncodeStatus' -v 2>&1 | tee /tmp/beast-status-pass.log`

Expected: all four `TestEncodeStatus*` tests PASS.

- [ ] **Step 9: Run full test suite**

Run: `cd ps && go test ./internal/beast/ -v 2>&1 | tee /tmp/beast-all2.log && grep -c FAIL /tmp/beast-all2.log`

Expected: 0 failures.

- [ ] **Step 10: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/internal/beast/beast.go ps/internal/beast/beast_test.go
git commit -m "feat(beast): add EncodeStatus for 0x34 Radarcape status frames

Two-mode API: timestampMode encodes timestamp in old decoder's format,
reportedMode sets the settings byte for mode advertisement. Status frames
clear sync bit 47. GPS status byte 0xFF matches real Radarcape.

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 3: EncodePosition for 0x35 Frames

**Files:**
- Modify: `ps/internal/beast/beast.go`
- Modify: `ps/internal/beast/beast_test.go`

- [ ] **Step 1: Write test for position frame layout**

Add to `beast_test.go`:

```go
func TestEncodePositionLayout(t *testing.T) {
	frame := EncodePosition(-31.9505, 115.8605, 25.0)

	payload := unescapePayload(frame)

	if payload[0] != TypePosition {
		t.Errorf("type: got 0x%02X, want 0x%02X", payload[0], TypePosition)
	}

	// Total payload: 1 type + 21 data = 22 bytes.
	if len(payload) != 22 {
		t.Fatalf("payload length: got %d, want 22", len(payload))
	}

	// Bytes 1-4 reserved (zero).
	for i := 1; i <= 4; i++ {
		if payload[i] != 0 {
			t.Errorf("reserved byte %d: got 0x%02X, want 0x00", i, payload[i])
		}
	}

	// Lat at bytes 5-8 as IEEE 754 float32 little-endian.
	lat := math.Float32frombits(
		uint32(payload[5]) | uint32(payload[6])<<8 | uint32(payload[7])<<16 | uint32(payload[8])<<24,
	)
	if math.Abs(float64(lat)-(-31.9505)) > 0.001 {
		t.Errorf("latitude: got %f, want ~-31.9505", lat)
	}

	// Lon at bytes 9-12.
	lon := math.Float32frombits(
		uint32(payload[9]) | uint32(payload[10])<<8 | uint32(payload[11])<<16 | uint32(payload[12])<<24,
	)
	if math.Abs(float64(lon)-115.8605) > 0.001 {
		t.Errorf("longitude: got %f, want ~115.8605", lon)
	}

	// Alt at bytes 13-16.
	alt := math.Float32frombits(
		uint32(payload[13]) | uint32(payload[14])<<8 | uint32(payload[15])<<16 | uint32(payload[16])<<24,
	)
	if math.Abs(float64(alt)-25.0) > 0.1 {
		t.Errorf("altitude: got %f, want ~25.0", alt)
	}

	// Bytes 17-21 reserved.
	for i := 17; i <= 21; i++ {
		if payload[i] != 0 {
			t.Errorf("reserved byte %d: got 0x%02X, want 0x00", i, payload[i])
		}
	}
}
```

(Add `"math"` to the `beast_test.go` imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ps && go test ./internal/beast/ -run 'TestEncodePosition' -v 2>&1 | tee /tmp/beast-pos-fail.log`

Expected: compilation error — `EncodePosition` not defined.

- [ ] **Step 3: Implement EncodePosition**

Add to `ps/internal/beast/beast.go` (also add `"encoding/binary"` and `"math"` to imports):

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ps && go test ./internal/beast/ -run 'TestEncodePosition' -v 2>&1 | tee /tmp/beast-pos-pass.log`

Expected: PASS.

- [ ] **Step 5: Run full test suite**

Run: `cd ps && go test ./internal/beast/ -v 2>&1 | tee /tmp/beast-all3.log && grep -c FAIL /tmp/beast-all3.log`

Expected: 0 failures.

- [ ] **Step 6: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/internal/beast/beast.go ps/internal/beast/beast_test.go
git commit -m "feat(beast): add EncodePosition for 0x35 Radarcape position frames

IEEE 754 float32 little-endian lat/lon/alt in a 21-byte payload
with no timestamp or signal header. Matches readsb and mlat-client
parsing expectations.

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 4: Server OnConnect Callback

**Files:**
- Modify: `ps/internal/server/server.go:103-125`
- Modify: `ps/internal/server/e2e_test.go`

- [ ] **Step 1: Write test for OnConnect welcome frames**

Add to `e2e_test.go`:

```go
func TestOnConnectWelcomeFrames(t *testing.T) {
	// Build a status frame to use as the welcome message.
	welcomeStatus := beast.EncodeStatus(
		beast.ModeRadarcapeLegacy12MHz, beast.ModeRadarcapeLegacy12MHz, 0, nil, 0,
	)

	srv := server.New(0)
	srv.SetWelcomeFrames([][]byte{welcomeStatus})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	// Connect a client AFTER welcome frames are set.
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read the welcome frame.
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ := conn.Read(buf)

	if n < 2 {
		t.Fatalf("expected welcome frame, got %d bytes", n)
	}
	if buf[0] != 0x1A || buf[1] != beast.TypeStatus {
		t.Errorf("welcome frame: got type 0x%02X, want 0x%02X (status)", buf[1], beast.TypeStatus)
	}
}
```

(Add `"github.com/plane-watcher/plane-feeder/internal/beast"` to e2e_test.go imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ps && go test ./internal/server/ -run 'TestOnConnectWelcome' -v 2>&1 | tee /tmp/server-welcome-fail.log`

Expected: compilation error — `SetWelcomeFrames` not defined.

- [ ] **Step 3: Implement SetWelcomeFrames in server.go**

Add to `ps/internal/server/server.go`:

```go
// Add to the Server struct:
//   welcomeFrames atomic.Pointer[[][]byte]

// (Replace the struct definition with:)
type Server struct {
	port          int
	listener      net.Listener
	mu            sync.Mutex
	clients       map[*client]struct{}
	done          chan struct{}
	stopOnce      sync.Once
	welcomeFrames atomic.Pointer[[][]byte]
}
```

Add the `"sync/atomic"` import (already imported via `sync`; just need `atomic` — actually Go's `sync/atomic` is separate). Since `sync` is already imported, add `"sync/atomic"` to the import block.

Add the method:

```go
// SetWelcomeFrames sets the frames sent to each newly connected client.
// These are typically 0x34 status and 0x35 position frames. Thread-safe;
// can be called from any goroutine. Pass nil to clear welcome frames.
func (s *Server) SetWelcomeFrames(frames [][]byte) {
	s.welcomeFrames.Store(&frames)
}
```

Update `acceptLoop` to send welcome frames on connect (after adding the client):

```go
func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		c := &client{
			conn: conn,
			ch:   make(chan []byte, clientBufSize),
		}
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		log.Printf("client connected: %s (%d total)", conn.RemoteAddr(), s.ClientCount())

		// Send welcome frames before entering broadcast loop.
		if wf := s.welcomeFrames.Load(); wf != nil {
			for _, frame := range *wf {
				c.ch <- frame
			}
		}

		go s.writeLoop(c)
	}
}
```

Also update `AddConn` (used by tests) to send welcome frames:

```go
func (s *Server) AddConn(conn net.Conn) {
	c := &client{
		conn: conn,
		ch:   make(chan []byte, clientBufSize),
	}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()

	// Send welcome frames to externally-added connections too.
	if wf := s.welcomeFrames.Load(); wf != nil {
		for _, frame := range *wf {
			c.ch <- frame
		}
	}

	go s.writeLoop(c)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ps && go test ./internal/server/ -run 'TestOnConnectWelcome' -v 2>&1 | tee /tmp/server-welcome-pass.log`

Expected: PASS.

- [ ] **Step 5: Run full server test suite**

Run: `cd ps && go test ./internal/server/ -v 2>&1 | tee /tmp/server-all.log && grep -c FAIL /tmp/server-all.log`

Expected: 0 failures (existing `TestEndToEnd` still passes — no welcome frames set means no change).

- [ ] **Step 6: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/internal/server/server.go ps/internal/server/e2e_test.go
git commit -m "feat(server): add SetWelcomeFrames for late-joining client support

New clients receive cached status/position frames before entering the
broadcast loop. Uses atomic.Pointer for lock-free updates from the
feeder goroutine.

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 5: Feeder State Machine

**Files:**
- Modify: `ps/cmd/plane-feeder/main.go:179-442`

This is the largest task. It wires together the mode model, status/position emission, welcome frames, and the `--alt` flag.

- [ ] **Step 1: Add --alt flag and extend gpsd query**

In `main.go`, add the `--alt` flag alongside `--lat`/`--lon` (near line 189):

```go
alt := flag.Float64("alt", 0, "Receiver altitude in metres (0 = query gpsd)")
```

Update the `queryGpsdPosition` function signature and struct to include altitude:

```go
func queryGpsdPosition() (lat, lon, alt float64, err error) {
	// ... (existing connection/write code unchanged) ...

	dec := json.NewDecoder(conn)
	for {
		var msg struct {
			Class  string  `json:"class"`
			Mode   int     `json:"mode"`
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			AltMSL float64 `json:"altMSL"`
		}
		if err := dec.Decode(&msg); err != nil {
			return 0, 0, 0, fmt.Errorf("decode: %w", err)
		}
		if msg.Class == "TPV" && msg.Mode >= 2 && msg.Lat != 0 {
			return msg.Lat, msg.Lon, msg.AltMSL, nil
		}
	}
}
```

Update the gpsd caller (near line 194):

```go
if *lat == 0 && *lon == 0 {
	if gLat, gLon, gAlt, err := queryGpsdPosition(); err == nil {
		*lat = gLat
		*lon = gLon
		if *alt == 0 {
			*alt = gAlt
		}
		log.Printf("receiver position from gpsd: %.4f, %.4f, alt=%.1fm", *lat, *lon, *alt)
	} else {
		log.Printf("WARNING: no receiver position (gpsd: %v). CPR decode and distance will be unavailable.", err)
	}
}
```

- [ ] **Step 2: Add radarcapeState type and constants**

Add above the `main` function:

```go
const (
	statusInterval   = 1 * time.Second
	positionInterval = 30 * time.Second
)

type receiverPosition struct {
	Lat float64
	Lon float64
	Alt float32
}

type radarcapeState struct {
	currentMode  beast.OutputMode
	desiredMode  beast.OutputMode
	lastGoodRef  *pps.ClockRef
	lastStatus   time.Time
	lastPosition time.Time
	position     *receiverPosition
	initialised  bool
}

// update computes desiredMode from the current clock reference.
func (rs *radarcapeState) update(ref *pps.ClockRef) {
	if ref != nil && ref.GpsSync {
		rs.desiredMode = beast.ModeRadarcapeGPS
		rs.lastGoodRef = ref
	} else {
		rs.desiredMode = beast.ModeRadarcapeLegacy12MHz
	}
}

// emitControlFrames returns any status/position frames that need broadcasting.
// now is the injected clock for testability.
func (rs *radarcapeState) emitControlFrames(now time.Time, toa uint64, ref *pps.ClockRef) [][]byte {
	var frames [][]byte

	if !rs.initialised {
		// Startup: emit initial status and position.
		statusFrame := beast.EncodeStatus(rs.desiredMode, rs.desiredMode, toa, ref, 0)
		frames = append(frames, statusFrame)
		if rs.position != nil {
			frames = append(frames, beast.EncodePosition(rs.position.Lat, rs.position.Lon, rs.position.Alt))
		}
		rs.currentMode = rs.desiredMode
		rs.lastStatus = now
		rs.lastPosition = now
		rs.initialised = true
		return frames
	}

	// Mode transition: timestamp in old mode, settings advertise new mode.
	if rs.desiredMode != rs.currentMode {
		tsMode := rs.currentMode
		tsRef := ref
		// For GPS-to-legacy, use lastGoodRef for the old-mode timestamp.
		if tsMode == beast.ModeRadarcapeGPS && (ref == nil || !ref.GpsSync) {
			tsRef = rs.lastGoodRef
		}
		statusFrame := beast.EncodeStatus(tsMode, rs.desiredMode, toa, tsRef, 0)
		frames = append(frames, statusFrame)
		rs.currentMode = rs.desiredMode
		rs.lastStatus = now
		return frames
	}

	// Periodic status.
	if now.Sub(rs.lastStatus) >= statusInterval {
		statusFrame := beast.EncodeStatus(rs.currentMode, rs.currentMode, toa, ref, 0)
		frames = append(frames, statusFrame)
		rs.lastStatus = now
	}

	// Periodic position.
	if rs.position != nil && now.Sub(rs.lastPosition) >= positionInterval {
		frames = append(frames, beast.EncodePosition(rs.position.Lat, rs.position.Lon, rs.position.Alt))
		rs.lastPosition = now
	}

	return frames
}

// welcomeFrames returns the pre-encoded frames for late-joining clients.
func (rs *radarcapeState) welcomeFrames(toa uint64, ref *pps.ClockRef) [][]byte {
	var frames [][]byte
	frames = append(frames, beast.EncodeStatus(rs.currentMode, rs.currentMode, toa, ref, 0))
	if rs.position != nil {
		frames = append(frames, beast.EncodePosition(rs.position.Lat, rs.position.Lon, rs.position.Alt))
	}
	return frames
}
```

- [ ] **Step 3: Wire the state machine into main()**

Replace the `emit` closure and the section between "--- 5. Beast TCP server ---" and the poll loop with the radarcape state machine integration.

In the section before the TCP server starts (after PPS watcher setup), add:

```go
	// --- 4b. Radarcape state machine ---
	var rcState *radarcapeState
	if *radarcape {
		rcState = &radarcapeState{}
		if *lat != 0 || *lon != 0 {
			rcState.position = &receiverPosition{Lat: *lat, Lon: *lon, Alt: float32(*alt)}
		}
		// Determine initial mode before starting server.
		var initRef *pps.ClockRef
		if ppsWatcher != nil {
			initRef = ppsWatcher.Ref()
		}
		rcState.update(initRef)

		// Build and cache startup welcome frames BEFORE starting the listener.
		startupFrames := rcState.emitControlFrames(time.Now(), 0, initRef)
		beastSrv.SetWelcomeFrames(startupFrames)
		for _, f := range startupFrames {
			beastSrv.Broadcast(f)
		}
	}
```

Move the `beastSrv.Start()` call to AFTER the welcome frames are set (for the startup race fix).

Replace the `emit` closure with:

```go
	emit := func(msg regs.Message, clockRef *pps.ClockRef) {
		now := time.Now()

		if rcState != nil {
			rcState.update(clockRef)

			// Emit control frames (transitions, periodic status/position).
			controlFrames := rcState.emitControlFrames(now, msg.TOA, clockRef)
			for _, f := range controlFrames {
				beastSrv.Broadcast(f)
			}

			// Update welcome frames for late joiners.
			if len(controlFrames) > 0 {
				beastSrv.SetWelcomeFrames(rcState.welcomeFrames(msg.TOA, clockRef))
			}
		}

		// Encode and broadcast data frame.
		var frame []byte
		if rcState != nil {
			frame = beast.EncodeData(rcState.currentMode, msg, clockRef)
		} else {
			frame = beast.EncodeData(beast.ModeBeast12MHz, msg, nil)
		}
		beastSrv.Broadcast(frame)
		msgCount.Add(1)

		select {
		case trackCh <- msg:
		default:
		}
	}
```

- [ ] **Step 4: Build and verify**

Run: `cd ps && go build ./cmd/plane-feeder/ 2>&1 | tee /tmp/feeder-build.log`

Expected: clean build, no errors.

- [ ] **Step 5: Run all tests**

Run: `cd ps && go test ./... 2>&1 | tee /tmp/feeder-all.log && grep -c FAIL /tmp/feeder-all.log`

Expected: 0 failures.

- [ ] **Step 6: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/cmd/plane-feeder/main.go
git commit -m "feat(plane-feeder): add radarcape state machine with mode transitions

Explicit mode determination from PPS watcher state. Emits 0x34 on
startup, mode transitions, and periodically at 1Hz. Emits 0x35
every 30s when position known. Welcome frames cached for late-joining
clients. Startup ordering prevents race: welcome frames built before
TCP listener starts.

Adds --alt flag and extends gpsd query to include altitude.

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 6: Update E2E Test for Status Frame Ordering

**Files:**
- Modify: `ps/internal/server/e2e_test.go`

- [ ] **Step 1: Update TestEndToEnd to use EncodeData**

Change `beast.EncodeV2(msg, nil)` to `beast.EncodeData(beast.ModeBeast12MHz, msg, nil)` at line 48.

- [ ] **Step 2: Run test to verify it passes**

Run: `cd ps && go test ./internal/server/ -v 2>&1 | tee /tmp/e2e-pass.log`

Expected: both tests PASS.

- [ ] **Step 3: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/internal/server/e2e_test.go
git commit -m "test(server): update e2e test for EncodeData API"
```

---

## Task 7: Update beast-client for 0x34 and 0x35

**Files:**
- Modify: `ps/cmd/beast-client/main.go`

- [ ] **Step 1: Add 0x34 and 0x35 frame recognition to extractFrame**

Replace the `switch buf[typeIdx]` block (lines 126-134) with:

```go
	var payloadLen int
	switch buf[typeIdx] {
	case 0x32:
		payloadLen = 1 + 6 + 1 + 7 // type + ts + sig + short msg
	case 0x33:
		payloadLen = 1 + 6 + 1 + 14 // type + ts + sig + long msg
	case 0x34:
		payloadLen = 1 + 6 + 1 + 14 // type + ts + sig + status msg
	case 0x35:
		payloadLen = 1 + 21 // type + position data (no ts/sig)
	default:
		// Unknown type — skip this 0x1A and try again.
		return nil, buf[start+1:], false
	}
```

- [ ] **Step 2: Add status and position counters and printing**

Add `statusCount` and `positionCount` to the var block alongside `shortCount`/`longCount`. Update the frame-type switch in the parse loop:

```go
			switch {
			case len(frame) == 15 && frame[0] == 0x32:
				shortCount++
				if !*quiet {
					printFrame(total+1, frame)
				}
			case len(frame) == 22 && frame[0] == 0x33:
				longCount++
				if !*quiet {
					printFrame(total+1, frame)
				}
			case len(frame) == 22 && frame[0] == 0x34:
				statusCount++
				if !*quiet {
					printStatusFrame(total+1, frame)
				}
			case len(frame) == 22 && frame[0] == 0x35:
				positionCount++
				if !*quiet {
					printPositionFrame(total+1, frame)
				}
			default:
				errCount++
				if !*quiet {
					fmt.Printf("#%d  ERROR  len=%d type=0x%02X\n", total+1, len(frame), frame[0])
				}
			}
```

Add the printing functions:

```go
func printStatusFrame(num int, payload []byte) {
	var ts uint64
	for i := 0; i < 6; i++ {
		ts = ts<<8 | uint64(payload[1+i])
	}
	settings := payload[8]
	ppsDelta := int8(payload[9])
	gpsStatus := payload[10]

	gpsMode := "legacy"
	if settings&0x10 != 0 {
		gpsMode = "GPS"
	}

	fmt.Printf("#%-5d STATUS  TS=%-14d  settings=0x%02X(%s)  delta=%d  gps=0x%02X\n",
		num, ts, settings, gpsMode, ppsDelta, gpsStatus)
}

func printPositionFrame(num int, payload []byte) {
	lat := math.Float32frombits(
		uint32(payload[5]) | uint32(payload[6])<<8 | uint32(payload[7])<<16 | uint32(payload[8])<<24,
	)
	lon := math.Float32frombits(
		uint32(payload[9]) | uint32(payload[10])<<8 | uint32(payload[11])<<16 | uint32(payload[12])<<24,
	)
	alt := math.Float32frombits(
		uint32(payload[13]) | uint32(payload[14])<<8 | uint32(payload[15])<<16 | uint32(payload[16])<<24,
	)

	fmt.Printf("#%-5d POSITION  lat=%.6f  lon=%.6f  alt=%.1fm\n",
		num, lat, lon, alt)
}
```

(Add `"math"` to imports.)

- [ ] **Step 3: Update printSummary to include new frame types**

```go
func printSummary(short, long, status, position, errs int, elapsed time.Duration) {
	total := short + long + status + position
	fmt.Printf("\n── Summary (%s) ──\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Total:    %d frames (%d long, %d short, %d status, %d position)\n",
		total, long, short, status, position)
	if errs > 0 {
		fmt.Printf("  Errors:   %d\n", errs)
	}
	if elapsed > 0 {
		rate := float64(total) / elapsed.Seconds()
		fmt.Printf("  Rate:     %.1f frames/sec\n", rate)
	}
}
```

Update the count check and both `printSummary` call sites to pass `statusCount, positionCount`.

- [ ] **Step 4: Build and verify**

Run: `cd ps && go build ./cmd/beast-client/ 2>&1 | tee /tmp/beast-client-build.log`

Expected: clean build.

- [ ] **Step 5: Commit**

```bash
cd /home/shanes/plane_watcher && git add ps/cmd/beast-client/main.go
git commit -m "feat(beast-client): add 0x34 status and 0x35 position frame support

Prints decoded settings byte (GPS/legacy), PPS delta, GPS status byte
for status frames. Prints lat/lon/alt for position frames. Unknown
frame types are skipped gracefully.

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 8: Update inspect_live_timestamps.py

**Files:**
- Modify: `tools/inspect_live_timestamps.py`

- [ ] **Step 1: Add TYPE_POSITION constant and settings byte decoder**

Add near the existing constants (line 24):

```python
TYPE_POSITION = ord("5")
```

Add a settings byte decoder function:

```python
def decode_settings(settings: int) -> str:
    """Decode the 0x34 settings byte into human-readable flags."""
    flags = []
    flags.append("binary" if settings & 0x01 else "AVR")
    if settings & 0x10:
        flags.append("GPS")
    else:
        flags.append("12MHz")
    if settings & 0x02:
        flags.append("filtered")
    if settings & 0x08:
        flags.append("no-CRC")
    if settings & 0x20:
        flags.append("RTSCTS")
    if settings & 0x40:
        flags.append("no-FEC")
    if settings & 0x80:
        flags.append("ModeAC")
    return "+".join(flags)
```

- [ ] **Step 2: Add 0x35 to the frame extractor**

Update the `payload_len` dict (line 60-64) to include `TYPE_POSITION`:

```python
        if frame_type not in (TYPE_SHORT, TYPE_LONG, TYPE_STATUS, TYPE_POSITION):
            i = start + 1
            continue

        payload_len = {
            TYPE_SHORT: 1 + 6 + 1 + 7,
            TYPE_LONG: 1 + 6 + 1 + 14,
            TYPE_STATUS: 1 + 6 + 1 + 14,
            TYPE_POSITION: 1 + 21,
        }[frame_type]
```

- [ ] **Step 3: Add position frame parsing in the main loop**

After the status frame handling block (around line 149), add:

```python
                if frame_type == TYPE_POSITION:
                    # Position frame: no ts/sig, just 21 bytes of data.
                    # Lat at bytes 5-8, lon at 9-12, alt at 13-16 (LE float32).
                    pos_data = payload[1:]  # skip type byte
                    pos_lat = struct.unpack("<f", pos_data[4:8])[0]
                    pos_lon = struct.unpack("<f", pos_data[8:12])[0]
                    pos_alt = struct.unpack("<f", pos_data[12:16])[0]
                    print(
                        f"[{label}] position lat={pos_lat:.6f} lon={pos_lon:.6f} alt={pos_alt:.1f}m"
                    )
                    continue
```

- [ ] **Step 4: Update the status sample printing to include decoded flags**

Update the status printing section (around line 180-186) to use `decode_settings`:

```python
        if status_samples[label]:
            for ts48, settings, delta, gps in status_samples[label]:
                sync, sec, ns, sod = parse_ts(ts48)
                print(
                    f"[{label}] status ts=0x{ts48:012X} sync={sync} sec={sec} ns={ns} "
                    f"settings=0x{settings:02X}({decode_settings(settings)}) "
                    f"delta={delta} gps=0x{gps:02X}"
                )
```

- [ ] **Step 5: Verify syntax**

Run: `python3 -c "import py_compile; py_compile.compile('tools/inspect_live_timestamps.py', doraise=True)"`

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /home/shanes/plane_watcher && git add tools/inspect_live_timestamps.py
git commit -m "feat(tools): add settings byte decoding and 0x35 position parsing to inspect tool

Decodes settings byte flags (GPS/12MHz, binary/AVR, etc.) for human
readability. Parses 0x35 position frames (lat/lon/alt as LE float32).

Ref: docs/plans/2026-04-01-radarcape-protocol-compliance.md"
```

---

## Task 9: Lint and Integration Verification

**Files:** All modified files

- [ ] **Step 1: Run go vet on all packages**

Run: `cd ps && go vet ./... 2>&1 | tee /tmp/vet.log`

Expected: no issues.

- [ ] **Step 2: Run full test suite**

Run: `cd ps && go test ./... 2>&1 | tee /tmp/all-tests.log && grep -c FAIL /tmp/all-tests.log`

Expected: 0 failures.

- [ ] **Step 3: Build all binaries**

Run: `cd ps && go build ./cmd/plane-feeder/ && go build ./cmd/beast-client/ && go build ./cmd/replay/ 2>&1 | tee /tmp/build-all.log`

Expected: all three build cleanly.

- [ ] **Step 4: Verify Python tooling**

Run: `python3 -c "import py_compile; py_compile.compile('tools/inspect_live_timestamps.py', doraise=True)"`

Expected: no errors.

- [ ] **Step 5: Commit design and plan docs**

```bash
cd /home/shanes/plane_watcher && git add docs/plans/2026-04-01-radarcape-protocol-compliance.md docs/plans/2026-04-01-radarcape-protocol-implementation.md
git commit -m "docs: add Radarcape protocol compliance design spec and implementation plan"
```

---

## Post-Implementation: Live Verification Checklist

This is not an automated task — it requires hardware. Run on the Fishball (PlutoSDR-compatible) with GPS connected.

1. Start `plane-feeder --radarcape --lat X --lon Y --alt Z`
2. Connect with `beast-client` — verify 0x34 status frame appears before any data frames
3. Verify settings byte = `0x11` (GPS mode) when PPS sync is up
4. Connect a second `beast-client` after traffic is flowing — verify it receives a welcome 0x34 immediately
5. Check `inspect_live_timestamps.py --dut pluto.local:30005` — verify status frames appear with decoded settings
6. **Capture real Radarcape data frames and check sync bit 47** — this is the unresolved protocol question
7. Restart `plane-feeder` without GPS (or stop chrony) — verify status frame switches to settings `0x01` (legacy)
8. Verify mlat-client autodetects the mode via 0x34 frames
9. Check `/tmp/mlat/sync.json` for stable pairings with the two Radarcapes
