# plane_watcher

FPGA-first ADS-B / Mode-S receiver with hardware timestamping for MLAT,
targeting a Zynq-7020 platform (Fishball / PlutoSDR-compatible Rev.C).

## Current Status

**As of 2026-04-04**: End-to-end working on hardware with GPS-disciplined MLAT timestamps.

- AD9363 RX chain live: samples flow through FIR → fractional resampler (30.72 MHz → 16 MHz) → FPGA decode pipeline
- 8 parallel decoders with BSD soft-decision error correction
- Score-based preamble detection with runtime-tunable thresholds via AXI
- Beast TCP output on port 30005 (standard and Radarcape format)
- GPS PPS-disciplined 100 MHz timestamps: sub-microsecond accuracy, validated against Radarcape reference
- chrony NTP discipline to PPS: <1 µs offset
- Embedded web dashboard and JSON API on port 8080
- Runtime control of radio gain and detector thresholds via HTTP

## Repository Layout

```
hdl/rtl/          20 synthesisable VHDL modules for the PL pipeline
hdl/tb/           6 VHDL testbenches
hdl/sim/          GHDL simulation targets, Makefile, sweep scripts
hdl/vivado/       Vivado project scaffold, TCL build scripts, constraints
linux-dts/        Board device-tree sources for stock-tap and hybrid flows
boot/             Boot-partition and initramfs-overlay artifacts
ps/               Go userspace: daemon, tools, and tests
docs/             Architecture, hardware, roadmap, and planning notes
docs/wiki/        Newcomer-oriented walkthrough with diagrams
tools/            Vector generation, capture inspection, build/deploy wrappers
firmware/         Board firmware (submodule — PlutoSDR 7020 fork)
```

For PS command usage and API details, see `ps/README.md`.
For HDL/Vivado build and deploy flow, see `hdl/vivado/README.md`.
For HDL directory roles and generated-output policy, see `hdl/README.md`.
For tooling categories and usage, see `tools/README.md`.

## Build Environment

### Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Vivado | 2025.2 | FPGA synthesis, implementation, bitstream |
| GHDL | 6.0.0+ | RTL simulation |
| GTKWave | any | Waveform viewing |
| Go | 1.22+ | PS userspace tools |
| Python (via `uv`) | 3.12+ | Test vector generation, capture analysis |
| ARM cross-compiler | Go cross-build | `GOARCH=arm GOARM=7` for Pluto target |

### Quick Start

```sh
# Configure machine-local paths (Vivado install, vendor HDL, target board)
cp tools/plane_watcher.env.example tools/plane_watcher.env
$EDITOR tools/plane_watcher.env

# Build bitstream (stock-tap flow)
./tools/build-bitstream.sh --skip-deploy

# Package .bit.bin for SD card
./tools/deploy-bitstream.sh --generate-only

# Or build and deploy in one step
./tools/build-bitstream.sh --deploy

# Build PS tools for the board
cd ps && ./build-arm-tools.sh

# Build PS tools for the host
cd ps && ./build-tools.sh

# Run RTL simulation
cd hdl/sim && make sim_all
```

## Implemented PS Components

Eleven Go commands under `ps/cmd/`:

- `plane-feeder`: main daemon — reads AXI registers, polls FIFO, tracks aircraft, encodes Beast frames (standard + Radarcape), serves Beast TCP on port 30005, hosts the dashboard/API on port 8080, manages PPS/GPS time correlation
- `regdump`: register inspector — shows VERSION, STATUS, radio IIO settings, pipeline debug counters; optionally pops FIFO
- `fifo-monitor`: continuous status monitor — polls FIFO fill level, flags, PPS count
- `pps-check`: PPS sanity checker — validates counter cadence and frequency
- `regpeek`: raw register hex dump utility — reads N consecutive 32-bit registers from a base address
- `replay`: hardware-free replay tool — streams reference/sim messages to Beast TCP clients
- `beast-client`: Beast stream consumer — connects to server, parses and verifies frames
- `collect-stats`: HTTP polling helper — samples `/api/stats?debug=1` into CSV for overnight or tuning runs
- `tune-detector`: detector sweep helper — applies detector settings over HTTP and measures decode/debug deltas
- `sweep-gain`: manual gain sweep tool — steps through gain values via HTTP and records decode metrics
- `watch-stats`: terminal dashboard — polls stats API and renders rolling charts for message rate, valid rate, aircraft count, and frontend headroom

Supporting packages:

- `regs`: AXI register access, FIFO pop semantics, and raw-message decoding
- `beast`: Beast frame encoding (standard and Radarcape v2 with UTC timestamps)
- `server`: TCP listener and multi-client fan-out with welcome-frame support
- `tracker`: live aircraft state derived from decoded frames
- `web`: embedded dashboard plus JSON API for stats, aircraft, radio gain, and detector control
- `chrony`: chrony NTP tracking source queries
- `pps`: PPS GPIO edge monitoring and frequency measurement
- `radio`: AD9363 IIO radio control (gain mode, manual gain)
- `crc`: Mode-S CRC-24 computation
- `reorder`: timestamp-based frame reordering for multi-decoder output
- `diag`: rejected frame diagnostics
- `icao`: ICAO address filtering

## Implemented PL Components

20 RTL modules under `hdl/rtl/` implement the full pipeline:

**Core pipeline:**
- `adsb_pkg.vhd` — shared constants, types, detection thresholds
- `adsb_edge_detector.vhd` — front-end energy detection
- `preamble_detector.vhd` — preamble pattern matching + soft-decision
- `adsb_decoder.vhd` — message framing controller
- `message_decoder.vhd` — per-message BSD decode engine
- `bit_flipper.vhd` — soft-decision bit correction
- `bsd_calculator.vhd` — Berlekamp–Massey soft-decision scoring
- `smallest_bsds.vhd` — weak-bit weak-symbol filtering
- `message_aggregator.vhd` — multi-decoder message arbitration

**I/O and clocking:**
- `iq_to_power.vhd` — complex I/Q magnitude (|I|+|Q|)
- `power_downsampler.vhd` — fractional rate conversion (30.72 MHz → 16 MHz)
- `vendor_rx_ingress.vhd` — AD9363 sample ingress wrapper
- `timestamp_counter.vhd` — 64-bit counter + PPS capture at 100 MHz
- `async_msg_fifo.vhd` — clock-domain-crossing message FIFO
- `async_sample_fifo.vhd` — clock-domain-crossing sample FIFO

**AXI / top level:**
- `axi_regs.vhd` — AXI4-Lite register block (messages, debug counters, control)
- `adsb_top.vhd` — top-level integration
- `adsb_vendor_wrapper.vhd` — board integration layer
- `adsb_pl_wrapper.vhd` — std_logic wrapper for non-vendor sample-domain integration
- `adsb_crc.vhd` — CRC computation

## Test Coverage

**PS Go tests** are co-located with the packages they exercise under `ps/internal/` and `ps/cmd/`:
- register decode, Beast encoding (standard + Radarcape), replay parsing
- golden end-to-end frame tests
- integration test for mock FIFO to TCP Beast output
- chrony, PPS, radio, reorder, tracker, and web package tests

**RTL testbenches** in `hdl/tb/` (runnable via `hdl/sim/Makefile`):
- `adsb_crc_tb.vhd` — CRC polynomial and syndrome computation
- `adsb_decoder_tb.vhd` — decoder state machine + vector regression
- `timestamp_counter_tb.vhd` — counter + PPS capture synchronisation
- `axi_regs_tb.vhd` — AXI4-Lite register interface + FIFO pop semantics
- `edge_detector_probe_tb.vhd` — edge detection logic
- `async_sample_fifo_tb.vhd` — CDC sample FIFO behaviour

**Simulation targets** in `hdl/sim/`:
- decoder regression targets (real captures, synthetic vectors)
- weak-signal test vectors (weak1–weak5)
- preamble alignment sweep script

**Known status**: weak1 and weak2 pass; weak3–weak5 still need tuning.

## Bitstream Flow

The **stock-tap** flow is the active build path. It sources the vendor Pluto
block design, then taps in the ADS-B decoder wrapper, PPS input, and GPS UART0
EMIO on top.

```sh
# Build only
./tools/build-bitstream.sh --skip-deploy

# Build + deploy to board
./tools/build-bitstream.sh --deploy

# Package .bit.bin locally (for SD card or manual install)
./tools/deploy-bitstream.sh --generate-only

# Deploy a previously built bitstream
./tools/deploy-bitstream.sh
```

Machine-specific paths and defaults belong in `tools/plane_watcher.env`, using
`tools/plane_watcher.env.example` as the template.

A vendor-hybrid flow (`build_vendor.tcl`) is retained but is not the active
integration path.

## Timestamping

Project rule:

> Timestamps originate in FPGA hardware, not in Linux userspace.

Implementation:

- **Timestamp counter**: 64-bit free-running counter at 100 MHz (S_AXI_ACLK)
- **PPS capture**: double-flop sync + rising edge detect, latches counter value on each PPS edge
- **GPS**: u-blox F9P on JP5 via EMIO UART0; PPS on V10 → FPGA + EMIO GPIO[17]
- **chrony discipline**: PPS via pps-gpio driver, sub-microsecond offset
- **Beast timestamps**: PS converts FPGA TOA (100 MHz) to 12 MHz Beast time: `toa * 3 / 4`
- **Radarcape mode**: hardware PPS + chrony-disciplined wall-clock correlation for UTC-stamped Beast frames
- **Validated**: 885 matched frames against Radarcape reference, 0.1 µs stddev, 100% within 1 µs

## Operational Surfaces

- **Beast TCP**: port `30005` (standard or Radarcape format via `-radarcape` flag)
- **Dashboard**: `http://<host>:8080/`
- **JSON API**: `GET /api/stats`, `GET /api/stats?debug=1`, `GET /api/aircraft`
- **Control API**: `POST /api/radio/gain-mode`, `POST /api/radio/gain`, `POST /api/detector/quiet-score-shift`, `POST /api/detector/snr-ratio-shift`

## Development Platform

The active development target is a Zynq-7020 class board (Fishball /
PlutoSDR-compatible Rev.C with AD9363). A 7010 cost-down target may be
evaluated later.

## Documentation Map

**Current state & reference:**
- `docs/ARCHITECTURE.md`: current architecture, clock domains, data path, implementation status
- `docs/ROADMAP.md`: phased development plan with current progress
- `docs/wiki/README.md`: newcomer-oriented walkthrough from ADS-B basics through the network output path

**Design reference (pre-hardware, kept for context):**
- `docs/HARDWARE.md`: original hardware architecture spec
- `docs/DSP_DESIGN_SPEC.md`: signal processing pipeline overview
- `docs/ADC_SELECTION.md`: ADC selection rationale

**Frozen interfaces & detailed plans:**
- `docs/plans/2026-03-15-ps-pl-contract.md`: frozen AXI register map + Beast wire protocol
- `docs/plans/2026-03-16-hybrid-bitstream-handoff.md`: Zynq-7020 hybrid bitstream build record
- All other `docs/plans/*.md`: historical implementation and planning notes

**Live tracking:**
- `TODO.md`: active backlog and follow-up work

## Known Limitations

- AD9361 calibration timeout on soft reboot (RESETB hard-tied high; requires power cycle)
- Weak-signal detector tuning incomplete (weak3–weak5 test vectors)
- Radarcape UTC seconds are chrony-correlated, not latched at the exact PPS edge
- CDC debug-counter snapshot uses waivers, not a proper handshake (acceptable for bring-up)
