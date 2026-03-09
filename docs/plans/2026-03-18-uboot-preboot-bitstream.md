# U-Boot Preboot Bitstream Plan

Purpose: make the custom `plane_watcher` PL image available before Linux
probes `ad9361`, without immediately rebuilding `BOOT.bin`.

## 1. Why This Is Needed

The current hardware session proved:

- vendor Linux boots
- cleaned DT boots cleanly
- `regdump` reads the custom block at `0x43D00000`

But it also proved a critical ordering problem:

- `ad9361_probe` succeeds during kernel boot
- the custom PL image is only loaded later by manual `fpga_manager`
- after late PL reconfiguration, `ad9361` rebind fails with:
  - `Unsupported PRODUCT_ID 0x0`

This means the manual runtime PL swap is not a stable operating mode.

The radio must see the correct PL image **before** the kernel probes it.

## 2. Why Initramfs Is Too Late

The extracted initramfs boot order shows:

- kernel device probes happen before `/init`
- `/init` then execs `/sbin/init`
- `/etc/init.d/rcS` only runs after that

Observed live dmesg order already confirmed this:

- `ad9361 spi0.0: ad9361_probe ...`
- only later, when manually requested, `fpga_manager fpga0: writing ...`

So an initramfs shell script cannot fix probe ordering for the built-in
`ad9361` driver on this image.

## 3. Better Intermediate Fix

Use U-Boot `uenvcmd` during SD boot to load the custom bitstream from the FAT
partition before `bootm`.

Why this is a good next step:

- small and reversible
- does not require immediate `BOOT.bin` rebuild
- happens before Linux kernel probe
- uses the existing vendor SD-boot path

## 4. Vendor U-Boot Facts

The stock `uEnv.txt` already defines:

- `bitstream_image=system.bit.bin`
- `loadbit_addr=0x100000`

And the boot flow already does:

- import `uEnv.txt`
- run `uenvcmd` during `sdboot`

So the missing piece is only the actual `uenvcmd` body.

## 5. Repo Artifact

The repo now contains:

- `boot/uEnv.plane_watcher.txt`

which sets:

- `uenvcmd=... load mmc ... && fpga load ...`

This expects the custom boot-time PL image on the FAT partition as:

- `system.bit.bin`

The current source artifact is:

- `hdl/vivado/build/plane_watcher/plane_watcher.runs/impl_1/zynq_adsb_vendor_wrapper.bit.bin`

For boot testing, copy or rename it on the FAT partition to:

- `system.bit.bin`

## 6. Test Procedure

1. Keep the cleaned DT:
   - `devicetree.dtb`

2. Add the custom bitstream to the FAT partition:
   - `system.bit.bin`

3. Merge the repo's `boot/uEnv.plane_watcher.txt` into the FAT partition's
   `uEnv.txt`

4. Reboot from SD

5. Check dmesg for:
   - successful U-Boot boot
   - successful `ad9361_probe`
   - absence of stale PL driver probes

6. On Linux, check:
   - `/root/regdump-arm --base-addr 0x43D00000`
   - `/sys/bus/iio/devices/iio:device0/ensm_mode`
   - whether ENSM can leave `sleep`

## 7. Success Criteria

This experiment is successful if all of these are true after a cold boot:

- `ad9361_probe` still succeeds
- custom AXI block at `0x43D00000` is alive
- no late runtime `fpga_manager` step is required
- ENSM no longer appears trapped purely because the PL arrived too late

## 8. Failure Criteria

If this still fails cleanly, stop patching the vendor image.

The next step after that is:

- rebuild `BOOT.bin` so PL is part of the power-on boot image

That is the correct pivot point if the U-Boot preboot load still does not
produce a stable AD9361 bring-up.
