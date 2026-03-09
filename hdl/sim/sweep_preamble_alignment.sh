#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$SCRIPT_DIR/work"
WAVE_DIR="$SCRIPT_DIR/waves"

VECTOR_FILE="${1:-vectors/single_clean.dat}"
TAP_START="${2:-60}"
TAP_END="${3:-90}"
DELAY_START="${4:-0}"
DELAY_END="${5:-64}"

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

printf "vector=%s tap=%s..%s delay=%s..%s\n" \
  "$VECTOR_FILE" "$TAP_START" "$TAP_END" "$DELAY_START" "$DELAY_END"

for tap in $(seq "$TAP_START" "$TAP_END"); do
  for delay in $(seq "$DELAY_START" "$DELAY_END"); do
    log="/tmp/preamble_sweep_t${tap}_d${delay}.log"
    ghdl -r --std=08 --workdir="$WORK_DIR" adsb_decoder_tb \
      -gVECTOR_FILE="$VECTOR_FILE" \
      -gPREAMBLE_OUTPUT_TAP="$tap" \
      -gPREAMBLE_MESSAGE_DELAY="$delay" \
      >"$log" 2>&1

    bits="$(grep 'SMALL_DBG:' "$log" | head -8 | sed -E "s/.*bhd='([01])'.*/\\1/" | tr -d '\n')"
    df_line="$(grep 'FLIP_DBG: BSDs done' "$log" | head -1 || true)"
    decoded_line="$(grep 'DECODED MESSAGE' "$log" | head -1 || true)"

    if [[ -n "$bits" ]]; then
      first_byte=$((2#${bits}))
      first_hex=$(printf "%02X" "$first_byte")
    else
      first_hex="--"
    fi

    if [[ -n "$decoded_line" || "$first_hex" == "8D" ]]; then
      printf "HIT tap=%d delay=%d first=%s decoded=%s\n" \
        "$tap" "$delay" "$first_hex" "${decoded_line:-no}"
    else
      printf "tap=%d delay=%d first=%s %s\n" \
        "$tap" "$delay" "$first_hex" "${df_line#*DF=}" 
    fi
  done
done
