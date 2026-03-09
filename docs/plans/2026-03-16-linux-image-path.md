# Linux Image Path

Purpose: define the practical path for getting Linux onto the current board
without turning first bring-up into a full platform-port project.

This document complements:

- `docs/plans/2026-03-15-first-custom-sd-image.md`
- `docs/plans/2026-03-16-hybrid-bitstream-handoff.md`
- `docs/plans/2026-03-16-first-board-session-checklist.md`

It is more operational than the earlier SD-image plan and records what the
vendor firmware tree actually appears to build.

## 1. Recommendation

Do not try to invent a fresh Linux platform right now.

For first board bring-up, the right path is:

1. keep a stock or known-good vendor SD card as recovery
2. use the vendor firmware tree to produce a bootable Linux image
3. boot vendor Linux first, unchanged if possible
4. only then introduce:
   - our PS helper binaries
   - our hybrid bitstream
   - our register access tests

Reason:

- the vendor tree already encodes the DDR, PS MIO, boot, and AD936x
  assumptions for this exact board family
- our current risk is no longer "can Linux boot on this hardware?"
- our current risk is "can our custom FPGA image and register map work on a
  board that already boots correctly?"

## 2. What The Vendor Tree Actually Builds

From the vendor top-level `Makefile`, the firmware tree builds:

- Buildroot toolchain and rootfs
- U-Boot
- Linux kernel (`zImage` / `uImage`)
- device tree blobs
- FSBL
- `BOOT.bin`
- an SD-card payload through the `sdimg` target

Relevant observed details:

- expected Vivado version: `2022.2`
- Buildroot defconfig: `buildroot/configs/zynq_pluto_defconfig`
- DTS base: `linux/arch/arm/boot/dts/zynq-pluto-sdr.dtsi`
- SD image target: `make sdimg`

The `sdimg` target assembles at least:

- `BOOT.bin`
- `uImage`
- `devicetree.dtb`
- `uEnv.txt`
- `uramdisk.image.gz`

inside a staging directory:

- `build_sdimg/`

That is exactly the kind of payload we want for first Linux bring-up.

## 3. Important Toolchain Constraint

There is one major split in the current workflow:

- our custom Vivado/bitstream work has been done successfully in `Vivado 2025.2`
- the vendor firmware tree explicitly expects `Vivado 2022.2`

This means:

- do not assume the vendor full firmware build will be happy under 2025.2
- do not block Linux bring-up on forcing the vendor firmware tree through a new
  tool version immediately

Practical implication:

- if you already have a known-good vendor SD image, use that first
- only rebuild the vendor Linux image if you need to, and preferably in an
  environment aligned with the vendor expectation (`2022.2`)

## 4. Recommended Phases

### Phase A: Recovery Baseline

Before changing anything:

- archive the stock SD card contents if you have them
- or archive the known-good vendor boot files if those are available
- record serial settings and recovery steps

Minimum things to record:

- serial device name
- baud rate
- Ethernet method or expected default IP behavior
- boot partition file list
- how to restore stock boot files

### Phase B: Boot Vendor Linux First

Goal:

- prove the board boots Linux before mixing in our custom FPGA image

Use either:

- a stock vendor SD image
- or a vendor-built `sdimg` payload

At this point, success means:

- serial console works
- root shell works
- Ethernet works
- `/dev/mem` is available

### Phase C: Add Our PS Helpers

Once vendor Linux boots reliably, copy in:

- `regdump`
- `fifo-monitor`
- `pps-check`
- `plane-feeder`
- `beast-client`

This isolates Linux/userland problems from HDL problems.

### Phase D: Replace The FPGA Payload

Only after Phase B and C are stable:

- introduce the hybrid bitstream
- confirm register access at `0x43D00000`
- run the first-board checklist

At that point the Linux image problem is largely solved. The remaining work is
PS/PL and RF bring-up.

## 5. Concrete Vendor Build Path

If you decide to build the vendor Linux payload from source, the top-level
vendor tree suggests this rough sequence:

```bash
cd ~/sdr/Fish-Wan-plutosdr-fw-7020-SDR
make TOOLCHAIN
make
make sdimg
```

Important notes:

- this assumes the vendor prerequisites are installed
- it assumes the vendor submodules/repos are populated correctly
- it assumes the Vivado/XSCT path matches the vendor expectation
- `make` in that tree is not lightweight; it can build HDL, U-Boot, Linux,
  Buildroot, and FSBL-related artifacts

The SD image staging directory is expected to be:

- `build_sdimg/`

Likely key output files:

- `build_sdimg/BOOT.bin`
- `build_sdimg/uImage`
- `build_sdimg/devicetree.dtb`
- `build_sdimg/uEnv.txt`
- `build_sdimg/uramdisk.image.gz`

## 6. Recommended First Linux Strategy

For this project, the best first Linux strategy is:

1. boot a vendor image unchanged
2. verify console, network, and root shell
3. stage our helper binaries
4. only then start swapping in our FPGA image or boot artifacts

Why this is the right order:

- it decouples Linux boot problems from FPGA integration problems
- it gives you a recovery baseline
- it avoids a situation where bootloader, kernel, DT, and bitstream all change
  at once

## 7. Where Our Custom Bitstream Fits

The current custom bitstream is:

- `zynq_adsb_vendor_wrapper.bit`

It was generated from the repo’s Vivado flow, not the vendor firmware tree.

For first board work, it is completely acceptable to:

- keep the vendor Linux userspace/kernel/boot chain
- replace only the FPGA image during testing

That is the lowest-risk split between software and PL work.

## 8. Device Tree Implications

The vendor DTS currently includes:

- vendor DMA blocks
- `axi_ad9361`
- a legacy node at `0x43c00000` (`mwipcore`)

Our current design now uses:

- `0x43C0_0000` for `axi_ad9361`
- `0x43D0_0000` for `adsb_vendor_wrapper`

Implication:

- the stock vendor DT is not yet the final truth for our custom PL additions
- but for first Linux boot, that does not need to stop us
- `/dev/mem` access can validate the custom block before a clean permanent DT
  node is added

So the recommended order is:

1. boot first
2. validate via `/dev/mem`
3. clean up the DT only after the hardware path is proven

## 9. First Boot Success Criteria

You can call Linux "good enough for board bring-up" when all of these are true:

- serial console works
- you can log in as root
- Ethernet comes up
- `/dev/mem` access works
- you can run `regdump`
- you can run `plane-feeder`
- you know how the FPGA image is being loaded for the session

That is enough to move from Linux concerns to actual hardware validation.

## 10. Next Actions

Short-term:

1. identify whether you already have a known-good stock/vendor SD image
2. if yes, archive it and use it first
3. if no, attempt a vendor-tree `sdimg` build in a vendor-compatible
   environment
4. prepare the PS helper binaries for the target
5. define how the current custom bitstream will be loaded during bring-up

After that:

- follow `docs/plans/2026-03-16-first-board-session-checklist.md`

## 11. Decision

The recommended Linux path from here is:

- vendor Linux first
- custom userspace second
- custom bitstream third
- custom DTS cleanup after `/dev/mem` validation, not before

That is the lowest-risk way to get the current board from "bitstream exists" to
"real end-to-end hardware session."
