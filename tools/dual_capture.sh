#!/bin/bash
# Simultaneous beast + raw IQ capture for decode comparison.
#
# Usage: ./tools/dual_capture.sh [gain_db] [seconds]
#   gain_db  — AD9363 manual gain (default: 24)
#   seconds  — capture duration (default: 10)
#
# Outputs (in /tmp/):
#   dual_g${GAIN}_beast.bin   — raw beast binary stream
#   dual_g${GAIN}_iq.raw      — raw IQ capture (I16/Q16 interleaved)
#   dual_g${GAIN}_iq.json     — capture stats

set -euo pipefail

GAIN="${1:-24}"
SECONDS_DUR="${2:-10}"
PREFIX="/tmp/dual_g${GAIN}"

BEAST_OUT="${PREFIX}_beast.bin"
IQ_OUT="${PREFIX}_iq.raw"
STATS_OUT="${PREFIX}_iq.json"

echo "=== Dual capture: gain=${GAIN} dB, duration=${SECONDS_DUR} s ==="
echo "  Beast → ${BEAST_OUT}"
echo "  IQ    → ${IQ_OUT}"
echo "  Stats → ${STATS_OUT}"

# Start beast capture in background — timeout kills it after duration + 2s grace.
timeout "$((SECONDS_DUR + 2))" nc pluto.local 30005 > "${BEAST_OUT}" &
BEAST_PID=$!

# Brief settle so the beast connection is established before IQ starts.
sleep 0.3

# Run IQ capture (foreground, blocks for the duration).
uv run --with pyadi-iio python tools/pluto_capture.py \
    --uri ip:pluto.local \
    --gain-mode manual \
    --gain-db "${GAIN}" \
    --seconds "${SECONDS_DUR}" \
    --output "${IQ_OUT}" \
    --stats-out "${STATS_OUT}"

# Wait for beast capture to finish (timeout will kill nc).
wait "${BEAST_PID}" 2>/dev/null || true

BEAST_SIZE=$(stat -c%s "${BEAST_OUT}" 2>/dev/null || echo 0)
echo ""
echo "=== Done ==="
echo "  Beast: ${BEAST_SIZE} bytes in ${BEAST_OUT}"
echo "  IQ:    $(stat -c%s "${IQ_OUT}") bytes in ${IQ_OUT}"
