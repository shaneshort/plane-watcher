#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$SCRIPT_DIR/work"
WAVE_DIR="$SCRIPT_DIR/waves"

VECTOR_FILE="${1:-vectors/live_pluto.dat}"
WAVE_FILE="${2:-waves/live_pluto.ghw}"
LOG_FILE="${3:-/tmp/live_pluto_sim.log}"

mkdir -p "$WORK_DIR" "$WAVE_DIR"

cd "$SCRIPT_DIR"

rm -f "$WORK_DIR"/work-obj08.cf

ghdl -a --std=08 --workdir="$WORK_DIR" \
  ../rtl/adsb_pkg.vhd \
  ../rtl/timestamp_counter.vhd \
  ../rtl/adsb_crc.vhd \
  ../rtl/smallest_bsds.vhd \
  ../rtl/bit_flipper.vhd \
  ../rtl/bsd_calculator.vhd \
  ../rtl/adsb_edge_detector.vhd \
  ../rtl/preamble_detector.vhd \
  ../rtl/message_decoder.vhd \
  ../rtl/message_aggregator.vhd \
  ../rtl/adsb_decoder.vhd \
  ../tb/adsb_decoder_tb.vhd

ghdl -e --std=08 --workdir="$WORK_DIR" adsb_decoder_tb

ghdl -r --std=08 --workdir="$WORK_DIR" adsb_decoder_tb \
  -gVECTOR_FILE="$VECTOR_FILE" \
  --wave="$WAVE_FILE" \
  2>&1 | tee "$LOG_FILE"

echo
echo "Log:   $LOG_FILE"
echo "Wave:  $WAVE_FILE"
echo "Key lines:"
grep -n "DECODED MESSAGE\\|FLIP_DBG\\|SOM_DBG\\|Invalid DF\\|All 32 iterations failed" "$LOG_FILE" | head -300 || true
