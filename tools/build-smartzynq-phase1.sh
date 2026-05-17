#!/bin/bash
# Build the Phase 1 Smart ZYNQ SL bitstream (empty PL + AXI-GPIO) and export XSA.
#
# See docs/plans/2026-04-16-smart-zynq-sl-port-plan.md Phase 1 for context.
# Output: build/smartzynq/vivado/plane_watcher_phase1.{bit,xsa}

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

VIVADO_SETTINGS="${VIVADO_SETTINGS:-/opt/vivado/2025.2/Vivado/settings64.sh}"
PROJECT_DIR="${PROJECT_DIR:-$REPO_ROOT/build/smartzynq/vivado}"
BASE_ADDR="${BASE_ADDR:-0x43C00000}"
JOBS="${JOBS:-$(nproc)}"
# ADC encode clock (MHz). Must match logdet_pkg.ENCODE_CLK_HZ. Conservative
# default for prototype bring-up; raise toward 40.0 after signal integrity and
# decode timing are validated on the AD9203 board.
ENCODE_MHZ="${ENCODE_MHZ:-16.000}"

if [[ ! -f "$VIVADO_SETTINGS" ]]; then
  echo "error: Vivado settings script not found: $VIVADO_SETTINGS" >&2
  exit 1
fi

# shellcheck disable=SC1090
source "$VIVADO_SETTINGS"

rm -rf "$PROJECT_DIR"
mkdir -p "$PROJECT_DIR"

pushd "$REPO_ROOT/hdl/vivado" >/dev/null

vivado -mode batch \
  -source build_smartzynq_phase1.tcl \
  -tclargs "$PROJECT_DIR" "$BASE_ADDR" "$JOBS" "$ENCODE_MHZ" \
  -log "$PROJECT_DIR/vivado.log" \
  -journal "$PROJECT_DIR/vivado.jou" \
  2>&1 | tee "$PROJECT_DIR/build.log"

popd >/dev/null

xsa_path="$PROJECT_DIR/plane_watcher_phase1.xsa"
if [[ ! -f "$xsa_path" ]]; then
  echo "ERROR: expected XSA not found: $xsa_path" >&2
  exit 1
fi

echo "Phase 1 bitstream + XSA ready:"
echo "  XSA: $xsa_path"
echo "  Feed to Petalinux with: tools/build-petalinux.sh --xsa $xsa_path"
