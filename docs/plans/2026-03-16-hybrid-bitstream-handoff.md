# Hybrid Bitstream Handoff

Purpose: record exactly how the current Zynq-7020 hybrid vendor-RX bitstream
was reached, what design assumptions it bakes in, and what the next board-side
steps should be.

This document is intentionally explicit. It exists so future work does not
silently undo board-specific decisions that were made during first integration.

## 1. Current Outcome

The repository now produces a real bitstream for the current board revision:

- top-level design: `zynq_adsb_vendor_wrapper`
- device: `xc7z020clg400-2`
- Vivado: `2025.2`
- implementation status: place, route, and `write_bitstream` all pass

Current generated design intent:

- Zynq PS configured with vendor-derived settings
- vendor `axi_ad9361` RX front-end instantiated
- vendor `rx_fir_decimator` instantiated
- project `adsb_vendor_wrapper` instantiated
- vendor RX I/Q path bridged into project decoder ingress
- AXI4-Lite register block exposed to PS through `M_AXI_GP0`

Current AXI windows:

- `axi_ad9361`: `0x43C0_0000`
- `adsb_vendor_wrapper`: `0x43D0_0000`

## 2. What Was Added To Reach This Point

### Vivado project scaffolding

Added or updated under `hdl/vivado/`:

- `srcs.tcl`
- `create_project.tcl`
- `create_zynq_bd.tcl`
- `create_vendor_hybrid_bd.tcl`
- `prepare_bd_top.tcl`
- `run_synth.tcl`
- `run_impl.tcl`
- `Makefile`
- `constr/plane_watcher_integration.xdc`
- `README.md`

These now support:

- plain RTL project creation
- minimal Zynq BD creation
- hybrid vendor-RX BD creation
- BD wrapper generation
- synthesis from the generated BD wrapper
- implementation and bitstream generation from the generated BD wrapper

### RTL integration work

Added or updated under `hdl/rtl/`:

- `async_msg_fifo.vhd`
- `iq_to_power.vhd`
- `power_downsampler.vhd`
- `vendor_rx_ingress.vhd`
- `adsb_vendor_wrapper.vhd`
- `adsb_top.vhd`

These changes did two important things:

1. removed the unsafe single-clock FIFO assumption across the sample and AXI
   domains
2. created a board-specific ingress path from vendor AD936x I/Q into the
   existing scalar-power decoder contract

## 3. Vendor RX Path Decision

The vendor Pluto-derived design naturally exposes the receive path as:

- `axi_ad9361/l_clk` at about `61.44 MHz`
- `rx_fir_decimator/data_out_0[15:0]` for I
- `rx_fir_decimator/data_out_1[15:0]` for Q
- `rx_fir_decimator/valid_out_0` for sample-valid

We deliberately did **not** rewrite the decoder core to run directly on the
raw vendor stream.

Instead the current repo uses:

```text
vendor RX I/Q @ 61.44 MHz
  -> iq_to_power
  -> /4 rate adapter
  -> existing decoder-facing scalar power stream
```

Reason:

- keeps decoder contract stable for first hardware bring-up
- isolates board-specific DSP/rate adaptation from the existing core
- avoids mixing radio, clock, and detector retuning changes all at once

Current first-pass ingress target:

- raw vendor RX clock: `61.44 MHz`
- decoder-facing bring-up rate: `15.36 MHz`

## 4. ENSM Control Decision

This was one of the main places the design could have gone wrong.

### What was verified

Board schematic and vendor constraints show real physical AD936x control pins:

- `ENABLE`
- `TXNRX`

Vendor XDC constrains them as:

- `enable`
- `txnrx`

Vendor software evidence from `test_ensm_pinctrl.sh` shows ENSM pin control is
driven from Zynq GPIO:

- `GPIO_ENABLE = zynq_gpio + 69`
- `GPIO_TXNRX = zynq_gpio + 70`

ADI binding text states:

- `ENABLE/TXNRX` control ENSM state
- default control path is SPI writes

### Resulting interpretation

- `enable` and `txnrx` are real board-facing physical pins
- `up_enable` and `up_txnrx` are internal control-side inputs on
  `axi_ad9361`
- exposing `up_enable` / `up_txnrx` as top-level package pins would be the
  wrong model for this board

### Current board-specific implementation

In the hybrid BD:

- keep `enable` and `txnrx` as external constrained pins
- hard-drive the AD936x bootstrap state in PL:
  - `axi_ad9361/up_enable = 1`
  - `axi_ad9361/up_txnrx = 0`
  - `gpio_resetb = 1`
  - `gpio_en_agc = 0`
- keep PS SPI0 EMIO wired to the physical AD936x SPI pins

This is intentional for bring-up. It avoids a dependency on Linux/userspace
sideband GPIO setup and keeps the radio in a fixed RX-capable bootstrap state.

Do not later "fix" this by re-exposing `up_enable` or `up_txnrx` as raw
unconstrained top-level pins unless the electrical path is identified and
documented first.

## 5. PPS Decision

The current board revision does not have a real PPS source wired into the FPGA.

The original project architecture includes PPS capture, but for this board
revision:

- `pps_in` is **not** a real board pin today
- no vendor XDC exists for it
- the current hardware does not have onboard GPS/PPS

Current implementation choice:

- keep PPS logic in the generic project architecture
- tie `pps_in` inactive in the current hybrid BD

Implication for bring-up:

- `PPS_COUNT` staying at `0` is expected on the current board image
- this is not a failure until a real PPS route is added

If PPS is later routed through JP5 or another header, that must be documented
as a new board-level integration step with:

- chosen pin
- voltage standard
- LOC constraint
- synchronization expectation

## 6. Real Board Pin Constraints Imported

The hybrid bitstream only became bitstream-capable after importing the real
vendor board constraints for:

- AD936x RX LVDS clock/frame/data
- AD936x TX LVDS clock/frame/data
- physical `enable`
- physical `txnrx`

Those constraints now live in:

- `hdl/vivado/constr/plane_watcher_integration.xdc`

They are intentionally board-specific.

## 7. Build Commands That Matter

### Build the hybrid BD only

```bash
cd hdl/vivado
make vendor-bd VENDOR_HDL_ROOT=/path/to/Fish-Wan-plutosdr-fw-7020-SDR
```

### Build the hybrid BD wrapper and run synthesis

```bash
cd hdl/vivado
make vendor-synth VENDOR_HDL_ROOT=/path/to/Fish-Wan-plutosdr-fw-7020-SDR
```

### Build implementation and bitstream

```bash
cd hdl/vivado
make vendor-impl VENDOR_HDL_ROOT=/path/to/Fish-Wan-plutosdr-fw-7020-SDR
```

Expected implementation top:

- `zynq_adsb_vendor_wrapper`

## 8. Where The Bitstream Comes From

The generated bitstream is produced by the Vivado implementation run for the
hybrid BD wrapper. In the standard build tree it will be under something like:

- `hdl/vivado/build/plane_watcher/plane_watcher.runs/impl_1/`

Look for:

- `zynq_adsb_vendor_wrapper.bit`
- routed DCP
- utilization/timing reports

## 9. What To Do Next On Hardware

The next practical sequence is:

1. load the hybrid bitstream on the board
2. boot the Linux image you intend to use for userspace bring-up
3. verify AXI register access at `0x43D00000`
4. confirm `VERSION`, `STATUS`, and FIFO access
5. accept that PPS is currently inactive
6. start `plane-feeder` and validate Beast framing
7. only after that, investigate actual RF decode behavior

Use:

- `docs/plans/2026-03-15-hardware-bringup-runbook.md`

with the additional current-board assumptions:

- `adsb_vendor_wrapper` base: `0x43D00000`
- PPS expected inactive
- hybrid bitstream top is `zynq_adsb_vendor_wrapper`

## 10. What Is Still Missing

This is not the final board design.

Still missing or intentionally deferred:

- PS/EMIO recreation of the vendor ENSM GPIO control path
- explicit SPI/I2C/userland transceiver management recreation in our BD
- real PPS/GNSS hardware route
- validation of live ADS-B performance on the board
- tuning based on actual radio levels and rate assumptions

## 11. Rules For Future Changes

If a future change touches any of these, update this document and the Vivado
README in the same commit:

- ENSM control path
- PPS routing
- vendor pin constraints
- AXI base addresses
- chosen hybrid top design name
- ingress clock/rate assumptions

That is the minimum needed to avoid repeating the same integration archaeology.
