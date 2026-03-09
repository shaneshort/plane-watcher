#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-arm}"
GOARM="${GOARM:-7}"
GOCACHE="${GOCACHE:-/tmp/plane_watcher_gocache}"
REMOTE_HOST="${REMOTE_HOST:-root@pluto.local}"
REMOTE_DIR="${REMOTE_DIR:-/usr/local/bin/}"
TARGET="${GOOS}-${GOARCH}v${GOARM}"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/bin/$TARGET}"

CMDS=(
  beast-client
  collect-stats
  fifo-monitor
  plane-feeder
  pps-check
  regdump
  regpeek
  sweep-gain
  tune-detector
  watch-stats
)

SCRIPTS=()

mkdir -p "$OUT_DIR"

echo "Building PS tools for ${GOOS}/${GOARCH} (GOARM=${GOARM})"
echo "Output directory: $OUT_DIR"

cd "$SCRIPT_DIR"

for cmd in "${CMDS[@]}"; do
  out="$OUT_DIR/${cmd}"
  echo "  -> $cmd"
  GOOS="$GOOS" GOARCH="$GOARCH" GOARM="$GOARM" GOCACHE="$GOCACHE" \
    go build -o "$out" "./cmd/$cmd"
done

echo "Done."
echo "Built binaries:"
DEPLOY_FILES=()
for cmd in "${CMDS[@]}"; do
  echo "  $OUT_DIR/${cmd}"
  DEPLOY_FILES+=("$OUT_DIR/${cmd}")
done

echo "Deploying helper scripts:"
for script in "${SCRIPTS[@]}"; do
  echo "  $SCRIPT_DIR/${script}"
  DEPLOY_FILES+=("$SCRIPT_DIR/${script}")
done

#echo "Copying to ${REMOTE_HOST}:${REMOTE_DIR}"
sshpass -panalog scp -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oCheckHostIP=no -O "${DEPLOY_FILES[@]}" "${REMOTE_HOST}:${REMOTE_DIR}"
