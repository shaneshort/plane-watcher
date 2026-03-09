# Radarcape Protocol Compliance: Status/Position Frames and Explicit Mode Model

**Date:** 2026-04-01
**Status:** Design approved, pending implementation
**Depends on:** GPS PPS timestamping (complete), Beast encoder (complete)

## Goal

Refactor the Beast encoder and plane-feeder to emit proper Radarcape protocol
frames (0x34 status, 0x35 position) with an explicit output mode model, replacing
the current implicit `ref != nil && ref.GpsSync` mode selection. This ensures
mlat-client correctly autodetects mode changes and downstream consumers see
wire-compatible Radarcape behaviour.

## Problem

The current `EncodeV2` function silently selects between GPS and 12 MHz timestamp
formats based on whether a valid `ClockRef` is passed. This implicit switching:

- Makes it possible to emit GPS-format timestamps without proper sync (or vice versa)
  without any explicit decision point
- Does not emit 0x34 status frames, so mlat-client cannot autodetect the timestamp mode
- Does not emit 0x35 position frames
- Has no mechanism to advertise mode transitions to connected clients

## Architecture

**Approach:** Stateless encoders in the `beast` package, state machine in `plane-feeder`.

- `beast` package: pure frame builders for data, status, and position frames. No scheduling
  or state. Takes an explicit `OutputMode` argument.
- `plane-feeder`: owns mode determination, transition detection, periodic emission cadence,
  and broadcast ordering.

This matches the existing architecture where `beast` is an encoding library and
`plane-feeder` is the orchestrator.

## Output Mode Model

### Type

```go
type OutputMode int

const (
    ModeBeast12MHz           OutputMode = iota // Pure Beast, 12 MHz freerunning timestamps
    ModeRadarcapeGPS                           // Radarcape GPS: 1 GHz secs+nanos, sync bit set
    ModeRadarcapeLegacy12MHz                   // Radarcape without GPS: 12 MHz timestamps, still sends 0x34
)
```

### Mode determination by plane-feeder

| Condition | Mode |
|-----------|------|
| `--radarcape` disabled | `ModeBeast12MHz` always |
| `--radarcape` enabled, `ref != nil && ref.GpsSync == true` | `ModeRadarcapeGPS` |
| `--radarcape` enabled, ref nil or `ref.GpsSync == false` | `ModeRadarcapeLegacy12MHz` |

**Last-good ref caching for GPS-to-legacy transitions:**
The feeder must cache the most recent valid `ClockRef` (where `GpsSync == true`)
as `lastGoodRef`. This is needed because the GPS-to-legacy transition requires
encoding a 0x34 status frame with `timestampMode = ModeRadarcapeGPS` (the old mode),
which requires a valid ref for GPS timestamp computation. If the ref becomes nil
(PPS watcher stopped, hardware failure), `lastGoodRef` provides the timing reference
for the transition frame.

**Staleness policy:** The transition 0x34 timestamp only needs to be *parseable* by
mlat-client under the GPS decoder, not temporally accurate. Its purpose is to deliver
the settings byte mode change, not to convey a meaningful time. mlat-client will
immediately switch decoder mode upon reading the settings byte, and the next data
frame will use the new (legacy 12 MHz) timestamp format.

Therefore no freshness bound is enforced on `lastGoodRef`. Even an arbitrarily stale
ref produces a structurally valid GPS-format timestamp (correct bit layout, secs+nanos
fields) that mlat-client can parse. The timestamp value may be wrong, but that is
acceptable for a control frame whose sole purpose is to trigger a mode switch.

If `lastGoodRef` is nil (GPS was never acquired), the transition from GPS mode
cannot occur because we were never in GPS mode — this case is unreachable.

### What each mode produces

| | Data timestamps | Sync bit (47) | 0x34 status | 0x35 position |
|---|---|---|---|---|
| `ModeBeast12MHz` | 12 MHz counter | never | never | never |
| `ModeRadarcapeGPS` | secs<<30 \| nanos | see note below | periodic + transitions | periodic (if position known) |
| `ModeRadarcapeLegacy12MHz` | 12 MHz counter | clear | periodic + transitions | periodic (if position known) |

**Sync bit (bit 47) in GPS mode — unverified against real hardware:**
The existing `EncodeV2` sets bit 47 on GPS-mode data frames. The live Radarcape
capture confirmed `sync=False` on 0x34 status frames, but data frame sync bit
behaviour was not captured. Our current code sets bit 47 because our timestamps
genuinely are GPS-disciplined, and the previous MLAT validation (-110us systematic
offset, 0.1us stddev against the Radarcape) succeeded with this behaviour.

**Action required during live verification (implementation step 7):** capture real
Radarcape data frames and confirm whether bit 47 is set or clear. If the real
Radarcape clears bit 47 on data frames, we must decide whether to match that
behaviour (wire compatibility) or keep our current behaviour (semantic correctness).
Until verified, the implementation sets bit 47 on GPS-mode data frames as a
deliberate choice, not a claim of wire compatibility.

## Beast Encoder API

### Data frames

```go
// EncodeData produces a 0x32 or 0x33 data frame.
// Mode determines timestamp format. ref is required for ModeRadarcapeGPS
// (programmer error to pass nil ref with GPS mode).
func EncodeData(mode OutputMode, msg regs.Message, ref *pps.ClockRef) []byte
```

Replaces `EncodeV2`. The explicit mode argument eliminates implicit fallback.

### Status frames (0x34)

```go
// EncodeStatus produces a 0x34 Radarcape status frame.
// timestampMode controls how the 6-byte timestamp is encoded.
// reportedMode determines the settings byte content (the mode being announced).
// During transitions, these differ: timestamp uses the old mode so mlat-client
// can parse it, settings byte advertises the new mode.
// toa is the current FPGA counter value (used for the timestamp field; for data
// frames this comes from msg.TOA, but status frames have no message).
// ref is required when timestampMode is ModeRadarcapeGPS.
// ppsDelta is the PPS timing offset byte. Currently always 0 (see design doc
// "PPS delta byte: UNRESOLVED" section). Parameter retained for future use when
// a genuine phase measurement is available.
func EncodeStatus(timestampMode, reportedMode OutputMode, toa uint64, ref *pps.ClockRef, ppsDelta int8) []byte
```

**Two-mode API rationale:** During a mode transition, mlat-client is still in the
old decoder mode when it receives the status frame. The timestamp must be parseable
under the old mode, but the settings byte must advertise the new mode so mlat-client
switches its decoder before the next data frame arrives.

### Position frames (0x35)

```go
// EncodePosition produces a 0x35 Radarcape position frame.
// lat, lon in degrees; alt in metres. No timestamp or signal byte.
func EncodePosition(lat, lon float64, alt float32) []byte
```

## Wire Format Details

### Frame type constants

```go
const (
    TypeShort    = 0x32  // Mode-S short (7 bytes)
    TypeLong     = 0x33  // Mode-S long (14 bytes)
    TypeStatus   = 0x34  // Radarcape status (14 bytes)
    TypePosition = 0x35  // Radarcape position (21 bytes, no timestamp/signal)
)
```

### 0x34 status frame layout

Frame structure: `[0x1A] [0x34] [timestamp 6B] [signal 1B] [message 14B]`

All payload bytes are subject to 0x1A escape doubling.

**Status message payload (14 bytes):**

| Byte | Field | GPS mode value | Legacy mode value |
|------|-------|---------------|------------------|
| 0 | Settings | `0x11` (binary + GPS) | `0x01` (binary, no GPS) |
| 1 | PPS delta | `0x00` (unresolved, see below) | `0x00` |
| 2 | GPS status | `0xFF` | `0x00` |
| 3-13 | Reserved | `0x00` | `0x00` |

**Settings byte bit definitions (from mlat-client `modes_reader.c:791`):**

| Bit | Mask | Meaning |
|-----|------|---------|
| 0 | 0x01 | Protocol: 0=AVR, 1=Beast binary |
| 1 | 0x02 | Filtering: 0=all frames, 1=filtered |
| 2 | 0x04 | AVR variant (used with bit 0 for AVRMLAT) |
| 3 | 0x08 | CRC: 0=check, 1=no check |
| 4 | 0x10 | **Timestamp mode: 0=12MHz legacy, 1=GPS** |
| 5 | 0x20 | RTSCTS: 0=off, 1=on |
| 6 | 0x40 | FEC: 0=enabled, 1=disabled |
| 7 | 0x80 | Mode A/C: 0=off, 1=on |

We emit 0x01 (binary) and 0x10 (GPS when applicable). Other bits are serial-port
or decoder config that don't apply to our TCP output.

**GPS status byte (data[2]) — mlat-client interpretation (`modes_reader.c:787-797`):**

| Bit | Mask | Meaning |
|-----|------|---------|
| 7 | 0x80 | UTC bugfix: when set, seconds field is correct as-is |
| 5 | 0x20 | When set, mlat-client selects RADARCAPE_EMULATED instead of RADARCAPE |
| 4 | 0x10 | sync_ok (when bit 7 set) |
| 3 | 0x08 | utc_offset_ok (when bit 7 set) |
| 2 | 0x04 | sats_ok (when bit 7 set) |
| 1 | 0x02 | tracking_ok (when bit 7 set) |
| 0 | 0x01 | antenna_ok (when bit 7 set) |

**PPS delta byte: UNRESOLVED — emit 0 for v1.**

The real Radarcape emits small values (-1 to 4) in this byte. Radarcape telemetry
labels the quantity in 4 ns units, suggesting it is a phase/timing offset at the PPS
edge, not a frequency error integrated over the whole second.

Our `ClockRef.Carryover` is a frequency error (measuredTicks - nominalTicksPerSec),
which is the wrong physical quantity. We do not currently measure the phase offset
between the PPS edge and the nearest FPGA clock edge, which is what a true PPS delta
would represent.

**v1 implementation:** Emit `0x00` always. This is safe because no known consumer
uses this byte for decision-making.

**Future work:** If we add PPS phase measurement (e.g. via a fine timestamp of the
PPS capture register relative to the FPGA clock), we can populate this byte with a
genuine 4 ns-step value. Validate against live Radarcape captures before claiming
equivalence.

### 0x35 position frame layout

Frame structure: `[0x1A] [0x35] [payload 21B]`

No timestamp or signal byte. All payload bytes are subject to 0x1A escape doubling.

| Bytes | Field | Encoding |
|-------|-------|----------|
| 0-3 | Reserved | `0x00` |
| 4-7 | Latitude | IEEE 754 float32, little-endian, degrees |
| 8-11 | Longitude | IEEE 754 float32, little-endian, degrees |
| 12-15 | Altitude | IEEE 754 float32, little-endian, metres |
| 16-20 | Reserved | `0x00` |

### Position source

- Primary: CLI `--lat`, `--lon`, `--alt` flags
- Fallback: gpsd (existing integration for lat/lon; extend to include altitude)
- If all three are not known, no 0x35 frames are emitted

## Feeder State Machine

### State

```go
type radarcapeState struct {
    currentMode   beast.OutputMode   // mode currently advertised to clients
    desiredMode   beast.OutputMode   // mode derived from GPS/PPS state
    lastGoodRef   *pps.ClockRef      // most recent ref where GpsSync==true, for transitions
    lastStatus    time.Time          // last periodic 0x34 emission
    lastPosition  time.Time          // last periodic 0x35 emission
    position      *receiverPosition  // nil if unknown
}
```

### Constants

```go
const (
    statusInterval   = 1 * time.Second   // match observed Radarcape cadence (~1 Hz)
    positionInterval = 30 * time.Second   // no Radarcape reference; reasonable default
)
```

### Emission rules

**On startup** (before any data frames, radarcape mode only):
1. Emit 0x34 with `timestampMode = desiredMode`, `reportedMode = desiredMode`
2. If position known: emit 0x35
3. Set `currentMode = desiredMode`

Startup is a distinct code path, not a transition from zero-value state.

**On new client connection** (late joiners):
The feeder maintains a set of **welcome frames**: the most recently emitted 0x34
status frame and (if position is known) the most recently emitted 0x35 position
frame. These are updated every time a status or position frame is broadcast.

The server gains an `OnConnect` callback. When a new client connects, the server
calls back to the feeder which sends the welcome frames to that client before it
enters the normal broadcast fan-out. This ensures every client receives a valid
mode advertisement before any data frames, regardless of when it connects.

The welcome frames are stored as pre-encoded `[]byte` slices (the exact bytes
already broadcast), so no re-encoding is needed. The feeder updates them atomically.

**Startup ordering to prevent race:** In radarcape mode, the feeder must generate
and cache the startup 0x34 (and 0x35 if position known) welcome frames **before**
starting the TCP listener. This guarantees that no client can connect before welcome
frames exist. The sequence is:

1. Build startup 0x34/0x35 frames and cache as welcome frames
2. Start TCP listener (server begins accepting clients)
3. Enter main data loop

If `ModeBeast12MHz`, no welcome frames are needed and the server can start immediately.

**On mode transition** (`desiredMode != currentMode`):
1. Emit 0x34 with `timestampMode = currentMode`, `reportedMode = desiredMode`
   - For GPS-to-legacy transitions: use `lastGoodRef` for GPS timestamp encoding
     (ref may already be nil or have `GpsSync=false` by the time the feeder detects
     the mode change, but `lastGoodRef` preserves the timing reference from the last
     valid GPS cycle)
2. Set `currentMode = desiredMode`
3. Reset `lastStatus` (the transition frame counts as a status emission)
4. Update welcome frames (so late joiners see the new mode)

**Periodic** (checked in the feeder loop before broadcasting each data frame):
- If `time.Since(lastStatus) >= statusInterval`: emit 0x34 with both modes = `currentMode`
- If `time.Since(lastPosition) >= positionInterval` and position known: emit 0x35

**ModeBeast12MHz**: all status/position logic is skipped entirely.

### Broadcast ordering per cycle

1. Transition 0x34 (if mode changed)
2. Periodic 0x34 (if due and no transition this cycle)
3. Periodic 0x35 (if due)
4. Data frame

Status before data ensures mlat-client has the correct decoder mode before it sees
the next data timestamp.

### Known limitation

Periodic control frames are emitted in the data path. If no ADS-B traffic is received,
no status or position frames are emitted either. A real Radarcape continues emitting
status frames during idle periods. This can be addressed later by moving periodic
emission to the outer poll loop.

### GPS sync flapping

If PPS sync flaps, a 0x34 is emitted on every transition. The PPS watcher already
guards against flapping (requires stable carryover, no skipped edges, chrony
confirmation). If flapping becomes a problem, the fix belongs in the PPS watcher's
hysteresis, not in the Beast emission layer.

## Fallback Behaviour Summary

| Condition | Behaviour |
|-----------|-----------|
| `--radarcape` disabled | `ModeBeast12MHz` always. No 0x34, no 0x35, pure Beast. |
| `--radarcape` enabled, GPS sync valid | `ModeRadarcapeGPS`. secs+nanos timestamps, 0x34 advertises GPS. Sync bit 47 set on data frames (pending live verification — see mode table note). |
| `--radarcape` enabled, GPS sync lost | `ModeRadarcapeLegacy12MHz`. 12 MHz timestamps, 0x34 advertises legacy. **Not fake GPS.** |
| `--radarcape` enabled, startup before sync | Start `ModeRadarcapeLegacy12MHz`. Transition when sync acquired. |
| `--radarcape` enabled, intermittent sync | Transitions follow PPS watcher state with immediate 0x34 emission. |

## Documented Decision: GPS Status Byte

**Decision:** Emit `0xFF` for the GPS status byte when GPS-synced, `0x00` when not.

**Rationale:** This matches observed real Radarcape behaviour. The real Radarcape
sends `gps=0xFF` (all bits set), which includes bit 5 (0x20). mlat-client
classifies this as `RADARCAPE_EMULATED` rather than `RADARCAPE`. However, this
distinction is functionally harmless in the stack we use:

- Both `RADARCAPE` and `RADARCAPE_EMULATED` parse timestamps identically (1 GHz secs+nanos)
- The mlat-server sets epoch from the initial handshake `clock_type` (`radarcape_gps`),
  not from runtime mode-change messages
- `process_clock_reset_message` on the server ignores the epoch/frequency/mode fields
- MLAT pairing works via relative clock drift, not absolute epoch
- `process_mlat_gps` on the server is marked `#UNUSED`

Clearing bit 5 would be "more correct" per Jetvision's protocol intent but would
diverge from what every real Radarcape emits. We choose wire compatibility over
semantic purity to avoid surprising consumers that may expect the same behaviour
as Jetvision hardware.

**This decision is intentional.** Code comments on `EncodeStatus` reference this
section.

## Documented Decision: Settings Byte Values

| Mode | Settings byte | Derivation |
|------|--------------|------------|
| `ModeRadarcapeGPS` | `0x11` | binary (0x01) + GPS timestamps (0x10) |
| `ModeRadarcapeLegacy12MHz` | `0x01` | binary (0x01) only |

The real Radarcape was observed sending settings byte `0x35` (which includes RTSCTS
0x20 and bit 2 0x04). Those extra bits describe serial-port configuration and do not
apply to our TCP output. No known consumer acts on them beyond the protocol/timestamp
mode bits.

## What We Do Not Implement

- **Config commands** (0x1A '1' 'C' etc.): we are a TCP server, not a serial Beast device
- **Mode A/C frames** (0x31): we do not decode Mode A/C
- **Client-driven mode switching**: mode is determined by PPS/GPS state, not client requests
- **Idle-period status emission**: control frames only emit alongside data frames (v1 limitation).
  Late-joining clients still receive welcome frames on connect, so they are not affected by
  idle periods.

## Test Plan

### Unit tests (`internal/beast`)

| Test | Verifies |
|------|----------|
| TestEncodeDataRadarcapeGPS | GPS timestamp encoding with explicit mode |
| TestEncodeDataLegacy12MHz | 12 MHz encoding under both Beast and Legacy modes (same output) |
| TestEncodeDataGPSModeNilRef | Programmer error: documents that GPS mode requires valid ref |
| TestEncodeStatusGPSMode | Settings=0x11, GPS=0xFF, GPS-format timestamp |
| TestEncodeStatusLegacyMode | Settings=0x01, GPS=0x00, 12 MHz timestamp |
| TestEncodeStatusTransition | timestampMode != reportedMode produces correct mixed frame |
| TestEncodeStatusPpsDelta | Delta byte placed at correct offset; v1 always 0 |
| TestEncodePositionLayout | Lat/lon/alt at correct byte offsets as LE float32 |
| TestGoldenFrames | Update existing golden tests for new API |

### Feeder state machine tests (injected clock via `now time.Time` parameter)

| Test | Verifies |
|------|----------|
| TestStartupEmitsStatus | First broadcast includes 0x34 before data |
| TestStartupEmitsPosition | 0x35 follows 0x34 on startup when position known |
| TestNoPositionNoFrame | No 0x35 when position unknown |
| TestModeTransitionGPSToLegacy | GPS sync loss: 0x34 with old timestamp, new settings |
| TestModeTransitionLegacyToGPS | GPS sync gain: same transition logic |
| TestPeriodicStatusCadence | 0x34 emitted at ~1s intervals |
| TestBeastModeNoStatus | ModeBeast12MHz never emits 0x34 or 0x35 |
| TestGPSToLegacyNilRef | GPS-to-legacy transition uses lastGoodRef for old-mode timestamp |

### E2e test update (`internal/server/e2e_test.go`)

- Verify that radarcape mode produces 0x34 before first data frame
- Verify frame ordering (status then data)
- Verify late-joining client receives welcome 0x34 before data frames
- Keep narrow: order and presence, not every field bit

### Tooling updates

- `tools/inspect_live_timestamps.py`: decode settings byte flags, parse 0x35 position
- `cmd/beast-client`: recognise and print 0x34 and 0x35 frames, skip unknown types

## Implementation Order

1. Explicit mode model and `EncodeData` API change (with test updates)
2. `EncodeStatus` for 0x34 frames
3. Server `OnConnect` callback for welcome frames
4. Feeder state machine for transitions, periodic emission, and welcome frame management
5. `EncodePosition` for 0x35 frames
6. `--alt` flag and gpsd altitude extension
7. Tooling updates (inspect, beast-client)
8. Live verification against Radarcape (including data frame sync bit 47 check)

## Success Criteria

- Running without GPS sync under `--radarcape` advertises legacy 12 MHz mode and no longer
  silently uses GPS timestamp format
- Running with GPS sync produces secs+nanos timestamps that mlat-client parses correctly
  (sync bit 47 behaviour to be confirmed against real Radarcape during live verification)
- mlat-client autodetects mode changes correctly via 0x34 frames
- `/tmp/mlat/sync.json` shows stable pairings with the two Radarcapes
- Inspection tools show 0x34 and 0x35 frames in the stream
- Mode transitions emit 0x34 with correct timestamp/settings byte combination

## References

- mlat-client `modes_reader.c` — authoritative for how 0x34 settings/GPS bytes are interpreted
- mlat-server `coordinator.py:53-56` — epoch set at connection time, not updated on mode change
- Live Radarcape capture: settings=0x35, GPS=0xFF, status cadence ~1 Hz, no 0x35 position observed,
  status frames have sync=False. Data frame sync bit behaviour was not captured — verify during
  implementation step 7
- readsb `net_io.c` — 0x35 position parsing (LE float32 at bytes 4-15 of 21-byte payload)
- Jetvision wiki (Mode-S Beast Data Output/Input Formats, Radarcape Software Features Major2)
