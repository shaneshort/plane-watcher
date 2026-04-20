#!/bin/bash
# Full rebuild chain: Vivado bitstream → FSBL (with sstate invalidation) →
# Petalinux build + BOOT.BIN packaging → copy artefacts to SD card.
#
# Use the full flow after any change to the PS7 config / bitstream TCL. The
# cleansstate steps are what makes PS7 or DDR config changes actually
# propagate into the FSBL — without them, bitbake silently reuses the
# cached FSBL built from the previous hardware description.
#
# For device-tree, rootfs, or kernel-config iterations, use --fast to skip
# Vivado + FSBL cleansstate (~5-10 min saved per cycle).
#
# Flags:
#   --fast          Skip Vivado + FSBL cleansstate; just Petalinux + SD copy
#   --skip-vivado   Skip Vivado but still run FSBL cleansstate
#   --skip-sd       Don't copy to SD (alias for SKIP_SD=1)
#
# Environment overrides:
#   SD_MOUNT    SD card mount point (default: /media/shanes/52AA-902E)
#   SKIP_SD=1   Skip copying to SD (useful if SD isn't plugged in)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

SD_MOUNT="${SD_MOUNT:-/media/shanes/52AA-902E}"
SKIP_SD="${SKIP_SD:-0}"
SKIP_VIVADO=0
SKIP_FSBL_CLEAN=0
RUN_BUILD="${RUN_BUILD:-$REPO_ROOT/firmware/scripts/run-build.sh}"
DEPLOY_DIR="${DEPLOY_DIR:-$REPO_ROOT/build/smartzynq/deploy}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --fast)
      SKIP_VIVADO=1
      SKIP_FSBL_CLEAN=1
      ;;
    --skip-vivado)
      SKIP_VIVADO=1
      ;;
    --skip-sd)
      SKIP_SD=1
      ;;
    -h|--help)
      sed -n '2,22p' "$0" | sed 's|^# \{0,1\}||'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 1
      ;;
  esac
  shift
done

if [[ "$SKIP_VIVADO" == "1" ]]; then
  echo "==> [1/4] Skipping Vivado (--fast / --skip-vivado)"
else
  echo "==> [1/4] Vivado: rebuild bitstream + XSA"
  "$SCRIPT_DIR/build-smartzynq-phase1.sh"
fi

if [[ "$SKIP_FSBL_CLEAN" == "1" ]]; then
  echo "==> [2/4] Skipping FSBL cleansstate (--fast)"
else
  echo "==> [2/4] Petalinux: cleansstate FSBL stack (fsbl-firmware + fsbl)"
  # Without these, bitbake reuses cached FSBL from the previous XSA. Learned
  # the hard way during bring-up.
  "$RUN_BUILD" petalinux-build -c fsbl-firmware -x cleansstate
  "$RUN_BUILD" petalinux-build -c fsbl -x cleansstate
fi

echo "==> [3/4] Petalinux: import XSA, build, package BOOT.BIN"
"$SCRIPT_DIR/build-petalinux.sh"

if [[ "$SKIP_SD" == "1" ]]; then
  echo "==> [4/4] Skipping SD copy (SKIP_SD=1)"
  exit 0
fi

if [[ ! -d "$SD_MOUNT" ]]; then
  echo "==> [4/4] SD mount not found at $SD_MOUNT — skipping copy"
  echo "    set SD_MOUNT=<path> or SKIP_SD=1, or mount the card and rerun:"
  echo "    cp $DEPLOY_DIR/{BOOT.BIN,image.ub,boot.scr} <sd_mount>/ && sync"
  exit 0
fi

echo "==> [4/4] Copying artefacts to $SD_MOUNT"
cp -v "$DEPLOY_DIR/BOOT.BIN"  "$SD_MOUNT/"
cp -v "$DEPLOY_DIR/image.ub"  "$SD_MOUNT/"
[[ -f "$DEPLOY_DIR/boot.scr" ]] && cp -v "$DEPLOY_DIR/boot.scr" "$SD_MOUNT/"
sync
sudo eject /dev/sda
echo "==> Done. SD card safe to eject."
