# Minimal Hybrid DT Plan

Purpose: define the smallest device-tree change needed to boot vendor Linux
against the `plane_watcher` hybrid FPGA image without stale vendor PL drivers
binding to nonexistent hardware.

## 1. Problem Statement

The current board session proved all of these:

- vendor Linux boots
- `fpga_manager` can load `zynq_adsb_vendor_wrapper`
- the custom AXI register block is alive at `0x43D00000`
- `regdump` reads correct values from the live hardware

But the running kernel still describes the old vendor PL design.

Observed stale vendor PL nodes:

- `cf-ad9361-lpc@79020000`
- `cf-ad9361-dds-core-lpc@79024000`
- `dma@7c400000`
- `dma@7c420000`
- `mwipcore@43c00000`

Why this matters:

- those nodes cause Linux to bind drivers for hardware that is no longer in the
  loaded bitstream
- after the custom bitstream is loaded, the kernel and PL no longer match
- ADI userspace then behaves badly:
  - `iio_info` segfaulted
  - `ensm_mode` writes did not behave normally

This is a DT/kernel mismatch, not an AXI-register failure.

## 2. Current Hybrid PL Map

The current custom hybrid bitstream contains:

- `axi_ad9361` at `0x43C00000`
- `adsb_vendor_wrapper` at `0x43D00000`

The current hybrid bitstream does **not** contain:

- vendor ADC core at `0x79020000`
- vendor DDS core at `0x79024000`
- vendor RX DMA at `0x7C400000`
- vendor TX DMA at `0x7C420000`
- legacy `mwipcore` at `0x43C00000`

## 3. Minimal DT Strategy

Do not try to produce a fully polished final DT yet.

The smallest useful DT change is:

1. keep the vendor base DTS for PS, memory, Ethernet, USB, SPI, and AD9361 PHY
2. delete the stale PL nodes that no longer exist in the custom bitstream
3. optionally add a simple `generic-uio` node for `adsb_vendor_wrapper`
4. continue using `/dev/mem` for first custom-block access

That is enough to stop Linux from binding known-bad stale drivers.

## 4. Repo Artifact

The repo now contains a minimal DT fragment:

- `linux-dts/zynq-pluto-sdr-plane-watcher-hybrid.dtsi`

and a Rev.C DTS entrypoint:

- `linux-dts/zynq-pluto-sdr-plane-watcher-hybrid-revc.dts`

It does these things:

- deletes:
  - `dma@7c400000`
  - `dma@7c420000`
  - `cf-ad9361-lpc@79020000`
  - `cf-ad9361-dds-core-lpc@79024000`
  - `mwipcore@43c00000`
- adds an optional `generic-uio` node for:
  - `plane_watcher_axi` at `0x43D00000`

## 5. What This DT Does Not Try To Solve Yet

It intentionally does **not**:

- add a full kernel binding for `axi_ad9361` at `0x43C00000`
- add a custom kernel driver for `adsb_vendor_wrapper`
- solve final userspace IIO streaming
- recreate the full vendor DMA-based RX/TX kernel path

That would be a larger platform port and is not needed to validate the current
custom block.

## 6. Intended Bring-Up Effect

With the minimal DT in place, Linux should:

- keep `ad9361-phy` on SPI
- stop binding stale PL-side ADC/DDS/DMA drivers
- stop crashing in userspace when probing those nonexistent vendor PL blocks
- still allow the custom block to be accessed through `/dev/mem`

That is the correct next intermediate state.

## 7. Practical Next Step

Create a board DTS based on the vendor board DTS currently used by the image,
and include this fragment.

The current live board reported:

- `Analog Devices PlutoSDR Rev.C (Z7020/AD9363)`

So the current best base DTS is the vendor Rev.C board file.

Before final compilation confirm:

- current `/proc/device-tree/model`
- current DTB filename used by the boot flow

Then:

1. use the repo DTS entrypoint:
   - `linux-dts/zynq-pluto-sdr-plane-watcher-hybrid-revc.dts`
2. copy that file and the companion `.dtsi` into the vendor kernel DTS tree
3. compile a replacement DTB
4. boot vendor Linux with the same kernel/rootfs but the corrected DTB
5. reload the custom bitstream
6. re-test:
   - `regdump`
   - radio state visibility
   - FIFO behavior

## 8. Boot Artifact Observation

The extracted vendor FAT boot partition contains:

- `BOOT.bin`
- `uImage`
- `devicetree.dtb`
- `uEnv.txt`
- `uramdisk.image.gz`

The current image therefore appears to boot from a plain:

- `devicetree.dtb`

replacement rather than requiring an immediate deeper boot-flow change.

## 9. Decision

The next Linux/PL integration step is:

- minimal DT cleanup first
- not more live shell poking against the stale vendor PL map

That is the smallest, cleanest move that addresses the actual failure mode we
observed on hardware.
