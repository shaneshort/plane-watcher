#!/bin/bash
# EDF/Yocto build wrapper for plane_watcher. Replaces tools/build-petalinux.sh
# in the post-Phase-D path: stages the Vivado XSA, regenerates the System
# Device Tree via xsct, optionally regenerates the meta-plane-watcher machine
# conf via gen-machine-conf, runs bitbake against plane-watcher-image, packs
# the kernel + DTB + initrd into image.ub via mkimage, and stages BOOT.BIN /
# image.ub / boot.scr into build/smartzynq/deploy/.
#
# The EDF workspace lives outside the repo by default (it's large, shared
# across checkouts, and bootstrapped once via `repo init && repo sync` per
# docs/migration/phase-b/README.md). The wrapper does not bootstrap the
# workspace; it expects EDF_WORKSPACE to point at an existing tree.
#
# Environment overrides:
#   XSA_PATH               XSA from the Vivado build (default: Phase 1 output)
#   FIRMWARE_DIR           Firmware submodule root (default: firmware/)
#   SDT_DIR                Where to stage the SDT (default: $FIRMWARE_DIR/petalinux/hardware/sdt)
#   EDF_WORKSPACE          EDF/yocto-manifests workspace (default: $HOME/edf-workspaces/plane-watcher)
#   EDF_BUILD_DIR          BitBake build dir under the workspace (default: $EDF_WORKSPACE/build)
#   MACHINE                Target MACHINE (default: plane-watcher-zynq7)
#   IMAGE                  Target image recipe (default: plane-watcher-image)
#   DEPLOY_DIR             Output staging (default: build/smartzynq/deploy)
#   VITIS_DIR              Vitis install root (default: /opt/amd/2025.2/Vitis)
#   SKIP_SDTGEN=1          Reuse an existing SDT under $SDT_DIR
#   SKIP_BUILD=1           Skip the bitbake step (XSA/SDT staging only)
#   SKIP_PACK=1            Skip the mkimage image.ub pack
#   SKIP_STAGE=1           Skip copying artefacts into $DEPLOY_DIR
#   REGEN_MACHINE_CONF=1   Force gen-machine-conf re-run (overwrites the
#                          checked-in layer files; review the diff manually).
#   CLEANSSTATE_FSBL=1     Run `bitbake -c cleansstate` on the FSBL multiconfig
#                          and exit. Used by the yocto-fsbl-clean step to
#                          invalidate stale FSBL sstate after a bitstream change
#                          (defensive port of the petalinux-fsbl-clean step;
#                          remove if it never fires in practice under EDF).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/plane_watcher.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

XSA_PATH="${XSA_PATH:-$REPO_ROOT/build/smartzynq/vivado/plane_watcher_phase1.xsa}"
FIRMWARE_DIR="${FIRMWARE_DIR:-$REPO_ROOT/firmware}"
SDT_DIR="${SDT_DIR:-$FIRMWARE_DIR/petalinux/hardware/sdt}"
EDF_WORKSPACE="${EDF_WORKSPACE:-$HOME/edf-workspaces/plane-watcher}"
EDF_BUILD_DIR="${EDF_BUILD_DIR:-$EDF_WORKSPACE/build}"
MACHINE="${MACHINE:-plane-watcher-zynq7}"
IMAGE="${IMAGE:-plane-watcher-image}"
DEPLOY_DIR="${DEPLOY_DIR:-$REPO_ROOT/build/smartzynq/deploy}"
VITIS_DIR="${VITIS_DIR:-/opt/amd/2025.2/Vitis}"
SKIP_SDTGEN="${SKIP_SDTGEN:-0}"
SKIP_BUILD="${SKIP_BUILD:-0}"
SKIP_PACK="${SKIP_PACK:-0}"
SKIP_STAGE="${SKIP_STAGE:-0}"
REGEN_MACHINE_CONF="${REGEN_MACHINE_CONF:-0}"
CLEANSSTATE_FSBL="${CLEANSSTATE_FSBL:-0}"

LAYER_DIR="$FIRMWARE_DIR/yocto/meta-plane-watcher"
MACHINE_CONF="$LAYER_DIR/conf/machine/$MACHINE.conf"

if [[ ! -f "$XSA_PATH" ]]; then
  echo "error: XSA not found: $XSA_PATH" >&2
  echo "       run tools/build-smartzynq-phase1.sh first" >&2
  exit 1
fi

if [[ ! -d "$EDF_WORKSPACE/sources/meta-amd-edf" ]]; then
  echo "error: EDF workspace not found at $EDF_WORKSPACE" >&2
  echo "       expected sources/meta-amd-edf to exist (set up via repo init + repo sync)" >&2
  echo "       see docs/migration/phase-b/README.md for the one-time setup" >&2
  exit 1
fi

if [[ ! -d "$LAYER_DIR" ]]; then
  echo "error: meta-plane-watcher layer not found at $LAYER_DIR" >&2
  exit 1
fi

# Stage the XSA into hardware/. The path lives under firmware/petalinux/ for
# now because xsct sdtgen scratch lives there alongside the SDT output; this
# moves under firmware/hardware/ once Phase G drops the petalinux/ tree
# entirely.
staged_xsa="$(dirname "$SDT_DIR")/plane_watcher.xsa"
mkdir -p "$(dirname "$staged_xsa")"
if [[ "$XSA_PATH" -ef "$staged_xsa" ]]; then
  echo "==> XSA already staged: $staged_xsa"
else
  cp -f "$XSA_PATH" "$staged_xsa"
  echo "==> Staged XSA: $staged_xsa"
fi

# Generate the System Device Tree from the XSA via xsct (Vitis ships it).
if [[ "$SKIP_SDTGEN" != "1" ]]; then
  if [[ ! -f "$VITIS_DIR/settings64.sh" ]]; then
    echo "error: Vitis settings not found: $VITIS_DIR/settings64.sh" >&2
    echo "       set VITIS_DIR or install Vitis 2025.2 (xsct ships with Vitis)" >&2
    exit 1
  fi
  echo "==> Generating SDT via xsct ($SDT_DIR)"
  rm -rf "$SDT_DIR"
  mkdir -p "$SDT_DIR"
  (
    # Vitis settings64.sh references PYTHONPATH and other env vars that may
    # not be set in a clean shell; disable nounset around the source.
    set +u
    # shellcheck disable=SC1091
    source "$VITIS_DIR/settings64.sh"
    set -u
    xsct -eval "
      sdtgen set_dt_param -dir {$SDT_DIR} -xsa {$staged_xsa}
      sdtgen generate_sdt
    "
  )
  if [[ ! -f "$SDT_DIR/system-top.dts" ]]; then
    echo "error: sdtgen did not produce system-top.dts in $SDT_DIR" >&2
    exit 1
  fi
fi

# Check whether the SDT is newer than the checked-in machine conf. The machine
# conf is hand-normalised on top of gen-machine-conf output (PLANE_WATCHER_SDT_DIR,
# fpga-overlay, etc.) so we don't auto-overwrite it; instead warn and require
# REGEN_MACHINE_CONF=1 to opt in.
if [[ -f "$MACHINE_CONF" && -f "$SDT_DIR/system-top.dts" ]]; then
  if [[ "$SDT_DIR/system-top.dts" -nt "$MACHINE_CONF" ]]; then
    if [[ "$REGEN_MACHINE_CONF" == "1" ]]; then
      echo "==> SDT newer than $MACHINE.conf — re-running gen-machine-conf"
      tmpout="$(mktemp -d)"
      trap '[[ -n "${tmpout:-}" ]] && rm -rf "$tmpout"' EXIT
      (
        cd "$EDF_WORKSPACE"
        # shellcheck disable=SC1091
        set build
        # edf-init-build-env references ZSH_NAME/etc; tolerate nounset.
        set +u
        # shellcheck disable=SC1091
        . ./edf-init-build-env build >/dev/null
        set -u
        gen-machine-conf \
          --hw-description "$SDT_DIR" \
          --soc-family zynq \
          --machine-name "$MACHINE" \
          --output "$EDF_WORKSPACE/phase-c-gen" \
          parse-sdt
      )
      # Copy generated machine conf + multiconfig back into the layer.
      cp "$EDF_BUILD_DIR/conf/machine/$MACHINE.conf" "$MACHINE_CONF.regen"
      cp "$EDF_BUILD_DIR/conf/machine/include/$MACHINE/"*.conf "$LAYER_DIR/conf/machine/include/$MACHINE/"
      cp "$EDF_BUILD_DIR/conf/multiconfig/$MACHINE-cortexa9-fsbl.conf" "$LAYER_DIR/conf/multiconfig/"
      echo "==> Wrote $MACHINE_CONF.regen — diff against $MACHINE_CONF and re-apply" \
           "manual normalisations (PLANE_WATCHER_SDT_DIR, MACHINE_FEATURES, comments)" \
           "before committing." >&2
    else
      echo "warning: SDT is newer than $MACHINE.conf" >&2
      echo "         $MACHINE.conf carries hand-applied normalisations on top of" >&2
      echo "         gen-machine-conf output (PLANE_WATCHER_SDT_DIR, fpga-overlay)." >&2
      echo "         Re-run with REGEN_MACHINE_CONF=1 to regenerate; review the" >&2
      echo "         diff and re-apply normalisations manually." >&2
    fi
  fi
fi

if [[ "$CLEANSSTATE_FSBL" == "1" ]]; then
  echo "==> bitbake -c cleansstate mc:$MACHINE-cortexa9-fsbl:fsbl-firmware"
  (
    cd "$EDF_WORKSPACE"
    set build
    # edf-init-build-env references ZSH_NAME/etc; tolerate nounset.
    set +u
    # shellcheck disable=SC1091
    . ./edf-init-build-env build >/dev/null
    set -u
    if ! grep -q "$LAYER_DIR" "$EDF_BUILD_DIR/conf/bblayers.conf"; then
      bitbake-layers add-layer "$LAYER_DIR"
    fi
    MACHINE="$MACHINE" bitbake -c cleansstate "mc:$MACHINE-cortexa9-fsbl:fsbl-firmware"
  )
  exit 0
fi

if [[ "$SKIP_BUILD" != "1" ]]; then
  echo "==> bitbake $IMAGE (MACHINE=$MACHINE)"
  (
    cd "$EDF_WORKSPACE"
    set build
    # edf-init-build-env references ZSH_NAME/etc; tolerate nounset.
    set +u
    # shellcheck disable=SC1091
    . ./edf-init-build-env build >/dev/null
    set -u
    # Ensure the layer is in bblayers. bitbake-layers add-layer is idempotent
    # in practice (it greps before appending), so unconditional invocation
    # is safe on rebuilds.
    if ! grep -q "$LAYER_DIR" "$EDF_BUILD_DIR/conf/bblayers.conf"; then
      bitbake-layers add-layer "$LAYER_DIR"
    fi
    MACHINE="$MACHINE" bitbake "$IMAGE"
  )
fi

# Pack image.ub: kernel + DTB + raw rootfs.cpio.gz into a FIT that the
# existing PetaLinux-flow boot.scr loads via bootm at 0x10100000. The raw
# .cpio.gz is intentional — the FIT type=ramdisk attribute tells U-Boot what
# it is; the .cpio.gz.u-boot variant has an extra mkimage header that breaks
# the kernel's initrd unpacker (see docs/migration/phase-b/README.md).
deploy_images="$EDF_BUILD_DIR/tmp/deploy/images/$MACHINE"
if [[ "$SKIP_PACK" != "1" ]]; then
  zimage="$deploy_images/zImage"
  dtb="$deploy_images/devicetree/cortexa9-linux.dtb"
  rootfs="$(ls "$deploy_images/$IMAGE-$MACHINE.rootfs-"*.cpio.gz 2>/dev/null | grep -v '\.u-boot$' | head -1)"
  if [[ -z "$rootfs" ]]; then
    echo "error: no rootfs cpio.gz under $deploy_images" >&2
    exit 1
  fi
  for f in "$zimage" "$dtb"; do
    [[ -f "$f" ]] || { echo "error: missing $f" >&2; exit 1; }
  done

  its_template="$REPO_ROOT/docs/migration/phase-b/image.its"
  its="$(mktemp -t plane-watcher-image-XXXXXX.its)"
  trap '[[ -n "${its:-}" ]] && rm -f "$its"' EXIT
  sed -e "s|@ZIMAGE@|$zimage|" \
      -e "s|@DTB@|$dtb|" \
      -e "s|@ROOTFS@|$rootfs|" \
      "$its_template" > "$its"

  out="$deploy_images/$IMAGE-$MACHINE.image.ub"
  echo "==> mkimage -> $out"
  mkimage -f "$its" "$out" >/dev/null
fi

# Stage the deploy artefacts. Mirror the existing build/smartzynq/deploy/
# convention so downstream SD-write / SSH-push tooling sees the same shape
# the PetaLinux flow produced.
if [[ "$SKIP_STAGE" != "1" ]]; then
  mkdir -p "$DEPLOY_DIR"
  for f in BOOT.bin image.ub boot.scr; do
    src=""
    case "$f" in
      BOOT.bin)
        src="$(ls "$deploy_images/BOOT-$MACHINE-"*.bin 2>/dev/null | head -1)"
        dst="$DEPLOY_DIR/BOOT.BIN"
        ;;
      image.ub)
        src="$deploy_images/$IMAGE-$MACHINE.image.ub"
        dst="$DEPLOY_DIR/image.ub"
        ;;
      boot.scr)
        src="$deploy_images/boot.scr"
        dst="$DEPLOY_DIR/boot.scr"
        ;;
    esac
    if [[ -n "$src" && -f "$src" ]]; then
      cp -f "$src" "$dst"
      echo "==> staged: $dst"
    else
      echo "warning: $f not produced (looked at: $src)" >&2
    fi
  done
fi

# Deliberately do not stage a uboot.env / uboot-redund.env: U-Boot's env is
# board-owned state, not a build artefact. First-time SD prep: break into
# U-Boot once and run `saveenv; saveenv` to capture the compiled defaults
# into both redundant files; from then on env lives on the SD and is owned
# by whoever is at the U-Boot prompt. Shipping an env blob from the build
# would clobber that on every rebuild. The "No Valid Environment Area found"
# warning on a fresh card is cosmetic — boot proceeds via compiled defaults.
# Yocto still emits u-boot-xlnx-initial-env.bin under the EDF deploy dir if
# anyone wants to seed a fresh SD from the compiled env.

cat <<EOF

Artefacts staged in: $DEPLOY_DIR
  BOOT.BIN  -> EDF-built FSBL + U-Boot + bitstream (Phase F flashes this to QSPI)
  image.ub  -> FIT: kernel + DTB + initrd; copy to FAT32 partition on SD
  boot.scr  -> EDF-built U-Boot boot script

First SD prep: at the U-Boot prompt, run \`saveenv; saveenv\` to populate
uboot.env + uboot-redund.env on the FAT partition. After that, U-Boot's
env is board-owned state and untouched by future builds.

Phase B's PetaLinux BOOT.BIN is still the QSPI baseline until Phase F.
DIP switches: QSPI boot (S1=off, S2=on).
EOF
