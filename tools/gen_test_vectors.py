#!/usr/bin/env python3
"""Generate ADS-B I/Q test vectors for VHDL simulation.

Encodes known ADS-B messages as Mode-S PPM waveforms at a configurable
sample rate, with optional noise and multi-message support. Outputs binary
files compatible with the VHDL testbench file I/O (16-bit signed I, 16-bit
signed Q, little-endian).

Reference: contrib/bladeRF-adsb/matlab/adsb_out.m
"""

import argparse
import struct
from pathlib import Path

import numpy as np


# Mode-S preamble: 1010 0001 0100 0000 (8 us at 1 MHz chip rate = 16 chips)
PREAMBLE_CHIPS = np.array([1, 0, 1, 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0],
                          dtype=np.float64)

# CRC-24 polynomial for Mode-S
CRC_POLY = 0x1FFF409


def crc24(bits: np.ndarray) -> np.ndarray:
    """Compute CRC-24 for Mode-S and return 24-bit remainder as array.

    Standard CRC-24 computes remainder(M(x) * x^24 / G(x)), which requires
    appending 24 zero bits to the message before division. The resulting CRC,
    when appended to the original message, yields a zero remainder on
    verification (i.e., CRC(payload || CRC) = 0).
    """
    crc = 0
    for b in list(bits) + [0] * 24:
        crc = (crc << 1) | int(b)
        if crc & (1 << 24):
            crc ^= CRC_POLY
    return np.array([(crc >> (23 - i)) & 1 for i in range(24)], dtype=np.int32)


def hex_to_bits(hex_str: str) -> np.ndarray:
    """Convert hex string to numpy array of bits (MSB first)."""
    n_bits = len(hex_str) * 4
    val = int(hex_str, 16)
    return np.array([(val >> (n_bits - 1 - i)) & 1 for i in range(n_bits)],
                    dtype=np.int32)


def bits_to_ppm(bits: np.ndarray) -> np.ndarray:
    """Encode bit array as PPM chips (2 chips per bit)."""
    chips = np.zeros(len(bits) * 2, dtype=np.float64)
    for i, b in enumerate(bits):
        if b:
            chips[2 * i] = 1.0
            chips[2 * i + 1] = 0.0
        else:
            chips[2 * i] = 0.0
            chips[2 * i + 1] = 1.0
    return chips


def encode_adsb_message(hex_msg: str, sps: int = 8,
                        flip_bits: list[int] | None = None,
                        weak_bits: list[int] | None = None,
                        weak_contrast: float = 0.15) -> np.ndarray:
    """Encode hex ADS-B message (without CRC) into I/Q samples.

    Parameters
    ----------
    hex_msg : str
        Hex string of message content. For extended (DF>=16): 11 bytes
        (DF+CA+ICAO+ME = 88 bits). For short: 4 bytes (DF+CA+ICAO = 32 bits).
    sps : int
        Samples per PPM chip.
    flip_bits : list[int] or None
        Bit positions (0 = first transmitted bit) to flip AFTER CRC
        computation. Creates deterministic bit errors for testing the
        brute-force error correction in the bit_flipper module.
        NOTE: This produces high-confidence wrong BSDs that the bit_flipper
        CANNOT correct. Use weak_bits instead for error correction testing.
    weak_bits : list[int] or None
        Bit positions (0 = first transmitted bit) where BOTH PPM chips are
        set to equal power (weak_contrast), destroying the PPM modulation
        and creating BSD ≈ 0 (maximally ambiguous). The bit_flipper's
        smallest_bsds will rank these as the weakest bits, and brute-force
        search over all 32 combinations of flipping these positions will
        find the one that recovers a valid CRC.
    weak_contrast : float
        Power level for both chips of weak bits (default 0.7). Both chips
        are set to this value, so they look identical to the BSD calculator.
        Values around 0.6-0.8 keep both chips in the typeA classification
        range (power between 0.25*RPL and 1.75*RPL), ensuring the BSD
        calculator sees two "signal present" chips and produces BSD ≈ 0.

    Returns
    -------
    np.ndarray
        Complex I/Q samples (float64).
    """
    msg_bits = hex_to_bits(hex_msg)
    crc_bits = crc24(msg_bits)
    payload = np.concatenate([msg_bits, crc_bits])

    if flip_bits:
        for pos in flip_bits:
            payload[pos] ^= 1

    data_chips = bits_to_ppm(payload)

    # Destroy PPM modulation at weak_bits positions to create BSD ≈ 0.
    # Both chips are set to the same power level (weak_contrast), so the
    # BSD calculator can't distinguish them. This makes these the weakest
    # bits in smallest_bsds, and the bit_flipper will brute-force all 32
    # combinations to find the one that passes CRC.
    if weak_bits:
        for pos in weak_bits:
            data_chips[2 * pos] = weak_contrast
            data_chips[2 * pos + 1] = weak_contrast

    frame_chips = np.concatenate([PREAMBLE_CHIPS, data_chips])
    frame_up = np.repeat(frame_chips, sps)

    # Apply pulse-shaping filter to simulate analog frontend bandwidth.
    # Without this, the signal is a perfect step function and the VHDL
    # edge detector (which looks for monotonic slopes) won't trigger.
    # A half-chip-width (~sps/2) raised-cosine-ish filter creates
    # realistic rise/fall times at chip transitions.
    filt_len = max(sps // 2, 3)
    filt = np.hanning(filt_len)
    filt /= filt.sum()
    frame_up = np.convolve(frame_up, filt, mode='same')

    # Complex I/Q (signal on both I and Q, like bladeRF MATLAB model)
    iq = frame_up + 1j * frame_up
    return iq


def generate_test_file(
    messages: list[dict],
    output_path: Path,
    sps: int = 8,
    noise_amplitude: float = 0.0,
    dead_air_samples: int = 1000,
    amplitude: float = 0.5,
    seed: int = 42,
) -> None:
    """Generate a binary I/Q test vector file."""
    rng = np.random.default_rng(seed)

    encoded = []
    for msg in messages:
        iq = encode_adsb_message(msg['hex'], sps=sps,
                                 flip_bits=msg.get('flip_bits', None),
                                 weak_bits=msg.get('weak_bits', None),
                                 weak_contrast=msg.get('weak_contrast', 0.15))
        msg_amp = msg.get('amplitude', amplitude)
        iq *= msg_amp
        offset = msg.get('offset', None)
        encoded.append((iq, offset))

    if encoded[0][1] is None:
        total_len = dead_air_samples
        offsets = []
        for iq, _ in encoded:
            offsets.append(total_len)
            total_len += len(iq) + dead_air_samples
    else:
        max_end = 0
        offsets = []
        for iq, offset in encoded:
            offsets.append(offset)
            end = offset + len(iq)
            if end > max_end:
                max_end = end
        total_len = max_end + dead_air_samples

    output = np.zeros(total_len, dtype=np.complex128)
    for (iq, _), offset in zip(encoded, offsets):
        end = offset + len(iq)
        output[offset:end] += iq

    if noise_amplitude > 0:
        noise = rng.normal(0, noise_amplitude, total_len) + \
                1j * rng.normal(0, noise_amplitude, total_len)
        output += noise

    scale = 2047.0
    i_samples = np.clip(np.round(output.real * scale), -2048, 2047).astype(np.int16)
    q_samples = np.clip(np.round(output.imag * scale), -2048, 2047).astype(np.int16)

    with open(output_path, 'wb') as f:
        for i_val, q_val in zip(i_samples, q_samples):
            f.write(struct.pack('<hh', int(i_val), int(q_val)))

    n_bytes = total_len * 4
    print(f"Wrote {output_path}: {total_len} samples, {n_bytes} bytes")
    for i, msg in enumerate(messages):
        print(f"  Message {i}: {msg['hex']} at offset {offsets[i]}")


def main():
    parser = argparse.ArgumentParser(description='Generate ADS-B test vectors')
    parser.add_argument('--output-dir', type=Path, default=Path('.'),
                        help='Output directory for test vector files')
    parser.add_argument('--sps', type=int, default=8,
                        help='Samples per chip (default: 8)')
    args = parser.parse_args()

    args.output_dir.mkdir(parents=True, exist_ok=True)

    # Signal amplitude: 0.3 keeps I²+Q² within 24-bit signed range even
    # when summed over the preamble detector's 5-sample accumulation window.
    # At amp=0.3: I/Q ≈ 614, power ≈ 754k, 5×power ≈ 3.77M < 8.39M (24-bit max).
    # At amp=0.5: I/Q ≈ 1024, power ≈ 2.1M, 5×power ≈ 10.5M → OVERFLOW!
    AMP = 0.3

    # Test vector 1: Single clean extended message
    # DF17 CA5 ICAO:75804B ME:580FF2CF7E9BA6
    generate_test_file(
        messages=[{'hex': '8D75804B580FF2CF7E9BA6'}],
        output_path=args.output_dir / 'single_clean.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        amplitude=AMP,
    )

    # Test vector 2: Single message with noise
    generate_test_file(
        messages=[{'hex': '8D75804B580FF2CF7E9BA6'}],
        output_path=args.output_dir / 'single_noisy.dat',
        sps=args.sps,
        noise_amplitude=0.02,
        amplitude=AMP,
    )

    # Test vector 3: Two non-overlapping messages
    generate_test_file(
        messages=[
            {'hex': '8D75804B580FF2CF7E9BA6'},
            {'hex': '8D4840D6202CC371C32CE0'},
        ],
        output_path=args.output_dir / 'two_sequential.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        amplitude=AMP,
    )

    # Test vector 4: Two overlapping messages (collision test)
    msg1_len = (16 + 112 * 2) * args.sps
    generate_test_file(
        messages=[
            {'hex': '8D75804B580FF2CF7E9BA6', 'offset': 1000, 'amplitude': AMP},
            {'hex': '8D4840D6202CC371C32CE0', 'offset': 1000 + msg1_len // 2,
             'amplitude': AMP * 0.6},
        ],
        output_path=args.output_dir / 'collision.dat',
        sps=args.sps,
        noise_amplitude=0.01,
        amplitude=AMP,
    )

    # Test vector 5: Short message (DF=0)
    generate_test_file(
        messages=[{'hex': '02E19504'}],
        output_path=args.output_dir / 'single_short.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        amplitude=AMP,
    )

    # Test vector 6: Busy traffic — 8 sequential extended messages
    # Exercises multiple decoder slots and decoder recycling.
    # Uses 5000-sample gaps (~312 µs) to allow false-triggered decoders
    # to expire before the next real preamble arrives.
    # All are DF17 (0x8D) with different ICAO addresses and ME fields.
    generate_test_file(
        messages=[
            {'hex': '8D75804B580FF2CF7E9BA6'},   # ICAO 75804B — airborne position
            {'hex': '8D4840D6202CC371C32CE0'},   # ICAO 4840D6 — airborne position
            {'hex': '8DA7C83E990C1E0E08042C'},   # ICAO A7C83E — airborne velocity
            {'hex': '8D3C6586F8230006004AB8'},   # ICAO 3C6586 — aircraft ID
            {'hex': '8D7C1A25E19B6800000000'},   # ICAO 7C1A25 — extended squitter
            {'hex': '8D40621D58C382D690C8AC'},   # ICAO 40621D — airborne position
            {'hex': '8D4CA251204994B1C36E60'},   # ICAO 4CA251 — airborne position
            {'hex': '8D800A47F82300020049B8'},   # ICAO 800A47 — aircraft ID
        ],
        output_path=args.output_dir / 'busy_traffic.dat',
        sps=args.sps,
        noise_amplitude=0.0,
        dead_air_samples=5000,
        amplitude=AMP,
    )

    # Test vector 7: Busy traffic with light noise
    generate_test_file(
        messages=[
            {'hex': '8D75804B580FF2CF7E9BA6'},
            {'hex': '8D4840D6202CC371C32CE0'},
            {'hex': '8DA7C83E990C1E0E08042C'},
            {'hex': '8D3C6586F8230006004AB8'},
            {'hex': '8D7C1A25E19B6800000000'},
            {'hex': '8D40621D58C382D690C8AC'},
            {'hex': '8D4CA251204994B1C36E60'},
            {'hex': '8D800A47F82300020049B8'},
        ],
        output_path=args.output_dir / 'busy_noisy.dat',
        sps=args.sps,
        noise_amplitude=0.01,
        dead_air_samples=5000,
        amplitude=AMP,
    )

    # Test vector 8-11: Error correction stress tests
    # Increasing noise levels to force 1-5 bit errors and exercise the
    # brute-force bit flipper. Same 8 messages at each noise level.
    # Multiple seeds per level give different noise realisations.
    stress_msgs = [
        {'hex': '8D75804B580FF2CF7E9BA6'},
        {'hex': '8D4840D6202CC371C32CE0'},
        {'hex': '8DA7C83E990C1E0E08042C'},
        {'hex': '8D3C6586F8230006004AB8'},
        {'hex': '8D40621D58C382D690C8AC'},
        {'hex': '8D4CA251204994B1C36E60'},
        {'hex': '8D800A47F82300020049B8'},
        {'hex': '8D7C1A25E19B6800000000'},
    ]
    for noise, label in [(0.06, 'mild'), (0.10, 'moderate'), (0.14, 'heavy')]:
        generate_test_file(
            messages=stress_msgs,
            output_path=args.output_dir / f'stress_{label}.dat',
            sps=args.sps,
            noise_amplitude=noise,
            dead_air_samples=5000,
            amplitude=AMP,
            seed=100,
        )

    # Test vectors 12-16: Weak-BSD error correction tests
    # Each message has specific bit positions with reduced chip contrast,
    # creating weak BSDs that decode wrong. The bit_flipper's smallest_bsds
    # module should rank these as the weakest bits, and brute-force search
    # should find the correct combination to pass CRC.
    #
    # NOTE: We do NOT use flip_bits here. Flipping bits in the digital domain
    # creates high-confidence WRONG BSDs (same |BSD| magnitude, wrong sign).
    # The bit_flipper targets the 5 weakest |BSD| values, so confidently-wrong
    # bits won't be among them. Instead, weak_bits reduces the PPM chip
    # contrast so the BSD magnitude is small AND the decision goes wrong.
    #
    # Bit positions are in the ME field (bits 32-87) to avoid DF/CA fields
    # which could affect message type detection.
    MSG = '8D75804B580FF2CF7E9BA6'
    weak_tests = [
        # (label, weak_bits, chip_level, description)
        ('weak1', [44],                 0.7, '1-bit ambiguous at bit 44'),
        ('weak2', [36, 67],             0.7, '2-bit ambiguous at bits 36, 67'),
        ('weak3', [35, 52, 78],         0.7, '3-bit ambiguous at bits 35, 52, 78'),
        ('weak4', [34, 48, 65, 83],     0.7, '4-bit ambiguous at bits 34, 48, 65, 83'),
        ('weak5', [33, 42, 55, 70, 85], 0.7, '5-bit ambiguous — worst case for brute force'),
    ]
    for label, weak_pos, chip_level, desc in weak_tests:
        generate_test_file(
            messages=[{'hex': MSG, 'weak_bits': weak_pos,
                       'weak_contrast': chip_level}],
            output_path=args.output_dir / f'{label}.dat',
            sps=args.sps,
            noise_amplitude=0.0,
            dead_air_samples=5000,
            amplitude=AMP,
        )
        print(f'    {desc}')

    print("\nAll test vectors generated.")


if __name__ == '__main__':
    main()
