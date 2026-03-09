# Ramdisk Overlay Plan

Purpose: make the vendor ramdisk image more useful for iterative bring-up
without repeatedly copying helper binaries or manually mounting the SD boot
partition.

## Overlay contents

The repo-local overlay lives at:

- `boot/ramdisk_overlay/`

Current files:

- `etc/init.d/S35bootfat`
  - creates `/boot`
  - mounts `/dev/mmcblk0p1` as `vfat` on `/boot`
- `root/.ssh/`
  - placeholder for an optional `authorized_keys`
- `usr/local/bin/`
  - destination for copied helper binaries

## Repack helper

Use:

- `tools/repack_pluto_initramfs.sh`

It:

1. prefers a repo-local source image:
   - `boot/uramdisk.image.gz.dist`
2. unpacks that image into a working directory
3. falls back to the older extracted rootfs tree only if the `.dist` image is
   not present
4. applies `boot/ramdisk_overlay/`
5. optionally installs a provided `authorized_keys` file as
   `/root/.ssh/authorized_keys`
6. copies these helper binaries if they exist under `ps/`:
   - `regdump-arm`
   - `fifo-monitor-arm`
   - `pps-check-arm`
   - `plane-feeder-arm`
   - `regpeek-arm`
7. repacks a new `uramdisk.image.gz`

Default output:

- `build/initramfs_out/uramdisk.image.gz`

Preferred source image:

- `boot/uramdisk.image.gz.dist`

If that source file is the stock Pluto-style U-Boot ramdisk image, install:

- `u-boot-tools`

on macOS so the repack helper can use `dumpimage` / `mkimage`.

## Intended result

After replacing the boot FAT partition's `uramdisk.image.gz` with the repacked
image:

- `/boot` should be mounted automatically from `/dev/mmcblk0p1`
- helper binaries should already be available under `/usr/local/bin`
- SSH public-key login can be enabled without recopying `authorized_keys`

## Usage

From the repo root on macOS:

```bash
./tools/repack_pluto_initramfs.sh ~/.ssh/id_ed25519.pub
```

Then copy:

- `build/initramfs_out/uramdisk.image.gz`

onto the FAT boot partition as:

- `uramdisk.image.gz`
