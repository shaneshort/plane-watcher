# Vendor RX Ingress Plan

Purpose: adapt the vendor board's natural AD936x receive path into the
project's existing ADS-B decoder input without rewriting the decoder core
before first hardware bring-up.

## 1. What We Learned

From the vendor Pluto-derived block design:

- `axi_ad9361` is the physical AD936x interface
- RX I/Q channel 0 feeds `rx_fir_decimator`
- `rx_fir_decimator` outputs feed `cpack` and then DMA

Relevant connections from the vendor `system_bd.tcl`:

- `axi_ad9361/adc_data_i0 -> rx_fir_decimator/data_in_0`
- `axi_ad9361/adc_data_q0 -> rx_fir_decimator/data_in_1`
- `rx_fir_decimator/data_out_0 -> cpack/fifo_wr_data_0`
- `rx_fir_decimator/data_out_1 -> cpack/fifo_wr_data_1`
- `rx_fir_decimator/valid_out_0 -> cpack/fifo_wr_en`
- `axi_ad9361/l_clk -> rx_fir_decimator/aclk`

The decimation helper configuration shows:

- I and Q outputs are each 16 bits wide
- the filter runs with `Clock_Frequency = 61.44 MHz`
- the filter runs with `Sample_Frequency = 61.44 MHz`
- `parallel_paths = 1`

So the natural board ingress point is:

- clock: `axi_ad9361/l_clk` at approximately `61.44 MHz`
- valid: `rx_fir_decimator/valid_out_0`
- I: `rx_fir_decimator/data_out_0[15:0]`
- Q: `rx_fir_decimator/data_out_1[15:0]`

## 2. Why The Current Decoder Should Not Be Rebased Yet

The existing decoder stack was built around a scalar power stream at roughly
`16 MHz`, with timestamping and test expectations written against that
assumption.

Blindly rebasing the whole decoder to `61.44 MHz` now would mix too many
changes at once:

- new radio ingress path
- new sample representation
- new sample rate
- new timestamp scaling assumptions
- possible detector tuning changes

That is the wrong risk profile immediately before first hardware bring-up.

## 3. Recommended Architecture

Keep the existing decoder contract intact for first bring-up and add a board-
specific ingress layer in front of it.

Recommended chain:

```text
vendor RX I/Q @ 61.44 MHz
  -> iq_to_power
  -> simple rate adaptation / downsample
  -> existing decoder input (sample_power, sample_valid)
```

This isolates board/radio specifics from the decoder core and preserves the
current PS/PL contract.

## 4. First Bring-Up Target Rate

Recommended first target: `15.36 MHz`.

Reason:

- exact divide of `61.44 MHz` by 4
- close to the current `16 MHz` design assumption
- avoids introducing fractional resampling before hardware exists
- keeps enough time resolution for an initial ADS-B bring-up path

This is a bring-up target, not necessarily the final production rate.

## 5. Immediate Implementation Tasks

### Task 1: Add `iq_to_power`

Create an RTL block that:

- accepts signed `I[15:0]`, `Q[15:0]`, and `valid`
- computes a scalar power-like metric suitable for the existing decoder
- outputs `sample_power` and `sample_valid`

Initial recommendation:

- `power = abs(I) + abs(Q)`

This is simple, cheap, and sufficient for the first integration pass.

### Task 2: Add a simple `/4` rate adapter

Create a first-pass rate adapter that:

- runs in the `61.44 MHz` RX clock domain
- emits one output sample for every four valid input samples
- preserves a clean `sample_valid` strobe

For first bring-up, this can be a deterministic decimator rather than a more
clever filter chain.

### Task 3: Introduce a vendor-specific ingress wrapper

Keep `adsb_pl_wrapper` generic.

Add a board-facing integration layer that accepts:

- `rx_i`
- `rx_q`
- `rx_valid`
- `rx_clk`
- `pps_in`

and drives the existing decoder input after the ingress chain.

### Task 4: Update timing assumptions in docs

Document explicitly that:

- the vendor RX path is `61.44 MHz`, 16-bit I/Q
- the first planned decoder ingress rate is `15.36 MHz`
- the historical `16 MHz` assumption now applies to the decoder-side contract,
  not the raw board RX path

## 6. What Not To Do Yet

Do not do these before first hardware bring-up:

- rewrite the decoder core for native `61.44 MHz`
- collapse the ingress adaptation into the decoder itself
- tap the packed DMA path after `cpack`
- redesign the PS/PL register map
- redesign timestamp semantics around the raw RX clock

## 7. Open Questions For Hardware Arrival

- what actual sample scaling/range reaches `rx_fir_decimator/data_out_*` on
  the real board under normal gain settings?
- is additional decimation or averaging needed before the decoder for robust
  weak-signal behavior?
- should PPS ultimately live in the same clock domain as the decoder ingress
  adapter or be synchronized later?

## 8. Board-Specific Control Path Decision

The current 7020 AD936x board revision does not justify exposing every control
signal as a top-level FPGA pin.

What was verified:

- board schematic shows real AD936x physical control pins:
  - `ENABLE`
  - `TXNRX`
- vendor XDC constrains those physical ports as:
  - `enable`
  - `txnrx`
- vendor software (`buildroot/board/pluto/test_ensm_pinctrl.sh`) drives those
  ENSM pins through Zynq GPIO:
  - `GPIO_ENABLE = zynq_gpio + 69`
  - `GPIO_TXNRX = zynq_gpio + 70`
- ADI binding text says `ENABLE/TXNRX` control ENSM state, while the default
  control path is SPI writes

Interpretation:

- `enable` and `txnrx` are board-facing outputs to the AD936x and remain
  package pins in the hybrid design
- `up_enable` and `up_txnrx` are internal control-side signals on
  `axi_ad9361`; they are not assumed to be board pins
- exposing `up_enable` / `up_txnrx` as unconstrained top-level FPGA pins would
  be the wrong model for this board

Current board-specific implementation choice:

- constrain the real AD936x LVDS and ENSM physical pins from the vendor XDC
- keep `enable` and `txnrx` external
- tie `axi_ad9361/up_enable` and `axi_ad9361/up_txnrx` inactive until the PS or
  EMIO GPIO control path is intentionally recreated
- tie decoder `pps_in` inactive for this board revision because no real PPS
  source exists on the current hardware

This choice is deliberate, not provisional guesswork. If future hardware adds a
JP5 or other PPS route, or if we recreate the vendor GPIO control path in PL,
that should be documented as a new board revision decision rather than silently
reversing this one.

## 9. Decision

For the next 1-2 days, the correct engineering move is:

- preserve the current decoder core
- add an ingress adapter
- target `61.44 MHz -> 15.36 MHz`
- defer deeper DSP/rate redesign until after first hardware evidence exists
