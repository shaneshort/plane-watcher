#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"
export REPO_ROOT

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

VIVADO_SETTINGS="${VIVADO_SETTINGS:-/opt/vivado/2025.2/Vivado/settings64.sh}"
IMPL_DIR="${IMPL_DIR:-$REPO_ROOT/hdl/vivado/build/plane_watcher/plane_watcher.runs/impl_1}"
TARGET_HOST="${TARGET_HOST:-root@pluto.local}"
TARGET_BIT_PATH="${TARGET_BIT_PATH:-/boot/system.bit.bin}"
TARGET_BOOT_MOUNT="${TARGET_BOOT_MOUNT:-/boot}"
SSHPASS_PASSWORD="${SSHPASS_PASSWORD:-analog}"
GENERATE_ONLY="${GENERATE_ONLY:-0}"

usage() {
  cat <<EOF
Usage:
  $(basename "$0") [--generate-only]

Options:
  --generate-only  Only create system_top.bit.bin locally; do not push it anywhere
  -h, --help       Show this help

Config:
  By default the script sources $CONFIG_FILE if it exists.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --generate-only)
      GENERATE_ONLY=1
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

if [[ ! -f "$VIVADO_SETTINGS" ]]; then
  echo "error: Vivado settings script not found: $VIVADO_SETTINGS" >&2
  exit 1
fi

if [[ ! -d "$IMPL_DIR" ]]; then
  echo "error: implementation directory not found: $IMPL_DIR" >&2
  exit 1
fi

source "$VIVADO_SETTINGS"

pushd "$IMPL_DIR" >/dev/null

cat > system_top.bif <<'EOF'
all:
{
  system_top.bit
}
EOF

bootgen -image system_top.bif -arch zynq -process_bitstream bin -w -o system_top.bit.bin
md5sum system_top.bit.bin

if [[ "$GENERATE_ONLY" == "1" ]]; then
  echo "Generated: $IMPL_DIR/system_top.bit.bin"
  popd >/dev/null
  exit 0
fi

SSH_COMMON_OPTS=(
  -F /dev/null
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o CheckHostIP=no
  -o PubkeyAuthentication=no
)

sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "$TARGET_HOST" "mkdir -p $TARGET_BOOT_MOUNT ; mount /dev/mmcblk0p1 $TARGET_BOOT_MOUNT"
sshpass -p"$SSHPASS_PASSWORD" scp "${SSH_COMMON_OPTS[@]}" -O system_top.bit.bin "$TARGET_HOST:$TARGET_BIT_PATH"
sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "$TARGET_HOST" "umount $TARGET_BOOT_MOUNT && sync"

popd >/dev/null
