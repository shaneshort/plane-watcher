#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VIVADO_DIR="$REPO_ROOT/hdl/vivado"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"
export REPO_ROOT

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

VIVADO_SETTINGS="${VIVADO_SETTINGS:-/opt/vivado/2025.2/Vivado/settings64.sh}"
VENDOR_HDL_ROOT="${VENDOR_HDL_ROOT:-$HOME/Downloads/sdr/Fish-Wan-plutosdr-fw-7020-SDR}"
BUILD_TARGET="${BUILD_TARGET:-stock-tap-build}"
RUN_DEPLOY="${RUN_DEPLOY:-0}"
DEEP_DEBUG="${DEEP_DEBUG:-0}"

usage() {
  cat <<EOF
Usage:
  $(basename "$0") [--deploy] [--skip-deploy] [--deep-debug|--no-deep-debug] [--target stock-tap-build|vendor-build]

Options:
  --deploy        Run deploy-bitstream.sh after a successful timing-clean build
  --skip-deploy   Build only; do not deploy (default)
  --deep-debug    Build with CONFIG.ENABLE_DEEP_DEBUG enabled
  --no-deep-debug Build with CONFIG.ENABLE_DEEP_DEBUG disabled
  --target NAME   Override BUILD_TARGET
  -h, --help      Show this help

Config:
  By default the script sources $CONFIG_FILE if it exists.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --deploy)
      RUN_DEPLOY=1
      ;;
    --skip-deploy)
      RUN_DEPLOY=0
      ;;
    --deep-debug)
      DEEP_DEBUG=1
      ;;
    --no-deep-debug)
      DEEP_DEBUG=0
      ;;
    --target)
      shift
      BUILD_TARGET="${1:-}"
      if [[ -z "$BUILD_TARGET" ]]; then
        echo "error: --target requires a value" >&2
        exit 1
      fi
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

if [[ ! -d "$VENDOR_HDL_ROOT" ]]; then
  echo "error: vendor HDL root not found: $VENDOR_HDL_ROOT" >&2
  exit 1
fi

case "$BUILD_TARGET" in
  stock-tap-build|vendor-build)
    ;;
  *)
    echo "error: unsupported BUILD_TARGET=$BUILD_TARGET (expected stock-tap-build or vendor-build)" >&2
    exit 1
    ;;
esac

source "$VIVADO_SETTINGS"

git_hex="$(git -C "$REPO_ROOT" rev-parse --short=4 HEAD)"
build_id=$(( 16#$git_hex & 0x7fff ))
if [[ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]]; then
  build_id=$(( build_id | 0x8000 ))
fi
printf 'Using BUILD_ID=0x%04X\n' "$build_id"
printf 'Using DEEP_DEBUG=%s\n' "$DEEP_DEBUG"

pushd "$VIVADO_DIR" >/dev/null

make "$BUILD_TARGET" \
  VENDOR_HDL_ROOT="$VENDOR_HDL_ROOT" \
  BUILD_ID="$build_id" \
  DEEP_DEBUG="$DEEP_DEBUG" \
  2>&1 | tee "${BUILD_TARGET}.log"

routed_timing_rpt="$VIVADO_DIR/build/plane_watcher/plane_watcher.runs/impl_1/system_top_timing_summary_routed.rpt"
post_override_timing_rpt="$VIVADO_DIR/build/plane_watcher/impl_timing_summary_post_override.rpt"

if [[ ! -f "$routed_timing_rpt" ]]; then
  echo "ERROR: routed timing summary not found: $routed_timing_rpt" >&2
  exit 1
fi

if grep -q "Timing constraints are not met." "$routed_timing_rpt"; then
  echo "ERROR: timing constraints are not met. Refusing to generate/deploy bitstream." >&2
  awk '
    /Design Timing Summary/ {show=1}
    show {print}
    /Clock Summary/ && show {exit}
  ' "$routed_timing_rpt" >&2
  exit 1
fi

if [[ -f "$post_override_timing_rpt" ]]; then
  routed_failed=0
  post_failed=0
  grep -q "Timing constraints are not met." "$routed_timing_rpt" && routed_failed=1
  grep -q "Timing constraints are not met." "$post_override_timing_rpt" && post_failed=1
  if [[ "$routed_failed" != "$post_failed" ]]; then
    echo "WARNING: routed timing report and post-override timing report disagree." >&2
    echo "         routed report is authoritative for build gating." >&2
  fi
fi

popd >/dev/null

if [[ "$RUN_DEPLOY" == "1" ]]; then
  "$SCRIPT_DIR/deploy-bitstream.sh"
fi
