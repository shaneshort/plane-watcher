# Development Roadmap

**Last updated: 2026-03-27** — Hardware is live, frames are decoding end-to-end.

This roadmap distinguishes between work that is already implemented in-tree
and work that still needs hardware validation or further design.

## Working Assumption

- use a Zynq-7020 class development board first
- prove the PL/PS register contract, timestamp path, and Beast output
- validate RF sampling and timing on real hardware
- only then decide whether a 7010 cost-down target is justified

## Phase Status

## Phase 1: PL/PS Interface And Bring-Up Scaffolding

Status: largely implemented in-repo, not yet proven on target hardware

Implemented:

- AXI register block in RTL
- PS register access via `/dev/mem`
- helper tools: `regdump`, `fifo-monitor`, `pps-check`, `beast-client`
- bring-up runbook
- documented PS/PL contract

Remaining:

- boot the target board with the current image and bitstream
- verify the base address and register reads on real hardware
- confirm FIFO pop semantics and decoder enable/reset behavior in-system

## Phase 2: FPGA Timing Core

Status: implemented and simulated

Implemented:

- 64-bit free-running counter
- PPS capture registers and count
- timestamp counter testbench

Remaining:

- validate PPS cadence and counter stability on hardware
- confirm the 16 MHz assumption against the real clocking setup

## Phase 3: ADS-B Decode Pipeline

Status: implemented with simulation coverage

Implemented:

- preamble detection
- decode pipeline
- message aggregation
- FIFO buffering
- AXI register exposure
- detector regression checks

Remaining:

- tune weak-signal performance beyond the current passing set
- validate behavior under real RF conditions

## Phase 4: Linux Receiver Stack

Status: implemented and tested

Implemented:

- `plane-feeder` runtime
- Beast encoding and TCP fan-out
- replay tooling
- golden tests and integration tests

Remaining:

- run the stack continuously on target hardware
- compare live FPGA output against a software reference

## Phase 5: Timing Quality And UTC Mapping

Status: partial

Implemented:

- standard Beast timestamp output
- provisional Radarcape mode using PPS plus coarse wall-clock correlation

Remaining:

- define and implement the final GNSS-backed UTC path
- validate rollover and PPS-boundary behavior on hardware
- decide what quality/health state needs surfacing to clients

## Phase 6: RF And Sample Source Validation

Status: ✅ in progress — hardware live, frames decoding

Completed:

- Fishball (PlutoSDR-compatible) Rev.C (Zynq-7020) boots and integrates with hybrid bitstream
- AD9363 SPI probe succeeds, RX samples flow through axi_ad9361 + FIR + fractional resampler
- FPGA decode pipeline validates preamble and decodes ADS-B messages end-to-end
- Beast TCP output confirmed working

Remaining:

- validate decode coverage against live airspace traffic
- stress-test stability (weak signals, high message rates, sustained operation)
- measure detector tuning needs (weak3–weak5 test vectors currently not passing)

## Phase 7: Cost-Down Evaluation

Status: deferred intentionally

Remaining:

- measure real 7020 utilization and operational headroom
- decide whether 7010 is viable without cutting required functionality

## Immediate Next Steps

1. **Weak-signal tuning** — test vectors weak3–weak5 are not passing. Profile against real capture data to understand whether:
   - detection thresholds need relaxation for real RF conditions, or
   - false-positive suppression is too aggressive

2. **AD9361 warm-reboot fix** — RESETB is hard-tied high, causing SPI probe failures on reboot. Add deterministic RESETB pulse in bootloader or early FPGA load.

3. **Real-world validation** — sustained testing with live traffic to measure:
   - message decode rate and accuracy
   - weak-signal coverage in target deployment location
   - stability (no FIFO overflows, no crashes)

4. **Radarcape UTC refinement** — current implementation is coarse; consider:
   - hardware-latched UTC seconds at PPS edge (v2 register interface), or
   - accept software correlation as sufficient for current use case

5. **Cost-down evaluation** — measure Zynq-7020 resource utilization and decide whether 7010 is viable.
