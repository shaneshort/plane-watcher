#!/bin/bash
# Glue around firmware/scripts/run-build.sh for the plane_watcher Petalinux
# build. Stages the Vivado XSA, imports it, builds, then packages BOOT.BIN
# with no bitstream (split-boot topology: QSPI = FSBL + U-Boot only, SD =
# image.ub).
#
# NOTE (2026-04-18): The firmware BSP defaults to the 2025.2 SDT flow, but
# SDT generation requires xsct (a Vitis tool) which is not installed. We've
# forced the project into XSA mode by editing `.petalinux/metadata`
# (HDF_EXT=xsa) and `project-spec/configs/config`
# (# CONFIG_SUBSYSTEM_SDT_FLOW is not set). Once Vitis is installed, we can
# flip back to SDT and restore the xsct `sdtgen generate_sdt` step between
# XSA staging and import. The whole Petalinux toolset retires in 2026.2
# anyway, so this is short-lived.
#
# Environment overrides:
#   XSA_PATH           XSA from the Vivado build (default: Phase 1 output)
#   PETALINUX_DIR      Petalinux project dir (default: firmware/petalinux)
#   DEPLOY_DIR         Output staging (default: build/smartzynq/deploy)
#   RUN_BUILD          Path to firmware/scripts/run-build.sh wrapper
#   SKIP_IMPORT=1      Skip the hw-description import
#   SKIP_BUILD=1       Skip petalinux-build (package only)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

XSA_PATH="${XSA_PATH:-$REPO_ROOT/build/smartzynq/vivado/plane_watcher_phase1.xsa}"
PETALINUX_DIR="${PETALINUX_DIR:-$REPO_ROOT/firmware/petalinux}"
DEPLOY_DIR="${DEPLOY_DIR:-$REPO_ROOT/build/smartzynq/deploy}"
RUN_BUILD="${RUN_BUILD:-$REPO_ROOT/firmware/scripts/run-build.sh}"
SKIP_IMPORT="${SKIP_IMPORT:-0}"
SKIP_BUILD="${SKIP_BUILD:-0}"

if [[ ! -x "$RUN_BUILD" ]]; then
  echo "error: firmware build wrapper not found or not executable: $RUN_BUILD" >&2
  exit 1
fi

if [[ ! -d "$PETALINUX_DIR" ]]; then
  echo "error: Petalinux project not found: $PETALINUX_DIR" >&2
  exit 1
fi

if [[ ! -f "$XSA_PATH" ]]; then
  echo "error: XSA not found: $XSA_PATH" >&2
  echo "       run tools/build-smartzynq-phase1.sh first" >&2
  exit 1
fi

# Stage the XSA inside the project tree so the container (which bind-mounts
# $PETALINUX_DIR as /work) can see it. hardware/ is gitignored per the
# firmware README and explicitly called out as the drop-in location.
staged_xsa_host="$PETALINUX_DIR/hardware/plane_watcher.xsa"
staged_xsa_container="./hardware/plane_watcher.xsa"
mkdir -p "$(dirname "$staged_xsa_host")"
cp -f "$XSA_PATH" "$staged_xsa_host"
echo "==> Staged XSA: $staged_xsa_host"

if [[ "$SKIP_IMPORT" != "1" ]]; then
  echo "==> Importing hardware description (XSA)"
  "$RUN_BUILD" petalinux-config --silentconfig --get-hw-description "$staged_xsa_container"
fi

if [[ "$SKIP_BUILD" != "1" ]]; then
  echo "==> petalinux-build"
  "$RUN_BUILD"
fi

# Package BOOT.BIN with FSBL + U-Boot + bitstream.
#
# The bitstream has to ride in BOOT.BIN (not split out onto SD) because
# UART0 is on EMIO. Without a loaded bitstream the EMIO TXD signal has no
# path to pin L17 and both FSBL and U-Boot run silently, making any early
# boot failure undebuggable. Once we're in U-Boot we can reprogram the PL
# from SD (`fpga load`) if we want to iterate on bitstream without re-flashing
# QSPI, but we need at least one bitstream loaded by FSBL to see anything.
echo "==> petalinux-package --boot (FSBL + U-Boot + bitstream)"
"$RUN_BUILD" petalinux-package --boot \
    --format BIN \
    --fsbl \
    --u-boot \
    --fpga \
    --force

# Stage artefacts for deployment / SD-card writes.
images_dir="$PETALINUX_DIR/images/linux"
for f in BOOT.BIN image.ub boot.scr; do
  src="$images_dir/$f"
  if [[ -f "$src" ]]; then
    mkdir -p "$DEPLOY_DIR"
    cp -v "$src" "$DEPLOY_DIR/"
  fi
done

cat <<EOF

Artefacts staged in: $DEPLOY_DIR
  BOOT.BIN  -> flash to QSPI once via JTAG (program_flash -flash_type qspi_single)
  image.ub  -> copy to FAT32 partition on SD for every dev iteration

DIP switches for operation: QSPI boot (S1=off, S2=on).
EOF
