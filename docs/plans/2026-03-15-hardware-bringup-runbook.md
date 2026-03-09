# Hardware Bring-Up Runbook

Purpose: first-power-on checklist for validating the PS/PL interface and
the Beast output path when the Zynq hardware arrives.

Current validated bitstream context for this runbook:

- implementation top: `zynq_adsb_vendor_wrapper`
- `adsb_vendor_wrapper` AXI base: `0x43D00000`
- `axi_ad9361` AXI base: `0x43C00000`
- PPS is intentionally tied inactive on the current board revision

For the full integration history and board-specific control-path decisions, see
`docs/plans/2026-03-16-hybrid-bitstream-handoff.md`.

This document assumes the current PS helpers exist:

- `plane-feeder`
- `regdump`
- `fifo-monitor`
- `pps-check`
- `beast-client`

It also assumes the PS/PL contract in `docs/plans/2026-03-15-ps-pl-contract.md`
is the source of truth for register order, FIFO pop semantics, byte order,
and timestamp meaning.

## 1. Pre-Flight

Before touching hardware, confirm:

- Linux is booted and you have shell access.
- `/dev/mem` access is available.
- The FPGA bitstream for the current RTL is loaded.
- The AXI base address is known.
- PPS wiring is connected if timing validation is part of the session.
- The helper binaries are present on the target.

Expected defaults:

- AXI base address: `0x43D00000`
- Beast TCP port: `30005`

Current expectation for this board revision:

- `PPS_COUNT` remains `0` unless you have intentionally added a new PPS route

## 2. Order of Operations

Do the checks in this order:

1. `regdump` for basic register sanity
2. `pps-check` for timing sanity
3. `fifo-monitor` for queue behavior
4. `plane-feeder` for end-to-end forwarding
5. `beast-client` for wire-format verification

Do not start by draining the FIFO aggressively. First prove the block is
alive and stable.

## 3. Step 1: Basic Register Sanity

Run:

```bash
./regdump --base-addr 0x43D00000
```

Expected good signs:

- `VERSION` reads `0x00010000`
- `CONTROL` is readable
- `STATUS` is readable and the bits make sense for the current conditions
  (for example, `0x00000000` is valid if the FIFO is empty and no overflow
  has occurred)
- `PPS_COUNT` and `PPS_CTR` are readable

For the current board revision:

- readable PPS registers with `PPS_COUNT = 0` are expected
- treat non-changing PPS as normal until a real PPS source is added

If FIFO is empty, expected output includes:

- `FIFO empty - no message data to display`

Bad signs and likely causes:

- open `/dev/mem` fails:
  - wrong permissions
  - not running as root
  - wrong platform image
- `VERSION = 0x00000000` or `0xFFFFFFFF`:
  - wrong AXI base address
  - bitstream not loaded
  - bus connection issue
- unreadable or frozen PPS registers:
  - PPS path not connected
  - timing core not running

Only use `--pop` after confirming the FIFO is non-empty and you are ready
to consume one message:

```bash
./regdump --base-addr 0x43D00000 --pop
```

Expected good signs with `--pop`:

- `RPL` is shown
- decoded DF looks plausible
- decoded hex is 7 or 14 bytes

## 4. Step 2: PPS Sanity

Run:

```bash
./pps-check --base-addr 0x43D00000 --samples 5
```

Expected good signs:

- `PPS_COUNT` increments once per second
- `DELTA_TICKS` is close to `16000000`
- `ERROR_PPM` is small
- final status is `PASS` or at least no hard fail

Interpretation:

- `FAIL: no PPS edges after 10s`
  - PPS wiring missing
  - GNSS not configured
  - no lock / no pulse source
- `FAIL: PPS count stuck`
  - pulse not arriving
  - PPS register path broken
- repeated `WARN` with large ppm error:
  - clock source issue
  - wrong sample clock assumption
  - unstable timing source

If PPS is intentionally unavailable for the session, note that now and
skip Radarcape validation later.

For the current hybrid bitstream, PPS is intentionally unavailable unless you
have made an additional board modification not captured in the repo docs.

## 5. Step 3: FIFO and Status Behavior

Run:

```bash
./fifo-monitor --base-addr 0x43D00000 --interval 1s
```

Expected good signs:

- `PPS` count increments once per second
- `FILL` stays low or oscillates sensibly
- `OVERFLOW` remains `false`
- `EMPTY` flips according to traffic conditions

What to look for:

- `FILL` climbs and never drops:
  - PS consumer not draining
  - decode path producing data but no reader running
- `FULL=true` or `OVERFLOW=true`:
  - consumer too slow
  - excessive false positives
  - FIFO depth issue
- always empty even with antenna / signal source present:
  - RF path issue
  - decoder not enabled
  - PL path broken before FIFO

If messages appear intermittently, use `regdump --pop` a few times to spot
check decoded payloads before moving to the full feeder.

Note:

- each `--pop` consumes one FIFO entry
- stop using `--pop` once you are ready to observe the real feeder path
- otherwise `plane-feeder` will not see those consumed messages

## 6. Step 4: Start plane-feeder

Run:

```bash
./plane-feeder --base-addr 0x43D00000 --port 30005
```

Expected startup signs:

- register mapping succeeds
- hardware version prints correctly
- Beast server listens on the requested port
- periodic stats appear

Expected runtime signs:

- message count increases
- client count is correct when a client connects
- overflow remains false

If PPS is available and you want to test the current Radarcape path:

```bash
./plane-feeder --base-addr 0x43D00000 --port 30005 --radarcape
```

Remember:

- current Radarcape mode is `PPS + coarse UTC correlation`
- it is not true GNSS-latched UTC yet

## 7. Step 5: Validate Beast Output

In a second shell, connect with the smoke-test client:

```bash
./beast-client --addr <target-ip>:30005
```

Useful options:

```bash
./beast-client --addr <target-ip>:30005 --count 20
./beast-client --addr <target-ip>:30005 --quiet --count 100
```

Expected good signs:

- frames parse cleanly
- `DF` values look plausible
- long/short mix is sensible for local traffic
- error count stays at zero

Bad signs and likely causes:

- no frames:
  - FIFO empty
  - plane-feeder not draining
  - no decode traffic
- many parse errors:
  - Beast framing bug
  - escaping bug
  - partial stream corruption
- obviously invalid DFs or nonsense payloads:
  - AXI byte order regression
  - FIFO read-order bug
  - upstream decode issue

## 8. First Real Comparison

Once the basic path works, do a short live comparison against a software
reference on the same raw input source if available.

Use the existing comparison tooling where practical:

```bash
cd tools
uv run python compare_results.py <plane-feeder-or-sim-log> <reference-log>
```

Track at minimum:

- total decoded messages
- unique ICAOs
- DF17/18 coverage
- short-message coverage
- CRC-clean FPGA-only messages
- reference-only misses

Goal of the first session:

- prove the system is alive
- prove the PS/PL boundary is correct
- prove Beast output is consumable

Do not chase weak-signal performance first. That is a second-pass task.

## 9. Quick Triage Matrix

`regdump` broken:
- base address, bitstream, `/dev/mem`, bus connectivity

`pps-check` broken:
- PPS wiring, GNSS config, timing core

`fifo-monitor` shows empty forever:
- no RF data, decoder disabled, upstream PL issue

Before going deeper, confirm decoder enable state:

- `plane-feeder` writes `CONTROL=0x02` automatically
- if you are only using manual tools, check `CONTROL.enable`
- an empty FIFO with `enable=0` is expected

`fifo-monitor` overflows:
- feeder not running, false positives, FIFO too shallow, consumer too slow

`plane-feeder` runs but `beast-client` sees nothing:
- no FIFO traffic, port mismatch, network path issue

`beast-client` sees malformed frames:
- Beast encode regression, escaping bug, byte-order bug

## 10. Capture for Postmortem

If bring-up fails, save:

- `regdump` output
- `pps-check` output
- a short `fifo-monitor` log
- `plane-feeder` startup and stats logs
- `beast-client` sample output

That set should be enough to distinguish:

- register-map problem
- timing problem
- FIFO problem
- Beast framing problem
- upstream decode problem

## 11. Deferred Work

Not required for first bring-up:

- true GNSS UTC integration
- better weak-signal tuning
- hardware normalization of message byte order
- full automated live comparison harness

Those come after the first successful end-to-end session.
