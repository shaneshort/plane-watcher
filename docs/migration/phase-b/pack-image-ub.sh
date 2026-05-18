#!/usr/bin/env bash
# pack-image-ub.sh — reproduce the Phase B EDF image.ub for Smart ZYNQ SL bring-up.
#
# Inputs:  an EDF deploy directory containing zImage, devicetree/cortexa9-linux.dtb,
#          and core-image-minimal-*.rootfs.cpio.gz (raw, NOT .u-boot wrapped).
# Output:  image.ub ready to drop onto the SD FAT partition alongside the existing
#          Phase A PetaLinux BOOT.BIN and boot.scr.
#
# Background:
#   Phase B (per MIGRATION.md) tests the EDF kernel/rootfs path while reusing the
#   known-good PetaLinux BOOT.BIN. EDF's `zynq-zc702-sdt-full` machine pulls the
#   public ZC702 reference SDT, so the generated DTB describes ZC702 hardware, not
#   Smart ZYNQ SL. We patch the DTB with the minimum required corrections to let
#   the EDF kernel boot to login on our board, then bundle it into a FIT image.ub
#   that the existing PetaLinux boot.scr knows how to load.
#
# These patches are deliberately throw-away — Phase C replaces this entire flow
# by feeding our own SDT to `gen-machine-conf` so the DTB is correct from source.
#
# Usage:
#   ./pack-image-ub.sh <edf-deploy-dir> <output-image.ub>
#
# Example:
#   ./pack-image-ub.sh \
#       ~/edf-workspaces/plane-watcher/build/tmp/deploy/images/zynq-zc702-sdt-full \
#       /tmp/edf-phase-b-image.ub

set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "Usage: $0 <edf-deploy-dir> <output-image.ub>" >&2
    exit 1
fi

DEPLOY="$1"
OUT="$2"

ZIMAGE="${DEPLOY}/zImage"
DTB_SRC="${DEPLOY}/devicetree/cortexa9-linux.dtb"
ROOTFS=$(ls "${DEPLOY}"/core-image-minimal-*.rootfs.cpio.gz 2>/dev/null \
    | grep -v '\.u-boot$' | head -n1 || true)

if [[ -z "${ROOTFS}" ]]; then
    echo "Error: no core-image-minimal-*.rootfs.cpio.gz in ${DEPLOY}" >&2
    echo "(Use the raw .cpio.gz; the .cpio.gz.u-boot wrapper breaks initrd loading.)" >&2
    exit 1
fi

for f in "${ZIMAGE}" "${DTB_SRC}"; do
    [[ -f "${f}" ]] || { echo "Error: missing ${f}" >&2; exit 1; }
done

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT

DTB="${TMP}/cortexa9-linux-patched.dtb"
cp "${DTB_SRC}" "${DTB}"

# ---- DTB patches (Smart ZYNQ SL corrections to the ZC702 reference DT) ----

# DDR size: ZC702 has 1 GiB; Smart ZYNQ SL has 512 MiB.
fdtput -t x "${DTB}" /memory@0 reg 0x00 0x20000000

# Boot args: kernel needs an explicit console and rootfs.
fdtput -t s "${DTB}" /chosen bootargs \
    "console=ttyPS0,115200 earlycon root=/dev/ram0 rw"

# Early console node path (used before bootargs is parsed).
fdtput -t s "${DTB}" /chosen stdout-path \
    "/axi/serial@e0000000:115200n8"

# UART aliases — ZC702 wires its console to UART1, Smart ZYNQ SL wires it to UART0
# via PL EMIO. ttyPS0/ttyPS1 numbering follows the serial0/serial1 aliases.
fdtput -t s "${DTB}" /aliases serial0 "/axi/serial@e0000000"
fdtput -t s "${DTB}" /aliases serial1 "/axi/serial@e0001000"

# UART0 node: EDF DT marks it disabled (ZC702 unused-port convention). Enable it
# and add the xlnx,clock-freq hints that PetaLinux's working DT carries. Without
# these the kernel xuartps driver reprograms UART0's baud divisor based on a
# clock-tree walk that doesn't match what FSBL actually configured, producing
# garbled output partway through driver probe.
fdtput -t s "${DTB}" /axi/serial@e0000000 status "okay"
fdtput -t x "${DTB}" /axi/serial@e0000000 xlnx,clock-freq        0x5f5e100  # 100 MHz
fdtput -t x "${DTB}" /axi/serial@e0000000 xlnx,uart-clk-freq-hz  0x5f5e100  # 100 MHz
fdtput -t x "${DTB}" /axi/serial@e0000000 xlnx,has-modem 0x00
fdtput -t x "${DTB}" /axi/serial@e0000000 port-number    0x00
fdtput -p -t lu "${DTB}" /axi/serial@e0000000 cts-override

# ---- Assemble image.ub ----
#
# Path to image.its is resolved relative to this script. Note the .its uses
# absolute paths that the user must adjust if running from a different layout.
ITS="$(dirname "$(readlink -f "$0")")/image.its"

# We need an .its with paths pointing at the artifacts we have. Generate a copy
# in the temp dir with substituted paths so the original template stays generic.
sed -e "s|@ZIMAGE@|${ZIMAGE}|" \
    -e "s|@DTB@|${DTB}|" \
    -e "s|@ROOTFS@|${ROOTFS}|" \
    "${ITS}" > "${TMP}/image.its"

mkimage -f "${TMP}/image.its" "${OUT}"
echo "image.ub written to ${OUT} ($(stat -c %s "${OUT}") bytes)"
echo
echo "Verify with: dumpimage -l ${OUT}"
