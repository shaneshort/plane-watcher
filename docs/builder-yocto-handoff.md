# builder — Yocto recipe handoff

Tasks 32–33 of the `builder` implementation plan land files inside the
`firmware/` submodule. The submodule was not initialised during the
automated execution pass (interactive credential prompt). This document
captures the exact steps to land them once submodule auth works.

## Prerequisite

```sh
cd /home/shanes/plane_watcher-builder
git submodule update --init firmware
```

## Step 1 — create the recipe directory and `.bb`

```sh
mkdir -p firmware/petalinux/project-spec/meta-user/recipes-pwtools/plane-watcher-tools
```

Create `firmware/petalinux/project-spec/meta-user/recipes-pwtools/plane-watcher-tools/plane-watcher-tools.bb`:

```bitbake
SUMMARY = "Plane-watcher PS Go tools (slipstreamed from external build)"
DESCRIPTION = "Pre-built ARM Go binaries dropped into ${PWBUILD_STAGING_DIR} \
by the builder tool. See docs/plans/2026-05-14-builder-tui-design.md."
LICENSE = "CLOSED"

PWBUILD_STAGING_DIR ?= "${TOPDIR}/../build/pwbuild-staging"
FILESEXTRAPATHS:prepend := "${PWBUILD_STAGING_DIR}:"

require plane-watcher-tools-binaries.inc

S = "${WORKDIR}"

do_install() {
    install -d ${D}${bindir}
    for b in ${PWTOOLS_BINS}; do
        install -m 0755 ${WORKDIR}/$b ${D}${bindir}/$b
    done
}

FILES:${PN} = "${bindir}/*"
INSANE_SKIP:${PN} = "already-stripped buildpaths"
```

## Step 2 — generate the `.inc`

```sh
go -C ps run ./cmd/builder sync-recipe --write
```

Expected stdout: `wrote: .../plane-watcher-tools-binaries.inc`.

## Step 3 — wire into the image

Edit `firmware/petalinux/project-spec/meta-user/recipes-core/images/petalinux-image-minimal.bbappend`.
Locate the existing `IMAGE_INSTALL:append` line:

```bitbake
IMAGE_INSTALL:append = " gpsd gpsd-conf gpsd-gpsctl gps-utils chrony chronyc pps-tools coreutils vim-xxd"
```

Append `plane-watcher-tools` at the end:

```bitbake
IMAGE_INSTALL:append = " gpsd gpsd-conf gpsd-gpsctl gps-utils chrony chronyc pps-tools coreutils vim-xxd plane-watcher-tools"
```

## Step 4 — verify variable resolution

Inside the firmware Docker container:

```sh
firmware/scripts/run-build.sh bitbake -e plane-watcher-tools \
  | grep -E '^(TOPDIR|PROOT|FILESEXTRAPATHS|SRC_URI|PWBUILD_STAGING_DIR|STAMP)='
```

Confirm:

- `PWBUILD_STAGING_DIR` resolves under `/work/build/pwbuild-staging`.
- `FILESEXTRAPATHS` includes that path.
- `SRC_URI` lists `file://<binary>` entries matching the
  `ps/cmd/build.toml` ARM-targeted commands.

If `STAMP` does not contain `build/tmp/stamps`, update the smoke test in
`docs/plans/2026-05-14-builder-tui-design.md` §9.3 to match the actual
path resolution.

## Step 5 — sigdata smoke test

```sh
# Inside the petalinux project root (firmware/petalinux/), within the
# build container.

bitbake -S none plane-watcher-tools
before=$(find build/tmp/stamps -name '*plane-watcher-tools*do_unpack*.sigdata.*' | sort)

# Touch a staged binary to invalidate.
mkdir -p build/pwbuild-staging
printf 'placeholder' > build/pwbuild-staging/plane-feeder
echo x >> build/pwbuild-staging/plane-feeder
bitbake -S none plane-watcher-tools
after=$(find build/tmp/stamps -name '*plane-watcher-tools*do_unpack*.sigdata.*' | sort)

new=$(comm -23 <(echo "$after") <(echo "$before"))
[ -n "$new" ] || { echo "FAIL: no new sigdata after staged-binary change"; exit 1; }
bitbake-diffsigs $(echo "$before" | tail -1) $(echo "$new" | head -1) | grep -F 'plane-feeder' \
  || { echo "FAIL: diffsigs did not record plane-feeder as input change"; exit 1; }
echo "PASS: staged-binary changes invalidate do_unpack"
```

## Step 6 — commit

```sh
git add firmware/petalinux/project-spec/meta-user/recipes-pwtools \
        firmware/petalinux/project-spec/meta-user/recipes-core/images/petalinux-image-minimal.bbappend
git commit -m "build(yocto): add plane-watcher-tools recipe + IMAGE_INSTALL"
```

## Step 7 — gate Wave 10

The real-hardware smoke test in `docs/plans/2026-05-14-builder-tui-design.md`
§9.3 (Task 35) must pass before deleting `tools/rebuild.sh`,
`ps/build-arm-tools.sh`, `ps/build-tools.sh` (Task 36).
