#!/bin/bash
# Full rebuild chain: Vivado bitstream → FSBL (with sstate invalidation) →
# Yocto/EDF build + BOOT.BIN packaging → scp image.ub to the running
# board and reboot (or fall back to a local SD copy — see --sd).
# A bitstream-only mode is also available: Vivado rebuild → re-import XSA →
# repackage BOOT.BIN with the existing FSBL/U-Boot artefacts → deploy
# BOOT.BIN, skipping the full Yocto rebuild.
#
# The canonical entry point is now `go -C ps run ./cmd/builder` (TUI/CLI);
# this script is the thin shell equivalent retained for habit and CI use.
#
# Use the full flow after any change to the PS7 config / bitstream TCL. The
# cleansstate step is what makes PS7 or DDR config changes actually
# propagate into the FSBL — without it, bitbake silently reuses the
# cached FSBL built from the previous hardware description.
#
# For device-tree, rootfs, or kernel-config iterations, use --fast to skip
# Vivado + FSBL cleansstate (~5-10 min saved per cycle).
#
# Deploy defaults to SSH: scp artefacts into the board's auto-mounted SD
# partition at /run/media/mmcblk0p1 and reboot. Use --sd to fall back to
# copying straight to a locally-mounted SD card (needed before the board
# can boot into a Linux image that has SSH, i.e. first-time provisioning).
#
# Flags:
#   --bitstream     Rebuild Vivado, repackage BOOT.BIN only, and deploy it
#                   without a full petalinux-build
#   --fast          Skip Vivado + FSBL cleansstate; just Petalinux + deploy
#   --skip-vivado   Skip Vivado but still run FSBL cleansstate / packaging
#   --sd            Deploy by copying to a locally-mounted SD card
#                   (default is SSH/scp to the running board)
#   --no-reboot     Skip the post-deploy reboot
#   --no-deploy     Don't deploy anywhere (build only). Alias: --skip-sd
#
# Environment overrides:
#   TARGET_HOST         SSH target (default: root@planewatcher.local)
#   SSHPASS_PASSWORD    Password for the SSH target (default: planewatcher)
#   TARGET_IMAGE_DIR    Where deploy artefacts land on the board
#                       (default: /run/media/mmcblk0p1)
#   SD_MOUNT            Local SD mount point for --sd
#                       (default: /media/shanes/52AA-902E)
#   SKIP_SD=1 / NO_DEPLOY=1   Skip deploy entirely

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

SD_MOUNT="${SD_MOUNT:-/media/shanes/52AA-902E}"
# Keep both names: SKIP_SD is the historic env var; NO_DEPLOY is the
# current name now that deploy can go via SSH instead of SD.
NO_DEPLOY="${NO_DEPLOY:-${SKIP_SD:-0}}"
BITSTREAM_ONLY=0
SKIP_VIVADO=0
SKIP_FSBL_CLEAN=0
USE_SD=0
NO_REBOOT="${NO_REBOOT:-0}"
TARGET_HOST="${TARGET_HOST:-root@planewatcher.local}"
SSHPASS_PASSWORD="${SSHPASS_PASSWORD:-planewatcher}"
TARGET_IMAGE_DIR="${TARGET_IMAGE_DIR:-/run/media/mmcblk0p1}"
RUN_BUILD="${RUN_BUILD:-$REPO_ROOT/firmware/scripts/run-build.sh}"
DEPLOY_DIR="${DEPLOY_DIR:-$REPO_ROOT/build/smartzynq/deploy}"
PROJECT_DIR="${PROJECT_DIR:-$REPO_ROOT/build/smartzynq/vivado}"
# Routed timing report emitted by Vivado impl_1. Paths must track the
# project and BD-wrapper names in hdl/vivado/build_smartzynq_phase1.tcl.
TIMING_RPT="${TIMING_RPT:-$PROJECT_DIR/plane_watcher_phase1.runs/impl_1/smartzynq_phase1_wrapper_timing_summary_routed.rpt}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bitstream)
      BITSTREAM_ONLY=1
      SKIP_FSBL_CLEAN=1
      ;;
    --fast)
      SKIP_VIVADO=1
      SKIP_FSBL_CLEAN=1
      ;;
    --skip-vivado)
      SKIP_VIVADO=1
      ;;
    --sd)
      USE_SD=1
      ;;
    --no-reboot)
      NO_REBOOT=1
      ;;
    --no-deploy|--skip-sd)
      NO_DEPLOY=1
      ;;
    -h|--help)
      sed -n '2,32p' "$0" | sed 's|^# \{0,1\}||'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 1
      ;;
  esac
  shift
done

if [[ "$BITSTREAM_ONLY" == "1" && "$SKIP_FSBL_CLEAN" != "1" ]]; then
  SKIP_FSBL_CLEAN=1
fi

if [[ "$BITSTREAM_ONLY" == "1" ]]; then
  TOTAL_STEPS=3
else
  TOTAL_STEPS=4
fi

if [[ "$SKIP_VIVADO" == "1" ]]; then
  echo "==> [1/$TOTAL_STEPS] Skipping Vivado (--fast / --skip-vivado)"
else
  echo "==> [1/$TOTAL_STEPS] Vivado: rebuild bitstream + XSA"
  "$SCRIPT_DIR/build-smartzynq-phase1.sh"

  # Gate the rest of the pipeline on timing closure. Vivado does NOT exit
  # non-zero when impl finishes with negative slack — it just writes the
  # violation into the routed timing report. Packaging BOOT.BIN with a
  # timing-failed bitstream and shipping it to the board would produce a
  # mystery-misbehaviour nightmare.
  if [[ ! -f "$TIMING_RPT" ]]; then
    echo "ERROR: routed timing summary not found: $TIMING_RPT" >&2
    exit 1
  fi
  if grep -q "Timing constraints are not met." "$TIMING_RPT"; then
    echo "ERROR: timing constraints are not met. Refusing to package or deploy." >&2
    awk '
      /Design Timing Summary/ {show=1}
      show {print}
      /Clock Summary/ && show {exit}
    ' "$TIMING_RPT" >&2
    exit 1
  fi
fi

if [[ "$BITSTREAM_ONLY" == "1" ]]; then
  echo "==> [2/$TOTAL_STEPS] Yocto: import XSA, package BOOT.BIN only"
  SKIP_BUILD=1 "$SCRIPT_DIR/build-yocto.sh"
elif [[ "$SKIP_FSBL_CLEAN" == "1" ]]; then
  echo "==> [2/$TOTAL_STEPS] Skipping FSBL cleansstate (--fast)"
else
  echo "==> [2/$TOTAL_STEPS] Yocto: cleansstate fsbl-firmware multiconfig"
  # Without this, bitbake reuses cached FSBL from the previous XSA. Learned
  # the hard way during bring-up.
  CLEANSSTATE_FSBL=1 "$SCRIPT_DIR/build-yocto.sh"
  echo "==> [3/$TOTAL_STEPS] Yocto: import XSA, build, package BOOT.BIN"
  "$SCRIPT_DIR/build-yocto.sh"
fi

if [[ "$NO_DEPLOY" == "1" ]]; then
  echo "==> [$TOTAL_STEPS/$TOTAL_STEPS] Skipping deploy (--no-deploy)"
  exit 0
fi

if [[ "$USE_SD" == "1" ]]; then
  if [[ ! -d "$SD_MOUNT" ]]; then
    echo "==> [$TOTAL_STEPS/$TOTAL_STEPS] SD mount not found at $SD_MOUNT — skipping copy"
    echo "    set SD_MOUNT=<path>, mount the card, or drop --sd to deploy over SSH."
    if [[ "$BITSTREAM_ONLY" == "1" ]]; then
      echo "    manual fallback: cp $DEPLOY_DIR/BOOT.BIN <sd_mount>/ && sync"
    else
      echo "    manual fallback: cp $DEPLOY_DIR/{BOOT.BIN,image.ub,boot.scr} <sd_mount>/ && sync"
    fi
    exit 0
  fi
  echo "==> [$TOTAL_STEPS/$TOTAL_STEPS] Copying artefacts to $SD_MOUNT"
  cp -v "$DEPLOY_DIR/BOOT.BIN"  "$SD_MOUNT/"
  if [[ "$BITSTREAM_ONLY" != "1" ]]; then
    cp -v "$DEPLOY_DIR/image.ub" "$SD_MOUNT/"
    [[ -f "$DEPLOY_DIR/boot.scr" ]] && cp -v "$DEPLOY_DIR/boot.scr" "$SD_MOUNT/"
  fi
  sync
  sudo eject /dev/sda
  echo "==> Done. SD card safe to eject."
  exit 0
fi

if [[ "$BITSTREAM_ONLY" == "1" ]]; then
  echo "==> [$TOTAL_STEPS/$TOTAL_STEPS] Deploying BOOT.BIN to $TARGET_HOST:$TARGET_IMAGE_DIR"
else
  echo "==> [$TOTAL_STEPS/$TOTAL_STEPS] Deploying BOOT.BIN + image.ub to $TARGET_HOST:$TARGET_IMAGE_DIR"
fi

if ! command -v sshpass >/dev/null; then
  echo "error: sshpass not installed (required for password-based SSH deploy)" >&2
  echo "       install it (apt install sshpass) or pass --sd to use the local SD path." >&2
  exit 1
fi

# Mirror the SSH hygiene flags used by tools/deploy-bitstream.sh: ignore
# the user ssh config entirely, never touch known_hosts, and force
# password auth so stray ssh-agent keys don't shadow the board password.
SSH_COMMON_OPTS=(
  -F /dev/null
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o CheckHostIP=no
  -o PubkeyAuthentication=no
)

if [[ "$BITSTREAM_ONLY" == "1" ]]; then
  sshpass -p"$SSHPASS_PASSWORD" scp "${SSH_COMMON_OPTS[@]}" -O \
      "$DEPLOY_DIR/BOOT.BIN" "$TARGET_HOST:$TARGET_IMAGE_DIR/BOOT.BIN"
else
  sshpass -p"$SSHPASS_PASSWORD" scp "${SSH_COMMON_OPTS[@]}" -O \
      "$DEPLOY_DIR/BOOT.BIN" "$DEPLOY_DIR/image.ub" \
      "$TARGET_HOST:$TARGET_IMAGE_DIR/"
fi
sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "$TARGET_HOST" sync

if [[ "$NO_REBOOT" == "1" ]]; then
  echo "==> Deploy complete. Reboot skipped (--no-reboot)."
  exit 0
fi

echo "==> Rebooting $TARGET_HOST"
# `reboot` severs the SSH session before ssh gets an exit status, which
# would fail the script under `set -e`. Swallow that specific case.
sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "$TARGET_HOST" reboot || true
echo "==> Done."
