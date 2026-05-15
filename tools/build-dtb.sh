#!/bin/bash
set -euo pipefail

# =============================================================================
# build-dtb.sh — Build the plane_watcher device tree blob from stock-tap DTS
# =============================================================================
# Uses cpp + dtc to compile the stock-tap DTS against the vendor kernel tree.
# The vendor kernel provides the base Zynq and Pluto Rev.C device tree sources
# plus the dt-bindings headers needed by the include chain.
# =============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

VENDOR_HDL_ROOT="${VENDOR_HDL_ROOT:-$HOME/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR}"
DTS_DIR="$REPO_ROOT/linux-dts"
DTS_INPUT="${DTS_INPUT:-$DTS_DIR/zynq-pluto-sdr-plane-watcher-stock-tap-revc.dts}"
DTB_OUTPUT="${DTB_OUTPUT:-$REPO_ROOT/linux-dts/devicetree.dtb}"

# Vendor kernel paths for the cpp include chain.
KERNEL_DTS_DIR="$VENDOR_HDL_ROOT/linux/arch/arm/boot/dts"
KERNEL_INCLUDE_DIR="$VENDOR_HDL_ROOT/linux/include"

usage() {
  cat <<EOF
Usage:
  $(basename "$0") [--deploy]

Options:
  --deploy   Push the built DTB to the target board (same host as deploy-bitstream.sh)
  -h, --help Show this help

Config:
  Sources $CONFIG_FILE if it exists.
  Override DTS_INPUT or DTB_OUTPUT via env vars if needed.

Requires: cpp, dtc
EOF
}

DEPLOY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --deploy)
      DEPLOY=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

# Preflight checks.
for cmd in cpp dtc; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "error: $cmd not found in PATH" >&2
    exit 1
  fi
done

if [[ ! -d "$KERNEL_DTS_DIR" ]]; then
  echo "error: vendor kernel DTS directory not found: $KERNEL_DTS_DIR" >&2
  echo "       set VENDOR_HDL_ROOT in plane_watcher.env" >&2
  exit 1
fi

if [[ ! -f "$DTS_INPUT" ]]; then
  echo "error: DTS input not found: $DTS_INPUT" >&2
  exit 1
fi

# Copy our DTS/dtsi into the vendor kernel DTS directory so the #include
# chain resolves without modifying the vendor tree permanently.
CLEANUP_FILES=()
cleanup() {
  for f in "${CLEANUP_FILES[@]}"; do
    rm -f "$f"
  done
}
trap cleanup EXIT

for f in "$DTS_DIR"/*.dts "$DTS_DIR"/*.dtsi; do
  [[ -f "$f" ]] || continue
  dest="$KERNEL_DTS_DIR/$(basename "$f")"
  if [[ ! -f "$dest" ]]; then
    cp "$f" "$dest"
    CLEANUP_FILES+=("$dest")
  fi
done

# Step 1: Preprocess with cpp (resolves #include and dt-bindings headers).
PREPROCESSED=$(mktemp /tmp/pw-dtb-XXXXXX.dts)
trap 'rm -f "$PREPROCESSED"; cleanup' EXIT

cpp -nostdinc \
    -I "$KERNEL_DTS_DIR" \
    -I "$KERNEL_INCLUDE_DIR" \
    -undef -x assembler-with-cpp \
    "$KERNEL_DTS_DIR/$(basename "$DTS_INPUT")" \
    "$PREPROCESSED"

# Step 2: Compile to DTB, renaming the bus node from "axi" to "amba" to match
# the convention the kernel and existing device trees expect. This mirrors the
# sed fixup in the firmware Makefile's DTB rule.
dtc -I dts -O dtb -@ "$PREPROCESSED" \
  | dtc -q -I dtb -O dts -@ - \
  | sed -e 's/axi {/amba {/g' -e 's|/axi/|/amba/|g' \
  | dtc -q -I dts -O dtb -@ -o "$DTB_OUTPUT" -

echo "DTB built: $DTB_OUTPUT"
md5sum "$DTB_OUTPUT"

if [[ "$DEPLOY" == "1" ]]; then
  TARGET_HOST="${TARGET_HOST:-root@pluto.local}"
  TARGET_BOOT_MOUNT="${TARGET_BOOT_MOUNT:-/boot}"
  SSHPASS_PASSWORD="${SSHPASS_PASSWORD:-analog}"

  SSH_COMMON_OPTS=(
    -F /dev/null
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -o CheckHostIP=no
    -o PubkeyAuthentication=no
  )

  sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "$TARGET_HOST" \
    "mkdir -p $TARGET_BOOT_MOUNT ; mount /dev/mmcblk0p1 $TARGET_BOOT_MOUNT 2>/dev/null || true"
  sshpass -p"$SSHPASS_PASSWORD" scp "${SSH_COMMON_OPTS[@]}" -O \
    "$DTB_OUTPUT" "$TARGET_HOST:$TARGET_BOOT_MOUNT/devicetree.dtb"
  sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "$TARGET_HOST" \
    "umount $TARGET_BOOT_MOUNT && sync"
  echo "Deployed to $TARGET_HOST:$TARGET_BOOT_MOUNT/devicetree.dtb"
fi
