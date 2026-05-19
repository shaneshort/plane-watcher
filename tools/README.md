# Tools

`tools/` contains utility scripts for three separate jobs:

- firmware build/deploy wrappers
- simulation vector generation and log analysis
- RF capture and inspection helpers

## `builder` (consolidated build TUI/CLI)

`ps/cmd/builder/` is the consolidated front-end for the Vivado →
Yocto/EDF → deploy chain *and* PS Go-tool cross-build/deploy. It
supersedes `rebuild.sh`, `ps/build-tools.sh`, and `ps/build-arm-tools.sh`
(legacy scripts retained until the Tier 2 hardware soak passes).

```sh
# Interactive TUI:
go -C ps run ./cmd/builder

# Equivalent CLI invocations:
go -C ps run ./cmd/builder --bitstream --yocto --deploy=ssh
go -C ps run ./cmd/builder --bitstream                       # repackage BOOT.BIN only
go -C ps run ./cmd/builder --ps-hotswap --no-restart         # iterate Go tools fast
go -C ps run ./cmd/builder --ps-slipstream --yocto --deploy=sd
go -C ps run ./cmd/builder sync-recipe [--write]             # keep Yocto .inc in sync
```

The leaf scripts (`build-smartzynq-phase1.sh`, `build-yocto.sh`) are
invoked by `builder`; they remain documented as primitives, not as
user-facing entry points. Settings are read from `tools/plane_watcher.env`
(unchanged); `builder` policy lives in `tools/builder.toml` (services
restart map, defaults); per-checkout state in `tools/builder.local.toml`
(gitignored).

See `docs/plans/2026-05-14-builder-tui-design.md` for the design.

## Build And Deploy

- [rebuild.sh](/home/shanes/plane_watcher/tools/rebuild.sh): thin shell wrapper for the Vivado + Yocto/EDF rebuild/package/deploy flow (prefer `ps/cmd/builder` for new use)
- [build-yocto.sh](/home/shanes/plane_watcher/tools/build-yocto.sh): EDF/Yocto build wrapper — stages XSA, runs `xsct sdtgen`, BitBake, packs `image.ub`
- [build-smartzynq-phase1.sh](/home/shanes/plane_watcher/tools/build-smartzynq-phase1.sh): Vivado-only Smart ZYNQ phase-1 bitstream/XSA build
- [build-petalinux.sh](/home/shanes/plane_watcher/tools/build-petalinux.sh): legacy PetaLinux wrapper, retained until Tier 2 soak passes
- [build-bitstream.sh](/home/shanes/plane_watcher/tools/build-bitstream.sh): legacy Pluto/vendor bitstream wrapper
- [deploy-bitstream.sh](/home/shanes/plane_watcher/tools/deploy-bitstream.sh): legacy `system_top.bit.bin` packaging/deploy helper
- [plane_watcher.env.example](/home/shanes/plane_watcher/tools/plane_watcher.env.example): template for machine-local configuration

Typical flow:

```sh
cp tools/plane_watcher.env.example tools/plane_watcher.env
$EDITOR tools/plane_watcher.env
./tools/rebuild.sh --no-deploy
```

## Simulation And Analysis

- [gen_test_vectors.py](/home/shanes/plane_watcher/tools/gen_test_vectors.py): generate ADS-B test vectors for `hdl/sim`
- [decode_sim_log.py](/home/shanes/plane_watcher/tools/decode_sim_log.py): parse decoded-message output from simulation logs
- [compare_results.py](/home/shanes/plane_watcher/tools/compare_results.py): compare sim output against a software reference log
- [scan_iq.py](/home/shanes/plane_watcher/tools/scan_iq.py): inspect IQ captures in software
- [scan_scope_csv.py](/home/shanes/plane_watcher/tools/scan_scope_csv.py): recover Mode-S frames from scope-exported detector-envelope CSV captures
- [udev/99-siglent-sds800x-hd-usbtmc.rules](/home/shanes/plane_watcher/tools/udev/99-siglent-sds800x-hd-usbtmc.rules): grant non-root USBTMC access to the bench `SDS814X-HD` scope
- [resample_pluto_capture.py](/home/shanes/plane_watcher/tools/resample_pluto_capture.py): convert captured raw IQ into the expected sample format/rate

## Capture Helpers

- [pluto_capture.py](/home/shanes/plane_watcher/tools/pluto_capture.py): record raw Pluto IQ to disk, optionally with JSON headroom/compression summary stats
- [pluto_plot.py](/home/shanes/plane_watcher/tools/pluto_plot.py): capture and plot Pluto RX data
- [hackrf_capture.py](/home/shanes/plane_watcher/tools/hackrf_capture.py): convert HackRF captures into testbench-ready input

## Tracked Versus Local Files

Tracked here:

- reusable scripts
- reference logs that support repeatable analysis
- Python dependency metadata

Ignored here:

- raw captures such as `*.raw`
- local virtualenvs and Python cache directories
- machine-local `plane_watcher.env`

Large capture files currently present in the working tree are local artifacts,
not part of the intended tracked tool surface.
