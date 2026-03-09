# Pipeline Improvement Ideas

Collected during simulation testing (2026-03-11). The full decode pipeline
works end-to-end (8/8 messages decoded on clean traffic), but adding noise
reveals that false preamble detections saturate all 8 decoder slots, starving
real messages.

## The Problem

Data transitions from a decoded (or decoding) message create power patterns
that pass the preamble correlator. At noise_amplitude >= 0.06, thousands of
false triggers consume every decoder. Each false trigger holds a slot for
~254 µs (sample collection + 32 CRC iterations on garbage), so real messages
arriving during that window get dropped.

## Idea 1: Quiet-Zone Verification in Preamble Detector

**Impact: High | Effort: Small**

The Mode-S preamble has specific quiet zones between pulse pairs:
- Gap A: samples 24–55 (between pulses 1–2 and 3–4, 2.0 µs gap)
- Gap B: samples 80–127 (after pulse 4, before data starts)

The current detector only checks that pulse positions have HIGH power. It
never checks that quiet zones have LOW power. Adding a sliding-window sum
over a quiet zone and requiring it to be BELOW a threshold would reject
most false triggers from data content.

The 128-sample shift register (`power_grid`) already contains the data —
just need another accumulator and a comparison.

## Idea 2: DF Field Validation in Bit Flipper

**Impact: Medium | Effort: Small**

After the first byte of BSDs arrives in `smallest_bsds`, check whether the
Downlink Format field is valid before starting the CRC brute force. Valid
DFs: 0, 4, 5, 11, 16, 17, 18, 19, 20, 21, 24. Any other value means the
message is definitely not Mode-S — abort immediately and free the decoder
in ~1 µs instead of ~142 µs.

This doesn't prevent the decoder from being allocated (the preamble still
fires), but it drastically reduces how long a false trigger holds a slot.

Could be implemented as an early-exit check in the `bit_flipper` FSM after
`WAIT_FOR_SMALLEST`, or as a new state between `WAIT_FOR_SMALLEST` and
`GEN_FLIP_MASK`.

## Idea 3: DF Pre-Check via Extended Shift Register

**Impact: High | Effort: Medium**

Extend the preamble detector's shift register from 128 to ~256 samples so
it can see the first 5–8 data bits (the DF field) before committing a
decoder. Only allocate a decoder when:
1. Preamble correlation passes (existing check)
2. Quiet zones are quiet (Idea 1)
3. DF field in the buffered data is valid

This is the cleanest solution — invalid messages never touch a decoder at
all — but requires a deeper buffer and a mini-demodulator in the preamble
detector to extract the DF bits from the PPM-encoded data in the shift
register.

## Idea 4: Reduce EXTENDED_MESSAGE_LENGTH Safety Margin

**Impact: Low | Effort: Trivial**

The sample collection window in `message_decoder.vhd` has a 2× safety
margin: `EXTENDED_MESSAGE_LENGTH = 112 * SPS * SPB * 2 = 3584` samples,
but the actual message only needs 1792 samples (112 bits × 16 samples/bit).

Reducing to 1.25× (~2240 samples) would cut sample collection time from
224 µs to 140 µs. This doesn't prevent false triggers but reduces how long
they hold a decoder slot. Low impact because the CRC brute force time
(~30 µs) is added on top regardless.

Note: the bladeRF original may have used 2× for clock uncertainty with a
real ADC. Our fixed-rate system doesn't need that margin.

## Idea 5: Smarter Decoder Assignment

**Impact: Medium | Effort: Medium**

Currently the preamble detector uses strict round-robin assignment. If the
target decoder is busy, the detection is dropped even if other decoders are
free (the `coulda` counter tracks this). Switching to a "first free" policy
would improve utilisation under load. The round-robin was likely chosen for
simplicity and fair ageing, but under heavy false-trigger load, any-free
would be more robust.
