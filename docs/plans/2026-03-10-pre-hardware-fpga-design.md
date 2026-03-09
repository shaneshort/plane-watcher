# Pre-Hardware FPGA Development Plan

## Purpose

Define the work that can begin before the Zynq-7020 development board arrives: simulation environment, adapted VHDL decode pipeline, and new timing modules.

## Reference Implementation

The [bladeRF-adsb](https://github.com/Nuand/bladeRF-adsb) project provides a proven FPGA-based ADS-B decoder in VHDL. The decode pipeline (preamble detection, multi-decoder architecture, soft-decision decoding, bit-flip error correction, CRC validation) is directly applicable. The bladeRF-specific frontend (Fs/4 mixing) is not.

Key architectural additions over bladeRF-adsb:

- Hardware timestamp counter with PPS capture (MLAT support)
- Timestamp threading through the decode pipeline
- Parameterised design (sample rate, decoder count, counter width)
- Beast binary output support (Linux-side, informed by FPGA output record)

---

## Project Structure

```
plane_watcher/
├── hdl/
│   ├── rtl/                        # Synthesisable VHDL
│   │   ├── adsb_pkg.vhd               # Constants, types, generics
│   │   ├── adsb_fe.vhd                # Frontend: FIR filter + I^2+Q^2 power
│   │   ├── adsb_edge_detector.vhd     # Leading edge detection
│   │   ├── preamble_detector.vhd      # Preamble correlation + RPL
│   │   ├── bsd_calculator.vhd         # Bit soft-decision scoring
│   │   ├── smallest_bsds.vhd          # Track N weakest bits
│   │   ├── bit_flipper.vhd            # Error correction: flip weak bits + CRC retry
│   │   ├── message_decoder.vhd        # Single decoder instance
│   │   ├── adsb_crc.vhd               # CRC-24 validation
│   │   ├── adsb_decoder.vhd           # Top-level: N parallel decoders
│   │   ├── message_aggregator.vhd     # First-free output collection with TOA
│   │   ├── timestamp_counter.vhd      # 64-bit counter + PPS capture
│   │   ├── msg_fifo.vhd               # Synchronous FWFT FIFO
│   │   ├── axi_regs.vhd               # AXI4-Lite slave register interface
│   │   └── adsb_top.vhd               # Top-level: pipeline + AXI + FIFO
│   ├── tb/                         # Testbenches
│   │   ├── adsb_crc_tb.vhd
│   │   ├── timestamp_counter_tb.vhd
│   │   ├── adsb_decoder_tb.vhd       # Full pipeline (vectors + live)
│   │   ├── axi_regs_tb.vhd           # End-to-end AXI register interface
│   │   └── edge_detector_probe_tb.vhd
│   └── sim/                        # Simulation scripts
│       ├── Makefile                    # GHDL build + run targets
│       ├── vectors/                    # Test vectors + live captures
│       └── waves/                      # GTKWave save files
├── tools/
│   ├── gen_test_vectors.py         # ADS-B test vector generator
│   ├── decode_sim_log.py           # GHDL log → decoded Mode-S output
│   ├── compare_results.py          # Sim vs dump1090 comparison tool
│   └── hackrf_capture.py           # HackRF raw → testbench format
├── bladeRF-adsb/                   # Reference implementation (cloned)
├── docs/
└── README.md
```

---

## Design Parameters

Centralised in `adsb_pkg.vhd`:

| Parameter | Default | Notes |
|-----------|---------|-------|
| `SAMPLE_RATE_HZ` | 16_000_000 | AD9363 target; drives SPS calculation |
| `SPS` | 8 | Samples per PPM chip at 16 MSPS |
| `SPB` | 2 | Samples per bit (2 chips per bit) |
| `NUM_DECODERS` | 8 | Parallel decoders (Gold Mode) |
| `COUNTER_WIDTH` | 64 | Timestamp counter bits |
| `INPUT_POWER_WIDTH` | 24 | Power signal width |
| `FIR_TAPS` | 83 | Frontend filter length |
| `FIR_CPS` | 2 | FIR clocks per sample |
| `NUM_WEAK_BITS` | 5 | Bits for brute-force error correction |

---

## Module Details

### Ported from bladeRF-adsb (with adaptations)

#### `adsb_fe.vhd` — Frontend

Adapted from bladeRF `adsb_fe.vhd`.

Changes:

- Remove Fs/4 mixer (not needed; ADC delivers baseband or direct samples)
- Keep FIR filter with parameterised coefficients and tap count
- Keep I^2 + Q^2 power calculation (more accurate than |I|+|Q|, DSP slices available on 7020)
- FIR coefficients parameterised via package for later tuning

#### `adsb_edge_detector.vhd` — Edge Detection

Adapted from bladeRF `adsb_edge_detector.vhd`.

Changes:

- Parameterise `SPS` from package instead of hardcoded 8
- Otherwise functionally identical

#### `preamble_detector.vhd` — Preamble Correlation

Adapted from bladeRF `preamble_detector.vhd`.

Changes:

- Parameterise `SPS` from package
- Add timestamp latch: when preamble is detected, capture current `counter_value` from timestamp core
- Thread timestamp + RPL into decoder allocation
- First-free decoder assignment (replaced round-robin): scans for first non-busy decoder slot instead of cycling through a fixed index
- Quiet-zone validation: requires low power between preamble pulses, rejecting most data-content false triggers

This is the critical integration point between the bladeRF decode logic and the new timing architecture.

#### `bsd_calculator.vhd` — Bit Soft-Decision

Adapted from bladeRF. Parameterise `SPS`. Otherwise functionally identical.

#### `smallest_bsds.vhd` — Weakest Bit Tracking

Adapted from bladeRF. Added `done` flag to prevent bits register corruption after 112th BSD (original bug: extra bsd_valid pulses from safety margin shifted the register, destroying decoded message before CRC iteration).

#### `bit_flipper.vhd` — Error Correction

Adapted from bladeRF. Two fixes:
- Fixed `calculate_flip_mask` index mapping (transmission order → accumulator position, using `111 - index`)
- Added `valid_df` early-exit: invalid DF fields skip 32-iteration CRC brute-force (~30 µs saved per false trigger)

#### `adsb_crc.vhd` — CRC-24

Near drop-in from bladeRF. Reference our package for types.

#### `message_decoder.vhd` — Single Decoder

Adapted from bladeRF. Thread timestamp value from SOM (start of message) through to output alongside decoded message bits.

#### `message_aggregator.vhd` — Output Collection

Adapted from bladeRF. TOA carried through holding registers alongside messages. Added `in_toas`/`out_toa` ports for MLAT timestamp passthrough.

#### `adsb_decoder.vhd` — Top-Level Decoder

Adapted from bladeRF. `NUM_DECODERS` already a generic. Connects timestamp counter, preamble detector, and N decoders.

### New Modules

#### `adsb_top.vhd` — Top-Level Wrapper

New module. Wires together the complete decode pipeline with AXI register interface:

```
in_power → adsb_decoder → message_aggregator → msg_fifo → axi_regs → AXI bus
```

Generics: `NUM_DECODERS`, `FIFO_DEPTH`, `C_S_AXI_DATA_WIDTH`, `C_S_AXI_ADDR_WIDTH`

#### `msg_fifo.vhd` — Message FIFO

New module. Synchronous first-word-fall-through (FWFT) FIFO between message_aggregator and AXI registers. Parameterised depth (default 64). Stores 176-bit entries (112-bit message + 64-bit TOA).

#### `axi_regs.vhd` — AXI4-Lite Register Interface

New module. PS ↔ PL interface for message readout, configuration, and status.

Register map (6-bit address space):

| Offset | Name | Access | Description |
|--------|------|--------|-------------|
| 0x00 | MSG_DATA_0 | R | Message bytes [31:0] |
| 0x04 | MSG_DATA_1 | R | Message bytes [63:32] |
| 0x08 | MSG_DATA_2 | R | Message bytes [95:64] |
| 0x0C | MSG_DATA_3 | R | Message bytes [111:96] + padding |
| 0x10 | TOA_LO | R | Timestamp [31:0] |
| 0x14 | TOA_HI | R | Timestamp [63:32] |
| 0x18 | RPL | R | Signal level [23:0]; **auto-pops FIFO on read** |
| 0x1C | STATUS | R | Bit 0: not_empty; [14:8]: FIFO count |
| 0x20 | PPS_COUNT | R | Total PPS events |
| 0x24 | PPS_LATCH_LO | R | Counter at last PPS [31:0] |
| 0x28 | PPS_LATCH_HI | R | Counter at last PPS [63:32] |
| 0x2C | CONTROL | RW | Bit 1: enable decoders |
| 0x30 | VERSION | R | 0x00010000 (v1.0.0) |

#### `timestamp_counter.vhd` — Timing Core

New module. Central timebase for the receiver.

```
Generics:
    COUNTER_WIDTH   : positive := 64

Ports:
    clock           : in  std_logic           -- sample clock domain
    reset           : in  std_logic

    counter_value   : out unsigned(COUNTER_WIDTH-1 downto 0)
    counter_at_pps  : out unsigned(COUNTER_WIDTH-1 downto 0)
    pps_count       : out unsigned(31 downto 0)
    pps_new         : out std_logic           -- pulses 1 cycle on PPS edge

    pps_in          : in  std_logic           -- raw PPS from GNSS
```

Internal design:

- Free-running counter, increments every rising edge of sample clock
- PPS input: double-flop synchroniser, then rising-edge detect
- On PPS rising edge: latch counter into `counter_at_pps`, increment `pps_count`, assert `pps_new` for one cycle

At 16 MHz with 64-bit counter, rollover period is ~36,000 years.

---

## FPGA Output Record

Every decoded message delivered to the PS carries:

```vhdl
type adsb_message_t is record
    data        : std_logic_vector(111 downto 0);   -- 14 bytes message
    timestamp   : unsigned(COUNTER_WIDTH-1 downto 0); -- raw counter at TOA
    fractional  : unsigned(15 downto 0);             -- sub-sample offset
    rpl         : signed(INPUT_POWER_WIDTH-1 downto 0); -- signal level
    valid       : std_logic;
end record;
```

Linux converts this to Beast binary format:

```
Beast frame: 0x1A | type | 6-byte timestamp (scaled to 12MHz convention) | signal_level | message_bytes
```

The 64-bit counter at 16 MHz is scaled to the Beast 48-bit / 12 MHz convention in userspace:

```
beast_timestamp = (counter_at_toa - counter_at_pps) * 12 / 16
```

---

## Simulation Environment

### Tooling

- **GHDL** — VHDL compilation and simulation (open source, fast, macOS via Homebrew)
- **GTKWave** — waveform viewer
- **Python** — test vector generation

### Test Vector Generator (`tools/gen_test_vectors.py`)

Generates binary I/Q sample files from known ADS-B messages.

Capabilities:

- Encode known hex messages as Mode-S PPM waveforms
- Add Mode-S preamble
- Upsample to target sample rate (default 16 MSPS)
- Add configurable Gaussian noise
- Add signal level variation
- Generate overlapping packets (collision testing)
- Output binary I/Q format compatible with VHDL testbench file I/O

### Testbench Hierarchy

Bottom-up verification:

| Testbench | Verifies |
|-----------|----------|
| `adsb_crc_tb` | CRC passes known-good messages, rejects corrupted ones |
| `timestamp_counter_tb` | Counter increments, PPS latches at correct value, rollover behaviour |
| `preamble_detector_tb` | Detects preamble at correct sample index, outputs valid RPL |
| `message_decoder_tb` | Single decoder: known message → correct bits + passed CRC |
| `adsb_tb` | Full pipeline: I/Q file → decoded messages, verified against known inputs |

### Collision Test

Generate two overlapping ADS-B packets at different power levels. Verify that with `NUM_DECODERS > 1`, both messages are recovered. This validates the Gold Mode multi-decoder architecture.

### Makefile Targets

```
make analyse          # GHDL compile all sources
make sim_crc          # Run CRC testbench
make sim_preamble     # Run preamble detector testbench
make sim_timestamp    # Run timestamp counter testbench
make sim_decoder      # Run single decoder testbench
make sim_full         # Run full pipeline testbench
make sim_all          # Run all testbenches
make vectors          # Generate test vectors
make waves_full       # Run full TB + open GTKWave
make clean            # Remove build artifacts
```

---

## Clock Strategy

Target: 16 MHz sample clock derived from GPSDO 10 MHz via FPGA PLL (MMCM).

```
GPSDO 10 MHz → FPGA MMCM → 16 MHz sample clock
                                 ├── DSP pipeline
                                 └── timestamp counter

GPSDO PPS → FPGA GPIO → timestamp_counter.pps_in
```

All design parameters are derived from `SAMPLE_RATE_HZ` so the pipeline adapts if the clock rate changes (e.g. 20 MHz or 40 MHz with a different ADC).

At 16 MHz: 1 LSB = 62.5 ns. With quadratic peak interpolation providing ~4-8x refinement, effective timing precision is approximately 8-15 ns — adequate for MLAT.

---

## Resource Estimate (Zynq-7020)

| Resource | 7020 Available | Estimated Usage | Headroom |
|----------|---------------|-----------------|----------|
| LUTs | 53,200 | ~10,000-14,000 | Comfortable |
| FFs | 106,400 | ~8,000-12,000 | Comfortable |
| DSP48 | 220 | ~40-50 | Comfortable |
| BRAM (36Kb) | 140 | ~15-25 | Comfortable |

### 7010 Feasibility (for later cost-down evaluation)

| Resource | 7010 Available | Fit? |
|----------|---------------|------|
| LUTs | 17,600 | Tight at 8 decoders; 4 decoders fits |
| DSP48 | 80 | Tight with dual FIR; increase CPS or shorten filter |
| BRAM | 60 | Likely fine |

Mitigation levers for 7010: reduce `NUM_DECODERS`, increase `FIR_CPS`, shorten FIR, real-only input (halves FIR cost).

---

## Current Status (2026-03-15)

### Completed

- **WS1 — Simulation Environment:** GHDL 6.0.0, GTKWave, Python/uv tooling all working
- **WS2 — Port and Adapt bladeRF VHDL:** All 14 RTL modules ported/created, compiling clean
- **WS3 — Timestamp Threading:** Full TOA path wired: timestamp_counter → preamble_detector → message_decoder → adsb_decoder.out_toas → message_aggregator.out_toa. Verified with test vectors (TOA differences match sample offsets exactly)
- **GPS/PPS Timing:** Complete. 64-bit free-running counter with double-flop PPS synchroniser, rising edge detect, counter latch, and PPS event counter. Exposed via AXI registers (PPS_COUNT, PPS_LATCH_LO/HI). Testbench verified. Remaining GPS work is board-bringup (GPSDO PLL configuration on real Zynq hardware)
- **Error Correction:** Two bugs fixed in original bladeRF code (bits register corruption in smallest_bsds, flip mask index mismatch in bit_flipper). Brute-force CRC search now working
- **DF Validation:** Early-exit on invalid DF fields — skips ~73% of false triggers immediately
- **Message Length:** EXTENDED_MESSAGE_LENGTH shared in adsb_pkg (1.25× = 2240 samples = 140 µs). Used by both message_decoder (sample gating) and preamble_detector (detection holdoff)
- **First-Free Assignment:** Replaced round-robin decoder allocation with first-free scan
- **AXI-Lite Register Block:** Complete. `adsb_top.vhd` wires full pipeline (adsb_decoder → message_aggregator → msg_fifo → axi_regs). Register map: MSG_DATA_0..3 (message bytes), TOA_LO/HI (64-bit timestamp), RPL (signal level, auto-pops FIFO on read), STATUS (FIFO not_empty + count), PPS_COUNT, PPS_LATCH_LO/HI, CONTROL (enable/disable), VERSION. Verified end-to-end with `axi_regs_tb`
- **Preamble Detector Overhaul:** Unified quality gate (4 pulse thresholds + 4 quiet zones + aggregate SNR) replaces the previous edge-qualified detection. Weak signals (2 of 4 edges or fewer) are intentionally accepted. Peak detection collapses adjacent qualifying windows into one claim. Message-length holdoff (140 µs) prevents payload-induced re-triggers from saturating decoders. `pending_downcount` reduced from 3 to 2 to compensate for peak detector's 1-cycle latency
- **Short-Frame Early Completion:** `smallest_bsds` asserts `finished` after 56 bits when DF < 16 (short message), halving decoder occupancy for DF0/4/5/11. Weak-bit policy: `idx < 32` (payload bits only) — PI/CRC bits are never flip candidates
- **Live Capture Results:** 10M-sample (625 ms) comparison: 16 decoded, 15 matched software reference (75%), 1 sim-only CRC-valid, 0 invalid. Short-frame change was neutral (same 15/20 match rate). Full results in `tools/capture2_10M_results.log`
- **Comparison Tooling:** `tools/compare_results.py` (sim vs reference), `tools/scan_iq.py` (software IQ decoder for reference generation)
- **Test Vectors:** Clean, noisy, sequential, collision, short message, and 5 weak-BSD error correction vectors
- **Detector Regression:** `make sim_detector_regression` verifies single-packet decoder allocation (single_clean, single_short, two_sequential, collision — all PASS)
- **Decode Script:** `tools/decode_sim_log.py` parses GHDL logs into human-readable Mode-S output with TOA

### Testbenches

| Testbench | Tests | Status |
|-----------|-------|--------|
| `adsb_crc_tb` | 3 (good, corrupted, zero) | PASS |
| `timestamp_counter_tb` | 4 (increment, PPS, 2nd PPS, pulse width) | PASS |
| `adsb_decoder_tb` | Full pipeline with test vectors + live captures | PASS |
| `axi_regs_tb` | End-to-end AXI: VERSION, STATUS, FIFO read, CONTROL, PPS | PASS |
| `edge_detector_probe_tb` | Edge detection probe | PASS |
| `sim_detector_regression` | 4 decoder-allocation checks (clean, short, sequential, collision) | PASS |

### Known Issues

- **weak3-5 test vectors:** Regressed after preamble detector peak detection change (pre-existing, not related to short-frame work). The 1-cycle peak latency shifts alignment for marginal signals with 3+ ambiguous bits
- **RPL overflow:** 27-bit signed sums can produce negative RPL values for very strong signals. Cosmetic (doesn't affect decode), but should be cleaned up

### Remaining (pre-hardware)

- **RPL/accumulator overflow cleanup** — Fix negative RPL for strong signals. Low priority but worth addressing before hardware
- **PS/Linux consumer path** — Beast binary output, FIFO polling, message forwarding to feed1090/readsb. Critical for end-to-end utility
- **Frontend FIR filter** — Matched filter for improved SNR at range, fewer false preambles. Polish item, not blocking
- **Message aggregator TB** — Not yet exercised independently
