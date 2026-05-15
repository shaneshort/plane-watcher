#!/bin/bash
export PATH=$PATH:/usr/local/go/bin
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$REPO_ROOT/tools/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-arm}"
GOARM="${GOARM:-7}"
GOCACHE="${GOCACHE:-/tmp/plane_watcher_gocache}"
TARGET_HOST="${TARGET_HOST:-root@planewatcher.local}"
SSHPASS_PASSWORD="${SSHPASS_PASSWORD:-planewatcher}"
REMOTE_HOST="${REMOTE_HOST:-$TARGET_HOST}"
REMOTE_DIR="${REMOTE_DIR:-/usr/local/bin/}"
TARGET="${GOOS}-${GOARCH}v${GOARM}"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/bin/$TARGET}"
# DEPLOY=0 to skip the scp deploy step (useful for local/CI builds
# where the remote host is unreachable). Defaults to 1.
DEPLOY="${DEPLOY:-1}"

#CMDS=(
#  beast-client
#  collect-stats
#  fifo-monitor
#  plane-feeder
#  pps-check
#  regdump
#  regpeek
#  watch-stats
#  ubx
#)
CMDS=(
        plane-feeder
	dump-capture
	regpeek
	regdump
	sweep-align
	tune-detector
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
  if ! command -v sshpass >/dev/null; then
    echo "error: sshpass not installed (required for password-based SSH deploy)" >&2
    exit 1
  fi

  SSH_COMMON_OPTS=(
    -F /dev/null
    -o StrictHostKeyChecking=no
    -o UserKnownHostsFile=/dev/null
    -o CheckHostIP=no
    -o PubkeyAuthentication=no
  )

  # SCP to /tmp then atomic mv to avoid ETXTBSY on running binaries.
  echo "Staging to ${REMOTE_HOST}:/tmp/"
  sshpass -p"$SSHPASS_PASSWORD" scp "${SSH_COMMON_OPTS[@]}" -O \
    "${DEPLOY_FILES[@]}" "${REMOTE_HOST}:/tmp/"
  #echo "Moving into ${REMOTE_DIR}"
  #sshpass -p"$SSHPASS_PASSWORD" ssh "${SSH_COMMON_OPTS[@]}" "${REMOTE_HOST}" \
  #  "for f in ${CMDS[*]}; do mv -f /tmp/\$f ${REMOTE_DIR}; done"
     # && /etc/init.d/S99plane-feeder restart"
else
  echo "DEPLOY=0 set — skipping scp to ${REMOTE_HOST}"
fi
