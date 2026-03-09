# Ideas from contrib/1090-receiver-fpga

Analysis of Dabin Zhang's Cyclone III (Altera) 1090 MHz receiver design.
Same 16 MSPS sample rate as ours, but simpler decoder (hard-decision only,
4 parallel decoders, no error correction). Our pipeline is more advanced
in the areas that matter.

## Mode-A/C Decoding

The most interesting unique feature. `decoder_ac.vhd` decodes legacy
Mode-A/C transponder replies — a completely different protocol from Mode-S:

- 90-sample shift register for the A/C frame
- F1/F2 framing bit validation (both must be '1')
- X-bit and inter-pulse spacing checks (must all be '0')
- Extracts 4×4-bit words from specific pulse positions
- Separate SNR threshold from Mode-S
- Togglable at runtime

Mode-A/C is still used by older aircraft, military, and GA in Australian
airspace. Would let us see squawk codes and Mode-C altitude from non-ADS-B
targets. The implementation is compact (~78 lines of core logic) but needs
its own detection path since pulse timing differs from Mode-S.

## FIR Matched Filter

They apply a 6-tap asymmetric FIR to raw ADC samples before preamble
detection. Coefficients (from `multiplier_tap0..5`):

    [-1, -6, -5, +23, +78, +127]

At 16 MSPS, 6 taps spans 375 ns — roughly one PPM chip leading edge. Acts
as a pulse-edge matched filter: boosts real pulse step-response, suppresses
broadband noise.

Not directly applicable to us — we process |I|+|Q| power, not raw
amplitude, and the AD9363 already has internal FIR decimation. Could be
worth investigating a matched filter in the power domain if we want to push
sensitivity further.

## Output Frame Deduplication

`ft245_interface.vhd` compares each decoded frame's data bits against the
previous frame and suppresses exact duplicates (ignoring timestamp).

We already handle this via peak detection + 140 µs holdoff + first-free
assignment, but a cheap content-based dedup in plane-feeder (Go side)
before Beast output would be a nice safety net. Hash the 112-bit frame,
suppress exact matches within a short time window.

## Runtime-Configurable Detection Thresholds

Their `control_unit.vhd` lets the host adjust at runtime:
- SNR threshold ("SS0"-"SSO" commands)
- 4 vs 8 preamble check groups ("DC4"/"DC8")
- Preamble-to-data timing offset ("SW0"-"SWO")

Validates our existing plan to make POWER_THRESHOLD and the ratio constants
AXI-writable post-bring-up.

## Not Useful

- Altera LPM megafunctions — vendor-locked, not portable to Xilinx
- FT245 USB interface — we use AXI/Linux
- AD9238 ADC interface — we use AD9363 RF frontend
- Their decoder quality — hard-decision only, no error correction
