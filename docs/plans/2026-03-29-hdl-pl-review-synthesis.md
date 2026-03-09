# HDL/PL Review Synthesis

Date: 2026-03-29

Purpose: merge the Codex and Claude HDL/PL reviews into one execution-oriented document. This is a fix list, not a second architecture summary.

## What Matters Most

There are four issues worth treating as current trust breakers for the PL image and its PS-visible outputs:

1. the timestamp contract is inconsistent across RTL, PS code, tools, and docs
2. the repo exposes multiple Vivado build paths that do not produce the same hardware, and one of them wires a reset source that its sibling flow explicitly documents as unsafe
3. the main decoder regression bench is not using the same detector timing as the integrated design
4. the deployed RX sample CDC is still relying on a false-pathed multi-bit bus capture

Everything else below is either a supporting fix for those items or lower-priority portability debt.

## Fix Now

### 1. Repair the timestamp contract end-to-end

Priority: `P0`
Status: current correctness bug

Merged findings:

- `timestamp_counter` is clocked from `adsb_top.clock`, and the deployed wrapper drives that from `S_AXI_ACLK` at 100 MHz.
- The frozen PS/PL contract still says 16 MHz and still documents `ts12 = toa * 3 / 4` and `nanos = delta * 1e9 / 16e6`.
- `hdl/rtl/adsb_pkg.vhd` still describes `COUNTER_WIDTH` as a 16 MHz counter, so the stale rate is also embedded in the shared package comments other modules reference.
- Active PS code still uses 16 MHz in `ps/internal/beast/beast.go`.
- `ps/cmd/pps-check/main.go` still expects `16_000_000` ticks per second.

Primary files:

- `hdl/rtl/adsb_top.vhd`
- `hdl/rtl/adsb_vendor_wrapper.vhd`
- `hdl/rtl/adsb_pkg.vhd`
- `ps/internal/beast/beast.go`
- `ps/cmd/pps-check/main.go`
- `docs/plans/2026-03-15-ps-pl-contract.md`
- `docs/ARCHITECTURE.md`

Actions:

- Change every active PS-side TOA/PPS conversion to 100 MHz.
- Update the frozen contract doc so a new client implemented from that file would be correct.
- Update any tool or diagnostic that assumes 16 MHz PPS deltas.
- Add a small PS-side test that checks known TOA inputs against expected Beast and Radarcape timestamp outputs for a 100 MHz counter.

Done when:

- code, docs, and tools all state 100 MHz unambiguously
- Beast timestamp scaling is derived from 100 MHz, not 16 MHz
- `pps-check` expects 100,000,000 ticks per second
- one automated test locks the conversion behavior

### 2. Pick one canonical Vivado flow and make build, deploy, and boot match it

Priority: `P0`
Status: current integration/build hazard with a latent hybrid reset bug

Merged findings:

- `create_vendor_hybrid_bd.tcl` uses the FIR-decimated ingress path and wires `rx_reset` from `axi_ad9361/rst`.
- `build_vendor.tcl` explicitly says not to use `axi_ad9361/rst`, because it is the vendor core's internal runtime reset and can pulse during calibration and ENSM transitions; it creates a dedicated reset block instead.
- That means the staged hybrid flow is not just a different build path. It is wiring exactly the reset source that the single-session flow calls unsafe for the custom RX path.
- `build_stock_tap.tcl` also feeds raw ADC directly into `adsb_vendor_wrapper`.
- `tools/build-bitstream.sh` defaults to `stock-tap-build`, while the handoff doc describes the hybrid FIR-based image.
- `tools/deploy-bitstream.sh` packages `system_top.bit`, while `build_vendor.tcl` reports `${design_name}_wrapper.bit`.
- boot docs mention renaming to `system.bit.bin`.

Primary files:

- `hdl/vivado/create_vendor_hybrid_bd.tcl`
- `hdl/vivado/build_vendor.tcl`
- `hdl/vivado/build_stock_tap.tcl`
- `hdl/vivado/Makefile`
- `tools/build-bitstream.sh`
- `tools/deploy-bitstream.sh`
- `boot/uEnv.plane_watcher.txt`
- `docs/plans/2026-03-16-hybrid-bitstream-handoff.md`
- `hdl/vivado/README.md`
- `tools/README.md`

Actions:

- Decide which image is actually the supported one: hybrid FIR path, direct-ADC stock-tap path, or a separate debug-only build.
- Either fix the staged hybrid flow's reset wiring immediately or stop presenting it as a supported build path.
- Mark non-canonical flows as debug-only or remove them from repo-level wrappers.
- Make the default build target, generated bit filename, deploy script, and boot instructions all describe the same artifact.
- If the FIR-bypass path is retained, label it as a diagnostic build in both code comments and docs.

Done when:

- one documented repo-level command builds the intended hardware image
- no supported flow feeds the custom RX path from `axi_ad9361/rst`
- the bitstream filename produced by the build is the one the deploy script packages
- the packaged `.bit.bin` name matches what the boot docs tell the user to load
- the handoff doc matches the selected canonical path

### 3. Align regression simulation with the actual detector timing

Priority: `P0`
Status: current verification blind spot

Merged findings:

- `adsb_decoder` defaults `PREAMBLE_MESSAGE_DELAY` to `50`.
- `adsb_decoder_tb` hardcodes `40`.
- `hdl/sim/Makefile` runs the main decoder and detector-regression targets without overriding that mismatch.
- The integrated top uses the decoder defaults, not the stale testbench override.
- On the current tree, `adsb_decoder_tb` with its default generic fails on `single_clean.dat`, but the same bench with `50/76` decodes cleanly.

Primary files:

- `hdl/rtl/adsb_decoder.vhd`
- `hdl/tb/adsb_decoder_tb.vhd`
- `hdl/sim/Makefile`
- `hdl/rtl/adsb_top.vhd`

Actions:

- Make the testbench defaults match the integrated design, or explicitly pass the deployed values from the Makefile.
- Strengthen the decoder and detector-regression sims so they assert decoded message content, not just SOM count.
- Pull `single_short.dat` into the standard sim surface instead of leaving it off the default path.

Done when:

- the default decoder TB uses the same timing as the deployed core
- `single_clean.dat` and `two_sequential.dat` pass with the integrated timing
- regression checks fail on wrong message content or wrong decoder claiming behavior

### 4. Replace or formally justify the wrapper CDC

Priority: `P1`
Status: current hardware risk

Merged findings:

- `adsb_vendor_wrapper` transfers `cdc_power_hold` directly across the `rx_clk -> S_AXI_ACLK` boundary and only synchronizes the toggle.
- the XDC false-paths both the toggle and the multi-bit data path.
- the source cadence comes from a fractional accumulator, not a fixed integer divider, but the actual minimum gap is still only two input samples at 30.72 MHz, or about 65 ns.
- with roughly 20 ns for a 2-FF synchronizer and another 10 ns capture cycle, the nominal margin is about 35 ns, which is plausible in practice but not guaranteed by timing constraints.

Primary files:

- `hdl/rtl/adsb_vendor_wrapper.vhd`
- `hdl/vivado/constr/plane_watcher_integration.xdc`
- `hdl/rtl/power_downsampler.vhd`

Actions:

- Preferred: replace the toggle-plus-held-bus crossing with a real small async FIFO or a bundled-data handshake that is constrained and reviewable.
- Lighter-weight option: if the current scheme is intentionally kept, document the worst-case timing math and replace the data-bus `set_false_path` with a real budget such as `set_max_delay`, so Vivado has to meet a bounded skew/arrival target.
- Add a wrapper-level simulation that uses unrelated `rx_clk` and `S_AXI_ACLK` periods and checks for exact sample ordering.

Done when:

- the wrapper no longer depends on an unconstrained false-pathed multi-bit bus capture
- or the retained bundled-data scheme has explicit timing constraints instead of an unconstrained false path on the data bus
- there is an end-to-end wrapper TB that exercises the actual deployed ingress and CDC path
- hardware behavior is no longer build-dependent on this crossing

## Fix Next

### 5. Re-baseline the RPL and PS signal-level contract

Priority: `P1`
Status: current integration mismatch

Merged findings:

- actual ingress power is `I^2 + Q^2`, scaled right by 2 in `iq_to_power`
- preamble `RPL` is derived from the summed pulse energy in that power domain
- `docs/ARCHITECTURE.md` still claims Manhattan magnitude
- `docs/DSP_DESIGN_SPEC.md` and `docs/wiki/images/iq-to-power.svg` still explain the ingress path as Manhattan magnitude
- `docs/plans/2026-03-15-ps-pl-contract.md` still says Beast signal is `RPL >> 16`
- active PS code maps `RPL` using a Manhattan-style `rplMax = 16383`
- `ps/internal/tracker/tracker.go` separately uses `uint8(msg.RPL >> 16)`, which is a different bug: with the current squared-power range it collapses real signal levels into a very coarse low-end bucket range

Primary files:

- `hdl/rtl/iq_to_power.vhd`
- `hdl/rtl/preamble_detector.vhd`
- `ps/internal/beast/beast.go`
- `ps/internal/tracker/tracker.go`
- `docs/ARCHITECTURE.md`
- `docs/DSP_DESIGN_SPEC.md`
- `docs/wiki/images/iq-to-power.svg`
- `docs/plans/2026-03-15-ps-pl-contract.md`

Actions:

- Decide what `RPL` means at the PS boundary and keep that definition stable.
- Fix `beast.go` signal-byte encoding against the actual PL quantity and range.
- Fix `tracker.go` separately; `RPL >> 16` is too coarse for the current practical dynamic range even if Beast encoding is corrected.
- Remove the old Manhattan-power wording from active docs and diagrams unless the hardware is changed back.
- Add at least one regression test that checks signal-byte behavior for known `RPL` values.

Done when:

- PL comments, PS code, and docs all describe the same `RPL` meaning
- Beast and tracker signal outputs are both calibrated to the current RTL output range
- the contract doc no longer describes a stale power metric

### 6. Bring the frozen PS/PL contract back in sync with the live register map

Priority: `P1`
Status: active documentation hazard

Merged findings:

- the contract doc timestamp section is stale
- the byte-order description is reversed even though the extraction procedure happens to work
- `DBG_INDEX`, `DBG_DATA`, `CONFIG`, and `CONTROL[2]` are missing from the contract doc

Primary files:

- `docs/plans/2026-03-15-ps-pl-contract.md`
- `hdl/rtl/axi_regs.vhd`
- `hdl/rtl/bit_flipper.vhd`

Actions:

- Rewrite the register map directly from `axi_regs.vhd`.
- Fix the byte-order prose so it matches what the hardware exposes.
- Keep one explicit example showing how a PS client reconstructs a message from the register words.

Done when:

- a new PS client could be implemented from the contract doc alone without reverse-engineering the RTL

### 7. Decide whether quiet zone B is intentional, then lock it with a directed test

Priority: `P2`
Status: plausible RTL bug, not yet proven harmful

Merged findings:

- quiet zone B is compared against `sum2`, not `sum1`
- quiet zone B is the gap between pulse 1 and pulse 2
- `sum1` is not used in any quiet-zone comparison

Primary files:

- `hdl/rtl/preamble_detector.vhd`

Actions:

- Re-check the intended detector math and the original reasoning for zone B.
- If the reference should be pulse 1, fix both the per-zone check and the quiet-score term together.
- Add a directed preamble-detector test where pulse 1 is weak and pulse 2 is strong, so the difference is observable.

Done when:

- the code reflects an intentional choice and there is a test that would fail if this relation regressed

## Portability And Low-Priority Debt

These are worth fixing, but they should not pre-empt the current correctness and build issues above.

### 8. Add `ASYNC_REG` to synchronizers that are meant to be real CDCs

Priority: `P2`

Relevant files:

- `hdl/rtl/timestamp_counter.vhd`
- `hdl/rtl/adsb_top.vhd`
- `hdl/rtl/adsb_vendor_wrapper.vhd` already shows the intended pattern

Why it stays below the line:

- `timestamp_counter` PPS synchronizer should carry `ASYNC_REG`; that is a small real improvement.
- `adsb_top` synchronizers are mostly latent today because the deployed wrapper drives the core and AXI logic from the same clock, but the generic code is clearly written as CDC.

### 9. Clean up reset discipline and startup state in the low-priority paths

Priority: `P3`

Relevant files:

- `hdl/rtl/async_msg_fifo.vhd`
- `hdl/rtl/adsb_pl_wrapper.vhd`
- `hdl/rtl/adsb_edge_detector.vhd`

Why it stays below the line:

- the current vendor-wrapper hardware path does not expose the dual-clock FIFO reset issue
- `adsb_edge_detector` does not clear `power_grid`, `power_exceeds_thresh`, or `edge_grid` on reset; that creates startup Xs and potentially a brief false-history window, but the downstream preamble quality gate and initial pipeline fill make it low-risk in practice

## Testbench Work That Retires The Most Risk

Add these before trusting further detector or hardware bring-up changes:

- a standalone `preamble_detector` TB covering peak detection, holdoff, no-free-decoder, and the quiet-zone-B corner case
- a `vendor_rx_ingress` or `adsb_vendor_wrapper` TB that exercises `iq_to_power`, `power_downsampler`, and the real CDC
- a true dual-clock `axi_regs` or `async_msg_fifo` TB instead of the current same-clock AXI bench
- stronger AXI assertions that check exact payload words, TOA ordering, and FIFO pop semantics
- a hermetic sim vector flow so `sim_all` does not depend on a writable external `uv` cache

## Suggested Execution Order

If this work is split into small PRs, the safest order is:

1. timestamp contract and PS scaling
2. canonical Vivado build and deploy flow
3. simulation timing alignment and stronger assertions
4. wrapper CDC redesign or justification plus wrapper-level TB
5. RPL/signal-level contract cleanup
6. contract-doc cleanup and lower-priority CDC/reset hygiene

## Practical Bottom Line

If only one thing is fixed immediately, fix the 100 MHz timestamp contract and verify the live PS code against it.

If two things are fixed immediately, also collapse the build and deploy story down to one canonical hardware image.

Those two items are the fastest way to stop the repo from producing hardware that "works" while still exporting wrong timestamps or ambiguous bitstreams.
