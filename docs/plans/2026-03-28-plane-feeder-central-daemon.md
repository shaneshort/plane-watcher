# plane-feeder Central Daemon Design

**Date:** 2026-03-28
**Status:** Approved

## Overview

Consolidate `plane-feeder` from a Beast-only forwarder into the single
long-running daemon on the Zynq. It gains three new responsibilities:

1. **Radio initialisation** — tune the AD9361 via IIO sysfs at startup
2. **Aircraft tracking** — decode Mode-S/ADS-B fields, maintain a live
   aircraft table with CPR position decode
3. **Web dashboard** — HTTP server exposing stats, radio health, FPGA
   debug counters, and the aircraft table

The existing standalone tools (`regdump`, `regpeek`, `beast-client`,
`pps-check`, `fifo-monitor`) remain for manual diagnostics.

## Architecture

```
FPGA FIFO
    |
    v
FIFO Poller (main goroutine)
    |
    |---> Beast TCP server (port 30005)
    |
    +---> tracker (chan regs.Message)
               |
               v
          map[uint32]*Aircraft
               |
               v
          HTTP server reads (mutex-protected)
```

Three concurrent subsystems after startup:

- **FIFO poller** — existing poll loop with CRC filtering, broadcasts
  Beast frames, sends decoded messages to the tracker channel
- **Aircraft tracker** — goroutine consuming messages, maintains state
- **HTTP server** — serves dashboard UI and JSON API

## Startup Sequence

1. Parse CLI flags
2. Open `/dev/mem`, read FPGA version register — **fatal** if
   `0x00000000` or `0xFFFFFFFF`
3. Tune radio via IIO sysfs — **log warning** on failure, mark
   `radioOK = false` (daemon continues; web UI shows error state)
4. Enable decoder core (write control register)
5. Start Beast TCP server
6. Start HTTP server
7. Start tracker goroutine
8. Enter FIFO poll loop
9. `SIGINT`/`SIGTERM` — graceful shutdown

Radio tune failures are non-fatal because the AD9361 has a known
calibration timeout bug on soft reboot (RESETB tied high). The web UI
and Beast server remain useful for diagnostics, and the user can see
"radio not tuned" on the dashboard and power-cycle.

## Package Layout

```
ps/
  cmd/plane-feeder/main.go    -- startup, flags, orchestration
  radio/radio.go              -- AD9361 IIO sysfs tune + status read
  tracker/tracker.go          -- aircraft state table, message consumer
  web/web.go                  -- HTTP server, JSON endpoints
  web/static/                 -- embedded HTML/JS/CSS (go:embed)
  beast/                      -- Beast encoder (existing)
  crc/                        -- CRC-24 (existing)
  icao/                       -- ICAO filter (existing)
  regs/                       -- register access (existing)
  server/                     -- Beast TCP server (existing)
```

## Radio Package (`ps/radio/`)

Tunes AD9361 via IIO sysfs on startup. Discovers the device by scanning
`/sys/bus/iio/devices/iio:device*/name` for `ad9361-phy`.

**Hardcoded parameters:**
- RX LO: 1,090,000,000 Hz
- RX BW: 2,000,000 Hz
- Sample rate: 30,720,000 Hz
- ENSM mode: FDD

**CLI-configurable:**
- Gain: `--gain` flag (default 54 dB, manual mode)

**Status reads** (for web dashboard):
- Current LO frequency, bandwidth, sample rate
- Gain mode and current gain dB
- RSSI
- RX port select

No config file support for now. Frequency and bandwidth are
single-purpose (1090 MHz ADS-B receiver). Config file support can be
added later when the base system image supports it.

## Tracker Package (`ps/tracker/`)

```go
type Aircraft struct {
    ICAO     uint32
    Callsign string      // DF17 TC=1-4
    Altitude int64       // feet; DF0/4/16/17/20
    Lat, Lon float64     // CPR local decode
    Squawk   string      // DF5/21
    Signal   uint8       // last signal level
    Seen     time.Time   // last message timestamp
    Messages uint64      // total count
}

type Tracker struct {
    mu       sync.RWMutex
    aircraft map[uint32]*Aircraft
    refLat   float64     // receiver position
    refLon   float64
}
```

- **Dependency:** `kreklow.us/go/go-adsb` for message decoding
  (callsign, altitude, squawk, CPR). Pure Go, one transitive dependency
  (`go-safecast`), no CGO.
- **CPR decode:** Local decode only, using receiver position from
  `--lat`/`--lon` flags. Global decode (even/odd pair) deferred until
  needed.
- **Expiry:** Aircraft entries removed after 60 seconds with no
  messages (matches ICAO filter TTL).
- **Input:** Buffered `chan regs.Message` from the FIFO poller.
- **Concurrency:** Single goroutine owns the map; HTTP server reads via
  `RLock`.

## Web Package (`ps/web/`)

**Endpoints:**

| Route             | Method | Description                                    |
|--------------------|--------|------------------------------------------------|
| `/`               | GET    | Dashboard HTML (embedded via `go:embed`)       |
| `/api/stats`      | GET    | System stats JSON                              |
| `/api/stats?debug=1` | GET | System stats + all FPGA debug counters         |
| `/api/aircraft`   | GET    | Aircraft table JSON                            |

**Stats JSON includes:**
- Message count, drop count, message rate
- ICAO filter size, Beast client count
- PPS count and status
- Uptime
- Radio config (LO, BW, gain, RSSI, tuned OK flag)
- Optionally: all 38+ FPGA debug counters (when `?debug=1`)

**Dashboard UI:**
- Single HTML page with vanilla JS, no framework
- Polls `/api/stats` and `/api/aircraft` every 2 seconds via `fetch()`
- Stats section: message rate, clients, PPS, uptime, radio state
- Aircraft table: ICAO, callsign, altitude, lat/lon, squawk, signal,
  age (seconds since last seen)
- "Advanced" toggle reveals full FPGA debug counter table
- All assets embedded in the binary via `go:embed`

**Port:** `--http-port` flag, default `8080`

## CLI Flags

| Flag           | Default       | Description                              |
|----------------|---------------|------------------------------------------|
| `--base-addr`  | `0x43D00000`  | AXI register base address                |
| `--port`       | `30005`       | Beast TCP output port                    |
| `--http-port`  | `8080`        | Web dashboard port                       |
| `--radarcape`  | `false`       | Radarcape timestamp format               |
| `--gain`       | `54`          | AD9361 RX gain (dB, manual mode)         |
| `--lat`        | `-31.94`      | Receiver latitude (CPR decode)           |
| `--lon`        | `115.97`      | Receiver longitude (CPR decode)          |
| `--mock`       | `false`       | Mock reader, no hardware                 |

## Build

Single static binary, cross-compiled for Zynq (ARM7):

```
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -o bin/plane-feeder ./cmd/plane-feeder
```

No CGO, no external file dependencies. All web assets embedded via
`go:embed`. The `go-adsb` dependency and its transitive dependency
(`go-safecast`) are pure Go.

## What Stays Standalone

| Tool           | Reason                                           |
|----------------|--------------------------------------------------|
| `regdump`      | Manual diagnostics, CSV export, one-shot use     |
| `regpeek`      | Raw register access for debugging                |
| `beast-client` | Test tool for verifying Beast stream              |
| `pps-check`    | One-shot PPS timing validation                   |
| `fifo-monitor` | Live FIFO fill monitoring                        |

These tools are unmodified. They continue to work independently via
`/dev/mem`.

## Future Work (Not In Scope)

- Config file support (YAML/TOML) for gain and other parameters
- GPS integration for automatic receiver position
- Global CPR decode (even/odd message pairing)
- Radarcape GPS position messages (type `0x35`)
- Beast heartbeat messages
