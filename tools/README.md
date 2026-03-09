# Tools

`tools/` contains utility scripts for three separate jobs:

- bitstream build/deploy wrappers
- simulation vector generation and log analysis
- RF capture and inspection helpers

## Build And Deploy

- [build-bitstream.sh](/home/shanes/plane_watcher/tools/build-bitstream.sh): Vivado build wrapper with timing gate
- [deploy-bitstream.sh](/home/shanes/plane_watcher/tools/deploy-bitstream.sh): package and optionally deploy `system_top.bit.bin`
- [plane_watcher.env.example](/home/shanes/plane_watcher/tools/plane_watcher.env.example): template for machine-local configuration

Typical flow:

```sh
cp tools/plane_watcher.env.example tools/plane_watcher.env
$EDITOR tools/plane_watcher.env
./tools/build-bitstream.sh --skip-deploy
./tools/deploy-bitstream.sh --generate-only
```

## Simulation And Analysis

- [gen_test_vectors.py](/home/shanes/plane_watcher/tools/gen_test_vectors.py): generate ADS-B test vectors for `hdl/sim`
- [decode_sim_log.py](/home/shanes/plane_watcher/tools/decode_sim_log.py): parse decoded-message output from simulation logs
- [compare_results.py](/home/shanes/plane_watcher/tools/compare_results.py): compare sim output against a software reference log
- [scan_iq.py](/home/shanes/plane_watcher/tools/scan_iq.py): inspect IQ captures in software
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
