# Vivado Integration

This directory contains the active Vivado integration flow for the current
Zynq-7020 board target.

## What This Is

- a reusable source list for the RTL
- a Vivado batch script that creates a project
- a synthesis batch script
- a top-level wrapper suitable for module import / IP Integrator use
- a constraints template for clocks and future pin assignment

## Current State

- produces working hardware bitstreams for the current board
- contains both staged Makefile targets and single-session build TCL flows
- keeps the PS/PL AXI address assignment at `0x43D00000`
- still depends on a vendor HDL checkout for the hybrid AD936x design

## Entry Points

Repo-level wrappers for the common flow live under `tools/`:

```bash
cp tools/plane_watcher.env.example tools/plane_watcher.env
$EDITOR tools/plane_watcher.env
./tools/build-bitstream.sh --skip-deploy
./tools/deploy-bitstream.sh
```

To package the `.bit.bin` locally without pushing it to a board:

```bash
./tools/deploy-bitstream.sh --generate-only
```

The lower-level `make` targets in this directory are still the source of truth
for the Vivado flow and are useful when iterating on timing or debugging a
specific stage.

Generated artifacts from local Vivado runs are intentionally ignored in git.
The tracked files here are the TCL, constraints, Makefile, and checked-in
source wrappers, not `build/`, `.Xil/`, or local log/journal output.

Create a Vivado project:

```bash
cd hdl/vivado
make project
```

Create the first Zynq block design:

```bash
cd hdl/vivado
make bd
```

Create the first hybrid vendor-RX block design:

```bash
cd hdl/vivado
make vendor-bd VENDOR_HDL_ROOT=/path/to/Fish-Wan-plutosdr-fw-7020-SDR
```

Prepare the basic BD as the project top and run synthesis:

```bash
cd hdl/vivado
make synth
```

Prepare the hybrid vendor-RX BD as the project top and run synthesis:

```bash
cd hdl/vivado
make vendor-synth VENDOR_HDL_ROOT=/path/to/Fish-Wan-plutosdr-fw-7020-SDR
```

Generate the hybrid vendor-RX implementation and bitstream:

```bash
cd hdl/vivado
make vendor-impl VENDOR_HDL_ROOT=/path/to/Fish-Wan-plutosdr-fw-7020-SDR
```

The `*-synth` and `*-impl` targets depend on the corresponding BD creation and
wrapper-generation steps, so the project top is set to the generated BD wrapper
instead of the raw RTL module.

Override the part if needed:

```bash
make project PART=xc7z020clg400-2
```

## Top Module

The current board-facing top is:

- `hdl/rtl/adsb_vendor_wrapper.vhd`

It exposes:

- RX clock / reset
- RX I/Q stream plus valid
- PPS input
- AXI4-Lite slave interface
- interrupt output

## Integration Scope

The first block design script is:

- `hdl/vivado/create_zynq_bd.tcl`
- `hdl/vivado/create_vendor_hybrid_bd.tcl`

It currently does these things:

- configures `processing_system7` using the vendor-derived PS settings
- instantiates `adsb_vendor_wrapper` as a module reference
- connects PS `M_AXI_GP0` to the wrapper's AXI4-Lite slave
- routes the wrapper IRQ into `IRQ_F2P`
- requests AXI base address `0x43D00000`
- leaves RX-domain and PPS signals as external ports

What still remains after that:

- connect the real vendor RX source path
- connect the real PPS path
- generate a board-level wrapper/bitstream once those interfaces are settled

The hybrid vendor-RX script is intentionally partial. It recreates the PS,
AD936x RX-side datapath, and our decoder wrapper in one BD, but it does not
attempt to mirror every part of the vendor control-plane design. It is not
the active integration path; see the stock-tap flow below.

## Active Build Flow: stock-tap

The stock-tap flow (`build_stock_tap.tcl`) is the active integration path.
It sources the vendor Pluto BD (`system_bd.tcl`), then taps in the ADS-B
decoder wrapper, PPS input, and GPS UART0 EMIO on top.

Board-pin constraints for the stock-tap additions live in:

- [constr/plane_watcher_stock_tap_io.xdc](/home/shanes/plane_watcher/hdl/vivado/constr/plane_watcher_stock_tap_io.xdc)
  (PPS and UART0 pin assignments on JP5, Bank 13)

The AD936x interface constraints come from the vendor `system_constr.xdc`
(copied and patched at build time with a relaxed 8 ns rx_clk period).

## Constraint Layering Note

The `stock-tap-build` flow has two constraint layers:

- board-pin assignments (PPS, UART0) in the stock-tap IO XDC above
- CDC false-path exceptions and debug counter crossings in
  [constr/plane_watcher_post_impl_overrides.tcl](/home/shanes/plane_watcher/hdl/vivado/constr/plane_watcher_post_impl_overrides.tcl)

`build_stock_tap.tcl` sources the post-impl override file during the
implementation run and again before generating the signoff timing report. In
practice, that means new rx-domain debug counters that are snapshotted into
`S_AXI_ACLK` must be added to the post-impl override file as well as the XDC.
Updating only the XDC can leave the same `rx_clk -> clk_fpga_0` path visible
in `impl_timing_summary_post_override.rpt`, with unchanged WNS/TNS after a
rebuild.

## Hybrid Control Notes

These details are easy to get wrong and should be treated as board-specific
facts for the current 7020 AD936x hardware revision:

- `enable` and `txnrx` are the real physical AD936x ENSM control pins.
- Those two pins are constrained from the vendor `system_constr.xdc` and are
  also visible on the board schematic (`ENABLE` and `TXNRX`).
- `up_enable` and `up_txnrx` are *not* treated as board pins in this repo's
  hybrid design. They are control-side inputs on `axi_ad9361`.
- Vendor software evidence (`test_ensm_pinctrl.sh`) shows ENSM pin control is
  driven from Zynq GPIO, not from extra package pins:
  - `GPIO_ENABLE = zynq_gpio + 69`
  - `GPIO_TXNRX = zynq_gpio + 70`
- The ADI device-tree binding also states that `ENABLE/TXNRX` control ENSM
  state, with the default control path being SPI writes.

Current repo policy for the stock-tap BD:

- keep external board pins for `enable` and `txnrx`
- do not expose `up_enable` or `up_txnrx` as external top-level pins
- the stock Pluto BD manages the AD936x control-side path via PS GPIO
- `pps_in` is a real board pin on JP5 pin 7 (V10), connected to both the
  ADS-B decoder wrapper and EMIO GPIO[17] (Linux GPIO 71)
- UART0 is enabled via EMIO for the GPS F9P on JP5 pins 9/11 (U9/U10)
- keep PS SPI0 EMIO wired to the real AD936x SPI pins so Linux can probe the
  transceiver without any userland GPIO choreography

The hybrid vendor-RX BD (`build_vendor.tcl`, `create_vendor_hybrid_bd.tcl`)
is retained but is not the active integration path. It hard-drives the radio
bootstrap state instead of using the stock PS GPIO sideband. Do not add new
board-pin features to the hybrid flow without explicitly upgrading it.
