# Log Detector Frontend: AD936x Decoupling Assessment

## Overview

This document assesses the work required to decouple the ADS-B decode pipeline
from the AD9363 IQ frontend and support an alternative RF chain using a
logarithmic detector and ADC.

## RF Chain

```
TQP3M9036 → TA0691 → TQP3M9036 → TA0691 → AD8318 → AD8009 → AD9238 → FPGA
  LNA #1    SAW #1    LNA #2    SAW #2    log det   op-amp   12b ADC
  ~20dB    1090 MHz   ~20dB    1090 MHz   -24mV/dB  buffer   65 MSPS
```

- **TQP3M9036**: Wideband LNA, two stages for ~40 dB total gain.
- **TA0691**: 1090 MHz SAW bandpass filter, one after each LNA.
- **AD8318**: Logarithmic detector. Outputs voltage proportional to RF envelope
  power in dBm. Negative slope (-24 mV/dB). ~60 dB dynamic range. ~10 ns
  response time (easily tracks 500 ns ADS-B pulses).
- **AD8009**: High-speed op-amp. Conditions the AD8318 output for the ADC input
  range (offset and gain).
- **AD9238**: Dual 12-bit 65 MSPS ADC. Parallel CMOS output (not LVDS).
  Both channels wired to the FPGA.

## Current Architecture: Natural Decoupling Boundary

The architecture has a clean split at `adsb_pl_wrapper.vhd`. Its port interface
is frontend-agnostic:

```vhdl
sample_power  : in  std_logic_vector(23 downto 0)  -- scalar power
sample_valid  : in  std_logic                       -- strobe
sample_clk    : in  std_logic                       -- clock
```

Everything from `adsb_pl_wrapper` downward (`adsb_top` → decoders → aggregator
→ FIFO) has zero knowledge of the AD9363. It consumes a 24-bit signed scalar at
an effective 16 MSPS. That is the contract.

All AD936x-specific code lives in exactly three modules above that boundary:

| Module | AD936x Coupling | Purpose |
|--------|----------------|---------|
| `adsb_vendor_wrapper.vhd` | Heavy — IQ ports, rx_clk CDC, soft reset, debug counters | Board-level integration |
| `iq_to_power.vhd` | Total — I²+Q², hardcoded 12-bit ADC assumption | IQ to scalar magnitude |
| `power_downsampler.vhd` | Moderate — 30.72→16 MHz fractional resampling | Rate adaptation for AD9363 sample rate |

## The Problem: Linear vs. Logarithmic Domain

The decode pipeline assumes **linear power** (I²+Q²). A log detector outputs
**log-amplitude**. This matters because the detection and decode chain uses
ratio-based thresholds throughout:

### Preamble Detector (`preamble_detector.vhd`)

All quality gates use shift-based ratio comparisons (`adsb_pkg.vhd:185-198`):

- **Absolute power gate**: each pulse window sum must exceed `POWER_THRESHOLD`
  (2000). On a log scale, this threshold is meaningless without knowing the
  reference level.
- **Quiet-zone ratio**: gap energy must be less than adjacent pulse energy
  (`quiet < pulse >> QUIET_ZONE_RATIO_SHIFT`). On a linear scale, a 10× SNR
  shows as 10×. On a log scale, it shows as a constant offset (~10 dB). The
  shift-based comparisons break.
- **Quiet-score gate**: summed per-zone pulse-minus-gap contrast must exceed
  `sum_pulse >> QUIET_SCORE_SHIFT`. Log compression flattens this ratio.
- **Aggregate SNR gate**: `sum_pulse > sum_gap << SNR_RATIO_SHIFT`. Same issue.
- **RPL estimation**: `sum_pulse >> 4` assumes linear scaling. On a log scale
  this is meaningless.

### BSD Classifier (`bsd_calculator.vhd`)

Sample classification uses RPL-relative windows (`adsb_pkg.vhd:215-228`):

- **typeA ("signal present")**: sample within [0.5×RPL, 2.5×RPL] — a 5× linear
  window. On log-amplitude this collapses to a few dB regardless of signal
  strength.
- **typeB ("quiet/noise")**: sample below 0.0625×RPL. On a log scale the
  absolute distance shrinks, misclassifying noise as signal.

Bit classification falls apart if samples are not on a linear power scale.

## Recommended Path: Linearise in the FPGA

Add a module between the ADC and `adsb_pl_wrapper` that applies an antilog
lookup table (or piecewise-linear approximation) to convert dB-scale ADC output
back to linear power. The entire existing decode pipeline then works unchanged —
same thresholds, same BSD classifier, same RPL.

The alternative (reworking all thresholds for log-domain operation) is a deeper
change affecting preamble_detector, bsd_calculator, and RPL semantics throughout.
Not worth it unless there is a specific reason to stay in log domain.

### LUT Details

- **Input**: 12-bit ADC code (maps linearly to dBm via AD8318 transfer function)
- **Output**: 24-bit signed linear power (matching `INPUT_POWER_WIDTH`)
- **Size**: 4096 entries × 24 bits = one 36Kb BRAM on the Zynq
- **Latency**: 1 clock cycle (vs 3 cycles for the current I²+Q² DSP path)
- **Resources**: 1 BRAM, 0 DSP48E (saves the 2 multipliers from `iq_to_power`)
- **Inversion**: The AD8318 has negative slope (-24 mV/dB: higher power = lower
  voltage). Handle this digitally by reversing the LUT index order — the table
  naturally maps low ADC codes to high linear power. Zero cost.

### Dynamic Range Consideration

The AD8318 has ~60 dB dynamic range. In linear, that is 1,000,000:1. The 24-bit
signed output can represent ~16M:1, so the full detector range fits comfortably.
The LUT output should be scaled so that typical signal levels land where the
existing thresholds expect them (POWER_THRESHOLD=2000, typical peak ~8000).
Weak signals 50+ dB below a strong one will quantise coarsely after
linearisation, but those would not decode anyway.

### Downsample Ratio

65 → 16 MHz = 4.0625:1. The existing phase-accumulator approach in
`power_downsampler` handles arbitrary ratios — only `INPUT_RATE_HZ` changes
from 30,720,000 to 65,000,000. ADS-B pulses are 500 ns wide (32 samples at
65 MSPS), so 16 MSPS still gives 8 samples per pulse — identical to today.

### Dual ADC Channels

The AD9238 has two channels. Both are wired to the FPGA. The decode pipeline
runs on one channel. The second channel is captured and made available to the PS
via AXI registers or a separate FIFO — no FPGA-side combining logic. The PS can
use the second channel for diagnostics, AGC feedback, or future dual-gain /
diversity experiments.

## Rework Summary

### Modules to Replace

| Module | Action | Notes |
|--------|--------|-------|
| `iq_to_power.vhd` | **Replace** | Not needed — log detector gives a scalar directly |
| `power_downsampler.vhd` | **Replace or adapt** | New ADC will not be 30.72 MSPS — need new rate ratio |
| `vendor_rx_ingress.vhd` | **Replace** | AD936x-specific plumbing |
| `adsb_vendor_wrapper.vhd` | **Replace** | AD936x-specific CDC, reset, debug counters |

### New Modules

| Module | Purpose | Estimated Size |
|--------|---------|----------------|
| `ad9238_ingress.vhd` | Parallel CMOS latch, 12-bit × 2 channels from the AD9238 | ~80-120 lines |
| `log_to_linear.vhd` | 4096-entry BRAM LUT: ADC dB → 24-bit linear power (includes inversion) | ~100-150 lines |
| `logdet_downsampler.vhd` | Phase accumulator, 65 → 16 MHz (reuse/adapt `power_downsampler`) | ~60-80 lines |
| `adsb_logdet_wrapper.vhd` | Board-level wrapper: ingress → LUT → downsample → `adsb_pl_wrapper`, plus channel B passthrough to PS | ~200-300 lines |

### Modules Unchanged

The entire decode pipeline stays untouched:

- `adsb_pl_wrapper.vhd` — the interface boundary
- `adsb_top.vhd` — top-level pipeline wiring
- `adsb_decoder.vhd` — decoder orchestration
- `adsb_edge_detector.vhd` — rising edge detection
- `preamble_detector.vhd` — preamble correlation and quality gate
- `message_decoder.vhd` — message sample gating
- `bsd_calculator.vhd` — soft-decision bit classification
- `smallest_bsds.vhd` — weak-bit identification
- `bit_flipper.vhd` — brute-force error correction
- `message_aggregator.vhd` — multi-decoder output mux
- `adsb_crc.vhd` — CRC validation
- `timestamp_counter.vhd` — PPS-disciplined MLAT timestamps
- `axi_regs.vhd` — AXI register bank
- `msg_fifo.vhd` / `async_msg_fifo.vhd` — message output path

### Modules That May Need Tweaking

| Module | What Might Change |
|--------|-------------------|
| `adsb_pkg.vhd` | `INPUT_POWER_WIDTH` may need adjustment depending on ADC resolution and linearisation output range. Thresholds should be fine if linearisation maps to a similar dynamic range. |

### Testbenches

Existing testbenches remain valid for the decode core. New test vectors will be
needed for the log-detector input path, and a new testbench for `log_to_linear`
to verify the LUT accuracy.

## Repo Structure: Same Repo, No Fork

The coupling is clean enough to handle with build-time selection:

```
hdl/rtl/
  adsb_pkg.vhd                -- shared
  adsb_top.vhd                -- shared, untouched
  adsb_pl_wrapper.vhd         -- shared, untouched (the interface)
  adsb_decoder.vhd            -- shared
  preamble_detector.vhd       -- shared
  bsd_calculator.vhd          -- shared
  ...all decode modules...    -- shared

  -- AD936x frontend (existing)
  vendor_rx_ingress.vhd
  iq_to_power.vhd
  power_downsampler.vhd
  adsb_vendor_wrapper.vhd

  -- Log-detector frontend (new)
  ad9238_ingress.vhd          -- parallel CMOS latch, 12-bit × 2 channels
  log_to_linear.vhd           -- 4096-entry BRAM LUT: ADC dB → linear power
  logdet_downsampler.vhd      -- phase accumulator, 65 → 16 MHz
  adsb_logdet_wrapper.vhd     -- board-level wrapper, channel B passthrough
```

At the Vivado level, two block designs (or one with a mux) — one instantiating
`adsb_vendor_wrapper`, the other instantiating `adsb_logdet_wrapper`. Both feed
the same `adsb_pl_wrapper`. The decode pipeline does not care which frontend
generated the samples.

## Resolved Design Decisions

1. **ADC**: AD9238-65, 12-bit, 65 MSPS dual channel. Downsample 65 → 16 MHz
   (4.0625:1) via phase accumulator.
2. **Inversion**: Digital. The LUT reverses the AD8318's negative slope at zero
   cost (table index order).
3. **Dual channels**: Both wired to FPGA. Channel A feeds the decode pipeline.
   Channel B passed through to PS for software use — no FPGA-side combining.
4. **Performance**: The LUT path is faster than the current I²+Q² path (1 cycle
   vs 3 cycles, 0 DSP48E vs 2). No performance regression.
5. **Clock**: AD9238 encode driven from FPGA fabric PLL. Single clock domain —
   no async FIFO CDC needed.

## Clock Architecture

**Decision: Drive the AD9238 encode clock from FPGA fabric (PLL off PS clock).**

This makes the ADC output synchronous to the fabric clock domain. The async
sample FIFO (used in the AD9363 path for rx_clk → S_AXI_ACLK CDC) is not
needed. Simpler design, one fewer class of timing bugs.

An external oscillator would only matter for low-jitter sampling in high-fidelity
SDR applications. For ADS-B envelope detection, the AD8318's ~10 ns response
time is already the limiting factor — ADC sampling jitter is irrelevant.

### ToA Timestamp Resolution

The ADC sample rate does **not** affect timestamp resolution. ToA is captured by
the 100 MHz `timestamp_counter` on `S_AXI_ACLK` when the preamble detector
fires — that is 10 ns resolution regardless of the ADC clock.

The ADC rate determines how precisely the preamble detector can locate the pulse
peak within samples. At 16 MSPS effective, each sample is 62.5 ns apart. The
65 MSPS raw rate does not help because the downsampler discards ~75% of samples
to reach 16 MSPS. The extra samples would only benefit ToA if the pipeline were
redesigned to use them (changing `SPS` and rippling through the entire detector).

### Potential Optimisation: Pre-Decimation Averaging

The current `power_downsampler` does nearest-sample selection. With 4.0625:1
decimation, a box-car or CIC filter averaging ~4 samples before selection would
yield ~6 dB better SNR on weak signals. This is a cheap improvement but not
required for initial bring-up — the existing nearest-sample approach works and
can be optimised later.

## Target Board: Smart ZYNQ SL (V1.3B, XC7Z020-CLG484)

The decoupled frontend will run on the Smart ZYNQ SL — a generic Zynq-7020
development board with no on-board RF hardware. The RF chain (TQP3M9036 → SAW →
AD8318 → AD8009 → AD9238) lives on a custom daughterboard connected via the
2.54mm GPIO headers.

Same Zynq-7020 silicon as the Fishball, but **CLG484 package** (not CLG400):
all pin constraints get rewritten regardless of package match. FPGA resource
budget (BRAM, DSP48E, LUTs) carries over unchanged from the Fishball design.

### Bank allocation

| Bank | Function | VCCIO |
|------|----------|-------|
| 13 | RGB LCD FPC (V1.3B only) — unused by plane-watcher | 3.3V |
| 33 | J6 GPIO header — daughterboard ADC bus | VADJ (default 3.3V, can be 1.8V) |
| 34 | On-board peripherals (UART, HDMI, LEDs, keys, EEPROM, 50 MHz clock) | 3.3V |
| 35 | J5 GPIO header AND ethernet PHY RGMII pins (shared) | 3.3V fixed |

J6/Bank 33 has 34 GPIO pins, completely free, with adjustable VCCIO.
J5/Bank 35 has 34 GPIO pins; ~16 are consumed by the RTL8211E ethernet,
leaving ~18 free.

### V1 daughterboard pin allocation

**J6 / Bank 33 (24-bit ADC data bus, both channels independent):**

| Signal | Count |
|---|---|
| AD9238 D[11:0] channel A | 12 |
| AD9238 D[11:0] channel B | 12 |
| AD9238 OTR A, OTR B | 2 |
| AD9238 DCO (output from ADC) | 1 |
| Spare (tie to GND for return path improvement) | 7 |

Total: 27 of 34 pins used. Multiplexed-output mode (combining both channels
onto one bus) was considered for V1 but deferred — the pin saving is real but
the only V1 use case for it is improving ground distribution on the header,
and we'd rather measure SI on a real board before committing to that
complexity. See "V2 considerations" below.

### V1 bring-up pin map (channel A only)

A 65 MSPS **AD9248** (14-bit) breakout — the generic "High_Speed_ADC_V1.0" —
is plugged *directly* onto the Smart ZYNQ SL J6 header, row-for-row aligned.
No jumper wires.

The decode pipeline is sized for 12 bits, so only chip `D[13:2]` (the top
twelve) are claimed by the design. Chip `D[1:0]` physically connect to FPGA
pins `T22`/`U22` but are not exposed as ports in the XDC — the ADC can drive
the pins harmlessly, nothing on the FPGA consumes them.

Channel B is not wired: the breakout's `BCL` and `BOT` pins hang past the
end of the J6 header (the breakout header is shorter than J6) and make no
electrical contact. Inside the RTL, `adsb_logdet_bringup` ties the
`ad9238_ingress` B-channel *inputs* to `'0'` for completeness.

| Breakout pin | FPGA pin | Signal |
|---|---|---|
| `ACL` | AB16 | `adc_encode` (FPGA → ADC CLK) |
| `AOT` | AA16 | `adc_otr_a` |
| `D0` | T22 | not in design (chip LSB, discarded) |
| `D1` | U22 | not in design (chip LSB, discarded) |
| `D2` | V22 | `adc_data_a[0]` |
| `D3` | W22 | `adc_data_a[1]` |
| `D4` | Y20 | `adc_data_a[2]` |
| `D5` | Y21 | `adc_data_a[3]` |
| `D6` | AA22 | `adc_data_a[4]` |
| `D7` | AB22 | `adc_data_a[5]` |
| `D8` | AA21 | `adc_data_a[6]` |
| `D9` | AB21 | `adc_data_a[7]` |
| `D10` | AB20 | `adc_data_a[8]` |
| `D11` | AB19 | `adc_data_a[9]` |
| `D12` | Y19 | `adc_data_a[10]` |
| `D13` | AA19 | `adc_data_a[11]` |
| `BCL`, `BOT` | — | no contact (overhangs the end of J6) |
| `+5V`, `GND` (breakout side) | — | breakout powered separately |

Constraints live in `hdl/vivado/constr/smartzynq_adc_io.xdc` and are included
in the Phase 1 build.

**J5 / Bank 35 (clock + GPS + ethernet coexistence):**

| Signal | Count |
|---|---|
| Ethernet RGMII (consumed by on-board PHY) | ~16 |
| AD9238 ENCODE (FPGA → ADC) | 1 |
| GPS PPS input | 1 |
| GPS UART TX/RX | 2 |
| Spare (debug, status indicators) | ~14 |

SmartZynq GPS pin assignment for the AD9238 bring-up build:

| Signal | FPGA pin | Header/bank | Direction |
|---|---|---|---|
| `gps_pps` | F16 | J5 / Bank 35 | GPS PPS → FPGA + PS EMIO GPIO[17] |
| `GPS_UART_txd` | E16 | J5 / Bank 35 | PS UART1 TX → GPS RX |
| `GPS_UART_rxd` | D16 | J5 / Bank 35 | GPS TX → PS UART1 RX |

ENCODE must source from a 3.3V bank: the AD9238's CMOS clock input requires
VIH ≥ 2.0V, and 1.8V LVCMOS VOH (~1.35V) does not meet it. This forces the
split — ENCODE on Bank 35 even if data is on Bank 33 at 1.8V.

### V1 VCCIO decision

**Start at 3.3V everywhere. Drop Bank 33 to 1.8V only if measured noise on
the AD8318 output during active sampling indicates digital coupling.**

Rationale: the noise reduction from 1.8V LVCMOS is real (~70% lower switching
power, slower edges), but 3.3V matches vendor default, gives wider noise margin
for the daughterboard interconnect, and avoids the need to swap RA/RB resistors
on the board back. If the daughterboard layout has good ground separation
between the digital and analogue sections, 3.3V will be fine. If the AD8318
output shows degraded noise floor when the FPGA is actively sampling vs idle,
that is concrete evidence for dropping to 1.8V.

### V2 considerations

These are deliberate non-goals for V1 but worth recording for later:

1. **AD9238 multiplexed output mode** — both channels share Channel A's data
   bus, alternating on each ENCODE edge. Reduces the data bus from 24 to 12
   data lines, freeing ~12 J6 pins for use as ground returns. Requires DDR
   capture in the FPGA (130 MHz effective bus rate at 65 MSPS encode) and a
   small demux state machine. Worth revisiting if V1 SI measurements show
   crosstalk or coupled noise on the data bus.

2. **Pre-decimation averaging in the downsampler** — replace nearest-sample
   selection with a box-car or CIC filter averaging ~4 samples before
   decimating to 16 MSPS. Yields ~6 dB SNR improvement on weak signals at
   negligible resource cost. Independent of V1 vs V2 hardware — purely an
   RTL change.

3. **Dual-channel diversity or dual-gain** — currently both ADC channels are
   captured but only channel A drives the decode pipeline. Channel B is
   passed through to PS for diagnostics. Future work could combine the
   channels in PS for diversity reception or extended dynamic range. No FPGA
   changes required for this evolution path.

### Port scope beyond the RF frontend

Moving from Fishball to Smart ZYNQ SL is broader than the REWRITE rework
above. The decode pipeline carries over unchanged (per the `adsb_pl_wrapper`
boundary), but everything around it is fresh:

- **Pin constraints**: complete rewrite for CLG484 package
- **Vivado block design**: PS config (GEM0 on EMIO to RTL8211E, UART on
  Bank 34), MMCM (50 MHz → 100 MHz S_AXI_ACLK + 65 MHz ADC encode), AXI
  register block. No vendor BD to inherit from.
- **Device tree**: full DTS from scratch via Petalinux from the hardware XSA
- **Linux**: full Petalinux build (the hellofpga.com tutorial set §8.5
  covers exactly this board)
- **Network**: `pluto.local` → fresh hostname, fresh SSH keys
- **Deploy scripts**: hostname updates throughout
- **`ps/internal/radio/`**: delete entirely — no controllable RF gain in this
  chain. The `plane-feeder` radio tuning section and AD9363 saturation-counter
  gain guard also go away.
- **`firmware/` submodule**: replaced (Pluto kernel fork is unusable for this
  board)

## Open Questions

1. **AD8318 transfer function** — needed to generate the antilog LUT entries.
   The datasheet's dB/V curve maps directly to table values. Must be
   characterised with the actual AD8009 gain/offset to know the ADC code ↔ dBm
   mapping.
2. **LUT output scaling** — the antilog table output range must be tuned so that
   typical received signal levels land where the existing thresholds expect them
   (`POWER_THRESHOLD=2000`, peak ~8000). This depends on the AD8009 gain/offset
   and is best determined from measured captures once hardware is available.
3. **AD9238 OTR pin count in multiplexed mode** — datasheet check needed: is
   OTR multiplexed onto a single pin in mux mode, or do both channels keep
   independent OTR outputs? Affects V2 pin budget by 1.
4. **Ethernet PHY reset path** — schematic shows `ETH_RST` net but it's not
   clear from the snippet whether it's driven from an FPGA EMIO GPIO or just
   pulled high to DVDD33. Verify before finalising the device tree.
