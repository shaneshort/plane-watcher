# HDL Layout

`hdl/` is the FPGA side of the repository.

## Directories

- `rtl/`: synthesizable VHDL for the decode pipeline and board wrappers
- `tb/`: VHDL testbenches
- `sim/`: GHDL entrypoints, helper scripts, generated vectors, waves, and work dirs
- `vivado/`: project generation, block-design assembly, implementation scripts, and constraints

## Recommended Workflow

Simulation:

```sh
cd hdl/sim
make sim_all
```

Vivado build:

```sh
./tools/build-bitstream.sh --skip-deploy
```

Bitstream packaging only:

```sh
./tools/deploy-bitstream.sh --generate-only
```

## Generated Output Policy

Generated outputs belong in the existing build directories and should not be
checked in:

- `hdl/sim/work/`
- `hdl/sim/vectors/`
- `hdl/sim/waves/*.ghw`
- `hdl/sim/*_tb`
- `hdl/vivado/build/`
- `hdl/vivado/.Xil/`
- `hdl/vivado/*.log`
- `hdl/vivado/*.jou`

The tracked source of truth is the RTL, testbenches, constraints, TCL, and
Makefiles, not the generated simulator or Vivado output.
