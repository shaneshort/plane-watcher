#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
GOARM="${GOARM:-}"
GOCACHE="${GOCACHE:-/tmp/plane_watcher_gocache}"

TARGET="${GOOS}-${GOARCH}"
if [[ -n "$GOARM" ]]; then
  TARGET="${TARGET}v${GOARM}"
fi
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/bin/$TARGET}"

CMDS=(
  beast-client
  collect-stats
  fifo-monitor
  plane-feeder
  pps-check
  regdump
  regpeek
  replay
  sweep-gain
  tune-detector
  watch-stats
)

mkdir -p "$OUT_DIR"

echo "Building PS tools for ${TARGET}"
echo "Output directory: $OUT_DIR"

cd "$SCRIPT_DIR"

for cmd in "${CMDS[@]}"; do
  out="$OUT_DIR/${cmd}"
  echo "  -> $cmd"
  GOOS="$GOOS" GOARCH="$GOARCH" GOARM="$GOARM" GOCACHE="$GOCACHE" \
    go build -o "$out" "./cmd/$cmd"
done

echo "Done."
