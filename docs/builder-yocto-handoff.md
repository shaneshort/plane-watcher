# builder — Yocto recipe handoff

Superseded by Phase D of the PetaLinux → EDF migration (see `MIGRATION.md`).

The handoff this doc described (tasks 32–33 of the builder TUI plan: land
the `plane-watcher-tools` recipe, wire it into the image, verify
`PWBUILD_STAGING_DIR` resolution) has been completed against the EDF
layer at `firmware/yocto/meta-plane-watcher/recipes-pwtools/plane-watcher-tools/`,
not the old `firmware/petalinux/project-spec/meta-user/recipes-pwtools/`
path.

Current state:

- Recipe: `firmware/yocto/meta-plane-watcher/recipes-pwtools/plane-watcher-tools/plane-watcher-tools.bb`
- Generated include: `…/plane-watcher-tools-binaries.inc`
  (regenerated via `go -C ps run ./cmd/builder sync-recipe --write`)
- Image wiring: `IMAGE_INSTALL:append` in
  `firmware/yocto/meta-plane-watcher/recipes-core/images/plane-watcher-image.bb`
- Staging dir default: `${LAYERDIR}/../../build/pwbuild-staging`, set in
  `firmware/yocto/meta-plane-watcher/conf/layer.conf` and matched by the
  Go builder's `config.LoadAll`.

For the original design see `docs/plans/2026-05-14-builder-tui-design.md`.
For the migration plan see `MIGRATION.md` in the repo root.
