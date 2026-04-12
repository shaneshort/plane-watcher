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
# DEPLOY=0 to skip the scp deploy step (useful for local/CI builds
# where the remote host is unreachable). Defaults to 1.
DEPLOY="${DEPLOY:-1}"

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
  ubx
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

# Use the "${arr[@]+...}" conditional-expansion idiom so an empty SCRIPTS
# array doesn't trip set -u on bash 3.2 (which macOS ships). The newer
# bash 4.4+ accepts a bare "${SCRIPTS[@]}" on an empty declared array,
# but bash 3.2 treats it as an unbound reference and aborts.
if [[ ${#SCRIPTS[@]} -gt 0 ]]; then
  echo "Deploying helper scripts:"
  for script in ${SCRIPTS[@]+"${SCRIPTS[@]}"}; do
    echo "  $SCRIPT_DIR/${script}"
    DEPLOY_FILES+=("$SCRIPT_DIR/${script}")
  done
fi

if [[ "$DEPLOY" == "1" ]]; then
  echo "Stopping plane-feeder"
  sshpass -panalog ssh ${REMOTE_HOST} "/etc/init.d/S99plane-feeder stop"
  echo "Copying to ${REMOTE_HOST}:${REMOTE_DIR}"
  sshpass -panalog scp -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oCheckHostIP=no -O "${DEPLOY_FILES[@]}" "${REMOTE_HOST}:${REMOTE_DIR}"
  echo "Starting plane-feeder"
  sshpass -panalog ssh ${REMOTE_HOST} "/etc/init.d/S99plane-feeder start"
else
  echo "DEPLOY=0 set — skipping scp to ${REMOTE_HOST}"
fi
