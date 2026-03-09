# Architecture Summary

This repository implements an FPGA-first ADS-B / Mode-S receiver pipeline with
Linux userspace responsible for control, framing, and network output.

**Status as of 2026-03-27:** Hardware (Fishball (PlutoSDR-compatible) Rev.C) is live and decoding ADS-B frames end-to-end.

Primary rules:

> all packet timestamps originate in FPGA hardware (100 MHz S_AXI_ACLK counter)
> signal power is computed from the current squared-power ingress path ((I²+Q²) >> 2)

## Current Implemented Split

### PL / FPGA

19 RTL modules under `hdl/rtl/` implement:

- AD9363 I/Q ingress, squared-power calculation ((I²+Q²) >> 2), fractional resampling (30.72M → 16M)
- Edge detection and preamble pattern matching
- 8 parallel message decoders with soft-decision Berlekamp–Massey error correction
- Message aggregation and FIFO buffering
- 64-bit timestamp counter at 100 MHz with PPS capture
- AXI4-Lite register block (messages, debug counters, control)

**Clock domains:**

| Domain | Freq | Purpose |
|--------|------|---------|
| `rx_clk` (l_clk) | ~61.44 MHz | ADC interface, I/Q processing, rate conversion |
| `S_AXI_ACLK` (FCLK_CLK0) | 100 MHz | Decode core, timestamp counter, AXI interface |
| `delay_clk` (FCLK_CLK1) | 200 MHz | IDELAYCTRL (LVDS DDR interface) |

The current integrated top level is `hdl/rtl/adsb_vendor_wrapper.vhd`.

### PS / Linux Userspace

Implemented Go code under `ps/` provides:

- register access via `/dev/mem`
- raw AXI message decode into standard Mode-S byte order
- radio gain and gain-mode control through IIO
- aircraft state tracking for the local dashboard
- Beast frame encoding
- TCP Beast fan-out server
- embedded HTTP dashboard + JSON API
- replay tooling and tuning/bring-up helpers

The main runtime binary is `ps/cmd/plane-feeder`.

## Current Data Path

```
AD9363 (Fishball (PlutoSDR-compatible)) LVDS RX
  ↓
axi_ad9361 IP (l_clk ~61.44 MHz)
  ↓
vendor_rx_ingress
  ↓
iq_to_power ((I²+Q²) >> 2)
  ↓
power_downsampler (30.72M → 16M effective via fractional resampler)
  ↓
async_sample_fifo → S_AXI_ACLK (100 MHz)
  ↓
adsb_edge_detector → preamble_detector → 8× message_decoder
  ↓
message_aggregator → async_msg_fifo
  ↓
axi_regs (0x43D00000)
  ↓
plane-feeder (reads /dev/mem)
  ↓
Beast TCP (port 30005)
  ↓
Clients (dump1090, Radarbox, etc.)
```

## Frozen PS/PL Contract

The current source of truth is:

- `docs/plans/2026-03-15-ps-pl-contract.md`

That document defines:

- register offsets
- FIFO pop semantics
- current byte-order behavior
- timestamp meaning
- Beast wire framing assumptions

Key interface details today:

- `RPL` read pops one FIFO entry
- PS must read message words and TOA before `RPL`
- FPGA still exposes the historical swizzled message byte order
- PS reverses bytes back into standard Mode-S order
- `RPL` comes from the current squared-power ingress path (`(I^2 + Q^2) >> 2` into the detector), not the old `|I| + |Q|` approximation

## Timestamp Architecture

Implemented behavior:

- **Timestamp counter**: 64-bit free-running at 100 MHz (S_AXI_ACLK), latched with every message decode
- **PPS capture**: Rising edge latches counter value and increments PPS count (synchronized to S_AXI_ACLK)
- **Beast timestamps**: PS converts the 100 MHz counter to the conventional 12 MHz Beast format via `floor(toa * 12 / 100)` (`3 / 25`)
- **Radarcape mode**: experimental; uses hardware PPS + software wall-clock correlation (not true GNSS-disciplined yet)

Known limitations:

- Radarcape UTC seconds are coarse and software-correlated, not latched at PPS edge
- Effective sample rate is 16M sps (via CDC from 30.72M fractional resampler), but timestamp clock is 100 MHz

## Verification Surface

### RTL

Simulation assets exist for:

- CRC
- timestamp counter
- decoder behavior
- AXI register access
- detector regression checks
- weak-signal vectors

Entry point:

- `hdl/sim/Makefile`

### PS

Go tests exist for:

- register decoding
- Beast encoding
- replay parsing
- golden end-to-end frame generation
- mock-reader integration through TCP output
- web API and dashboard smoke coverage

## Development Platform Assumption

The active development path is:

- Zynq-7020 first for bring-up and headroom
- evaluate 7010 only later as a cost-down target

This supersedes older 7010-first wording that may appear in historical notes.

## What Is Implemented vs. Pending

**✅ Implemented:**
- End-to-end hardware: AD9363 → FPGA decode pipeline → Beast TCP output
- Preamble detection with timing-optimized quiet-zone gate
- 8-parallel soft-decision decoders with Berlekamp–Massey error correction
- AXI register interface with debug counters
- Go PS software (plane-feeder, regdump, fifo-monitor, collect-stats, tune-detector, etc.)
- Simulation coverage (5 testbenches, 35 test vectors)

**🔄 In Progress / Known Issues:**
- Weak-signal detector tuning (weak3–weak5 test vectors)
- AD9361 warm-reboot recovery (RESETB pin issue)
- Long-term stability validation
- Real-world signal coverage in different RF environments

**⏳ Future Work:**
- Final GNSS/UTC integration (beyond coarse PPS correlation)
- Cost-down evaluation of Zynq-7010
- Optional: AXI interface v2 byte-order cleanup

## Related Documents

- `README.md`: repo-level summary
- `docs/wiki/README.md`: newcomer-oriented technical walkthrough
- `docs/ROADMAP.md`: phased development plan
- `docs/plans/2026-03-15-ps-pl-contract.md`: frozen register interface
- `docs/plans/2026-03-28-plane-feeder-central-daemon.md`: current PS daemon/dashboard design notes
