# First Custom SD Image Plan

Purpose: prepare a low-risk Linux image for the first board bring-up without
doing a full custom Zynq platform port from scratch.

This plan assumes the vendor board-support source is available at:

- `/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR`

and the vendor board documents are available at:

- `/Users/shanes/Downloads/sdr/New_7020_AD936X_SDR_Documents`

## 1. Strategy

Do not start with a fresh distro or a clean-sheet Vivado platform.

For first hardware bring-up, the safest path is:

1. keep the stock vendor SD image as recovery
2. prepare a second SD card using the vendor Pluto-derived firmware tree
3. boot a known-good vendor-style Linux image first
4. add only what this project needs:
   - our userspace binaries
   - our FPGA bitstream when ready
   - our AXI base address once verified

Reason:

- the vendor tree already encodes the board's DDR settings
- the vendor tree already encodes PS MIO assignments for UART, Ethernet, USB,
  SD, and QSPI
- the vendor tree already includes a working boot chain for this exact board
- this reduces risk to "our additions are wrong" instead of "the entire board
  port is wrong"

## 2. What To Reuse

Reuse these components from the vendor firmware tree for the first image:

- FSBL generation flow
- U-Boot configuration
- Linux kernel and device-tree build flow
- Buildroot root filesystem
- SD-card packaging flow

Relevant files:

- [Makefile](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/Makefile)
- [scripts/pluto.mk](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/scripts/pluto.mk)
- [buildroot/configs/zynq_pluto_defconfig](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/buildroot/configs/zynq_pluto_defconfig)
- [linux/arch/arm/boot/dts/zynq-pluto-sdr.dtsi](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/linux/arch/arm/boot/dts/zynq-pluto-sdr.dtsi)
- [hdl/projects/pluto/system_bd.tcl](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/hdl/projects/pluto/system_bd.tcl)
- [hdl/projects/pluto/system_constr.xdc](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/hdl/projects/pluto/system_constr.xdc)

The vendor `sdimg` flow already produces:

- `BOOT.bin`
- `uImage`
- `devicetree.dtb`
- `uEnv.txt`
- `uramdisk.image.gz`

That is enough for first boot and `/dev/mem`-based register access.

## 3. What To Avoid Reusing As-Is

Treat these Pluto-specific pieces as temporary baggage, not the final platform
shape:

- board identity and naming
- Pluto-branded hostnames and login defaults
- update/DFU/mass-storage paths that only exist for Pluto compatibility
- hardcoded assumptions about vendor HDL blocks

Examples:

- [buildroot/configs/zynq_pluto_defconfig](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/buildroot/configs/zynq_pluto_defconfig)
  still sets hostname `pluto` and root password `analog`
- [linux/arch/arm/boot/dts/zynq-pluto-sdr.dtsi](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/linux/arch/arm/boot/dts/zynq-pluto-sdr.dtsi)
  still identifies as a Pluto board

For bring-up that is acceptable. For the final image it is not.

## 4. Schematic Impact

The schematic matters anywhere the design touches physical board wiring.

### Fixed by the board

These are not negotiable in our design:

- PS MIO assignments for UART, Ethernet, USB, SD, and QSPI
- DDR wiring and timing assumptions
- AD936x LVDS/sample/control pins
- any onboard LEDs, buttons, reset lines, PHY reset lines, and clock pins

Evidence in vendor sources:

- [hdl/projects/pluto/system_bd.tcl](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/hdl/projects/pluto/system_bd.tcl)
  configures PS peripherals and MIO usage
- [hdl/projects/pluto/system_constr.xdc](/Users/shanes/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR/hdl/projects/pluto/system_constr.xdc)
  fixes AD936x-facing PL pins and control signals

### Still ours to choose

These remain project decisions:

- the internal RTL pipeline
- AXI register layout
- AXI address map for our custom block
- whether PPS enters through EMIO, PL GPIO, or another board-exposed path
- whether extra debug GPIOs or LEDs are worth exposing in the first design

Important current warning:

- the vendor DTS already places `mwipcore` at `0x43c00000`
- our project now uses `0x43D00000` as a provisional default to avoid that
  occupied region
- do not treat that address as final until the actual Vivado design is built

See:

- [TODO.md](/Volumes/External Storage/Documents/development/plane_watcher/TODO.md)
- [docs/plans/2026-03-15-ps-pl-contract.md](/Volumes/External%20Storage/Documents/development/plane_watcher/docs/plans/2026-03-15-ps-pl-contract.md)

## 5. First Image Goal

The first custom SD image does not need to be elegant. It only needs to boot
reliably and let us validate the PS/PL boundary.

Success criteria:

- serial console works
- Ethernet works
- root shell works
- `/dev/mem` works
- our helper binaries run
- bitstream loading path is known and repeatable
- AXI registers are reachable at the verified base address

That is enough to execute:

- [docs/plans/2026-03-15-hardware-bringup-runbook.md](/Volumes/External%20Storage/Documents/development/plane_watcher/docs/plans/2026-03-15-hardware-bringup-runbook.md)

## 6. Phase 1: Ready A Vendor-Derived SD Card

Use the vendor source tree to produce or reconstruct an SD-card boot layout.

Immediate tasks:

1. record the exact vendor toolchain version expected by the tree
2. archive the stock SD image or card contents
3. build or extract a known-good SD-card payload from the vendor tree
4. confirm serial console settings and recovery procedure
5. boot this image unchanged before introducing our payload

Expected toolchain baseline from the vendor tree:

- Vivado `2022.2`
- Vitis/XSCT compatible with that release
- the Buildroot-provided ARM cross-toolchain from the firmware tree

## 7. Phase 2: Add Our Userspace First

Do not replace the FPGA image yet.

First, boot vendor Linux and copy these binaries from our `ps` tree:

- `plane-feeder`
- `regdump`
- `fifo-monitor`
- `pps-check`
- `beast-client`

This proves:

- the board boots
- Linux is usable
- networking is usable
- our userland builds and runs on target

If the vendor FPGA image is still loaded, these tools will only be useful for
general environment validation, not our register map. That is still worthwhile.

## 8. Phase 3: Introduce Our Bitstream

Once the board boots reliably on the vendor-derived image:

1. build a Zynq design that preserves board-required PS and RF wiring
2. add our AXI register block into that design
3. assign and record the actual AXI base address
4. load the bitstream
5. rerun the hardware bring-up runbook

Hard requirements for that design:

- keep the board's PS configuration valid
- avoid colliding with vendor-reserved AXI regions
- explicitly document any PPS/GNSS input pin selection

## 9. Decision On PPS/GNSS Wiring

This is still open and must be settled before MLAT-grade timing work.

Questions to answer on the real board:

- is there a dedicated PPS-capable header or onboard GNSS path?
- does PPS already reach the Zynq through MIO or PL IO?
- is an external 10 MHz reference available and useful in the first revision?

Until that is answered:

- keep standard Beast mode as the default
- treat current Radarcape mode as provisional

## 10. Recommended Deliverables Before Hardware Arrives

Prepare these now:

1. a second SD card for experiments
2. a local archive of the vendor stock image
3. a copy of the vendor firmware source tree
4. cross-compiled ARM builds of the `ps` helper binaries
5. a short worksheet with:
   - serial device name
   - baud rate
   - board IP if static
   - expected boot files
   - recovery steps

## 11. Exit Criteria For "Image Ready"

We can say the Linux image is ready for bring-up when:

- we have a reproducible SD-card payload
- we know how to get back to stock
- we can boot to a root shell on serial
- Ethernet comes up
- we can copy and run our binaries
- we know how the bitstream will be loaded
- we know where our AXI block will live

At that point the next step is no longer Linux preparation. It is Vivado
integration and PS/PL validation on the actual hardware.
