# First Board Session Checklist

Purpose: provide an exact, low-ambiguity command sequence for the first real
session with the current hybrid bitstream and PS helper tools.

This checklist assumes the current board-specific hybrid design described in:

- `docs/plans/2026-03-16-hybrid-bitstream-handoff.md`

Key assumptions for this session:

- bitstream top: `zynq_adsb_vendor_wrapper`
- `adsb_vendor_wrapper` AXI base: `0x43D00000`
- `axi_ad9361` AXI base: `0x43C00000`
- PPS is intentionally tied inactive for the current board revision

## 1. Artifacts To Have Ready

Before powering the board:

- the generated bitstream from
  `hdl/vivado/build/plane_watcher/plane_watcher.runs/impl_1/zynq_adsb_vendor_wrapper.bit`
- the helper binaries from `ps/cmd/`:
  - `regdump`
  - `fifo-monitor`
  - `pps-check`
  - `plane-feeder`
  - `beast-client`
- a shell on the target Linux system
- `/dev/mem` access on the target

## 2. Optional: Rebuild The PS Helpers

From the repo root on the build machine:

```bash
cd ps
go build ./cmd/regdump
go build ./cmd/fifo-monitor
go build ./cmd/pps-check
go build ./cmd/plane-feeder
go build ./cmd/beast-client
```

If you need cross-compiled binaries for the board image, build those using your
normal target toolchain flow before the session.

## 3. Program The FPGA

Use your normal board programming path to load:

- `zynq_adsb_vendor_wrapper.bit`

This repository does not yet define a single canonical programming command
because board access may happen through Vivado Hardware Manager, boot image
packaging, or a board-specific Linux flow.

The requirement for the rest of this checklist is simply:

- the current hybrid bitstream is loaded into the FPGA

## 4. Boot Linux And Stage The Tools

On the target board:

1. boot Linux
2. copy the helper binaries to a working directory
3. confirm root or `/dev/mem` access

Useful quick checks:

```bash
id
ls -l /dev/mem
uname -a
```

## 5. Step 1: Register Sanity

Run:

```bash
./regdump --base-addr 0x43D00000
```

Expected:

- `VERSION` is readable
- `CONTROL` is readable
- `STATUS` is readable
- PPS registers are readable

Current-board expectation:

- `PPS_COUNT` stays `0`
- this is normal because PPS is tied inactive in the current hybrid BD

If this fails:

- stop and capture the full output
- do not continue to FIFO or Beast checks until register access works

## 6. Step 2: PPS Sanity, But With The Correct Expectation

Run:

```bash
./pps-check --base-addr 0x43D00000 --samples 5
```

Current expected behavior:

- no PPS increments
- PPS-related failure output is expected on this board revision

Use this step only to confirm the software can read the timing registers, not
to prove real PPS behavior yet.

Do **not** treat `PPS_COUNT = 0` as a hardware failure for the current board.

## 7. Step 3: FIFO Status Polling

Run:

```bash
./fifo-monitor --base-addr 0x43D00000 --interval 1s
```

Watch for:

- readable status
- sensible FIFO fill reporting
- no immediate overflow

At this stage, the key question is not decode quality yet. The first question
is whether the register path and decoder-facing status path are alive.

## 8. Step 4: Spot-Check One FIFO Pop

Only do this if `fifo-monitor` suggests data is actually present.

Run:

```bash
./regdump --base-addr 0x43D00000 --pop
```

Expected:

- one FIFO entry is consumed
- decoded payload length is plausible
- output is not obviously corrupt

Do not keep using `--pop` repeatedly once you move to `plane-feeder`.

## 9. Step 5: Start The Beast Feeder

Run:

```bash
./plane-feeder --base-addr 0x43D00000 --port 30005
```

Expected:

- startup succeeds
- hardware version is printed
- Beast server starts
- periodic stats appear

Do **not** start with `--radarcape` for the first session unless you are
explicitly testing timestamp formatting behavior. PPS is inactive, so standard
Beast mode is the sensible default for first power-on.

## 10. Step 6: Validate Beast Output

From another shell or another machine:

```bash
./beast-client --addr <target-ip>:30005 --count 20
```

or:

```bash
./beast-client --addr <target-ip>:30005 --quiet --count 100
```

Expected:

- frames parse cleanly
- the client does not report framing corruption
- message flow is at least plausible for the local RF environment

## 11. What To Save From The First Session

Save these outputs even if the session succeeds:

- `regdump` output
- `pps-check` output
- 10-30 seconds of `fifo-monitor`
- `plane-feeder` startup and stats output
- `beast-client` sample output

Those become the baseline for future regressions.

## 12. Immediate Triage Rules

If `regdump` fails:

- suspect bitstream load, AXI base, or `/dev/mem` access first

If `pps-check` reports no PPS:

- that is expected on the current board revision

If `fifo-monitor` is always empty:

- either no valid RF traffic is reaching the decoder yet
- or the ingress/decoder path still needs live tuning

If `plane-feeder` starts but `beast-client` sees nothing:

- focus on FIFO status and feeder logs before touching RF assumptions

If frames parse but content looks implausible:

- capture examples and compare later against a software reference
- do not retune the detector blindly during the first session

## 13. What Not To Do In Session One

Avoid these on the first board session:

- changing AXI base addresses
- re-exposing PPS
- re-exposing `up_enable` / `up_txnrx`
- retuning detector thresholds without baseline logs
- turning the session into a full RF-performance investigation

Session one is for proving:

- the bitstream loads
- the PS/PL register path works
- the feeder runs
- Beast output is consumable

Everything else comes after that baseline exists.
