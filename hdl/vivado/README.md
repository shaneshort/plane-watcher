# Vivado Integration

This directory contains the active Vivado integration flow for the current
Smart ZYNQ SL / Zynq-7020 board target.

## What This Is

- a reusable source list for the RTL
- a Vivado batch script that creates a project
- a synthesis batch script
- a top-level wrapper suitable for module import / IP Integrator use
- board constraints for the active Smart ZYNQ phase-1 firmware

## Current State

- produces the Smart ZYNQ phase-1 bitstream and XSA consumed by Petalinux
- keeps the PS/PL AXI address assignment at `0x43C00000`
- does not depend on the old Pluto/vendor HDL flow for the active firmware

## Entry Points

Repo-level wrappers for the active firmware flow live under `tools/`:

```bash
cp tools/plane_watcher.env.example tools/plane_watcher.env
$EDITOR tools/plane_watcher.env
./tools/rebuild.sh --no-deploy
```

To rebuild only the Vivado bitstream/XSA and repackage `BOOT.BIN`:

```bash
./tools/rebuild.sh --bitstream --no-deploy
```

The lower-level phase-1 TCL is the source of truth for the active Vivado flow:

```bash
./tools/build-smartzynq-phase1.sh
```

The older `make` targets and vendor/stock-tap TCL files are retained as legacy
bring-up references, not as the active firmware path.

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
attempt to mirror every part of the vendor control-plane design. It is retained
as a historical reference, not as the active integration path.

## Active Build Flow: Smart ZYNQ Phase 1

The active firmware path is `tools/rebuild.sh`, which runs
`tools/build-smartzynq-phase1.sh` and then packages the generated XSA through
Petalinux.

The only hand-authored XDC files used by this path are:

- [constr/smartzynq_phase1_io.xdc](/home/shanes/plane_watcher/hdl/vivado/constr/smartzynq_phase1_io.xdc)
  for UART, GPS PPS, and Ethernet pin/timing constraints
- [constr/smartzynq_adc_io.xdc](/home/shanes/plane_watcher/hdl/vivado/constr/smartzynq_adc_io.xdc)
  for the AD9203 prototype log-detector frontend pins

Generated IP and block-design XDC files under `build/` are owned by Vivado and
are not tracked as source constraints.

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

Historical repo policy for the stock-tap BD:

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
