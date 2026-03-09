#!/usr/bin/env python3
"""Scan raw IQ data for Mode-S messages using software preamble detection.

Supports:
- 2-channel little-endian I16+Q16 files (legacy testbench format)
- 4-channel little-endian I16 files from Pluto `cf-ad9361-lpc`, selecting
  one I/Q pair for analysis

Usage:
    uv run python scan_iq.py <iq_file> [--pair 0|1] [--sample-rate HZ] [--ref <output.log>]

The --ref flag writes a dump1090-compatible reference file (*hex; format)
for use with compare_results.py.
"""

import argparse
import sys
import numpy as np


def load_iq(path: str, pair: int = 0, fmt: str = "auto") -> np.ndarray:
    """Load raw IQ file, return power (I^2 + Q^2) as float64.

    If the file contains 4 interleaved int16 channels, `pair=0` selects
    channels 0/1 and `pair=1` selects channels 2/3.
    """
    raw = np.fromfile(path, dtype=np.int16)
    if fmt == "auto":
        # Default to 2-channel IQ for host captures. The old Pluto DMA dump
        # was 4-channel, but that format is ambiguous whenever the total int16
        # count also happens to be divisible by 4.
        fmt = "iq16"

    if fmt == "pluto4":
        if raw.size % 4 != 0:
            raise ValueError(f"expected 4-channel int16 input, got {raw.size} samples")
        data = raw.reshape(-1, 4)
        if pair not in (0, 1):
            raise ValueError(f"pair must be 0 or 1 for 4-channel input, got {pair}")
        base = pair * 2
        i = data[:, base].astype(np.float64)
        q = data[:, base + 1].astype(np.float64)
    elif fmt == "iq16":
        if raw.size % 2 != 0:
            raise ValueError(f"expected I16/Q16 interleaved input, got {raw.size} samples")
        i = raw[0::2].astype(np.float64)
        q = raw[1::2].astype(np.float64)
    else:
        raise ValueError(f"unsupported format {fmt!r}")
    power = i * i + q * q
    return power


def bits_to_hex(bits: list[int]) -> str:
    """Convert list of 0/1 bits to hex string."""
    hex_str = ""
    for i in range(0, len(bits), 4):
        nibble = bits[i] * 8 + bits[i+1] * 4 + bits[i+2] * 2 + bits[i+3]
        hex_str += f"{nibble:X}"
    return hex_str


MODE_S_POLY = 0xFFF409


def bits_to_int(bits: list[int]) -> int:
    value = 0
    for bit in bits:
        value = (value << 1) | (bit & 1)
    return value


def modes_crc_ok(bits: list[int]) -> bool:
    """Return True if the final 24 bits match the Mode S CRC remainder."""
    n = len(bits)
    if n not in (56, 112):
        return False
    reg = bits_to_int(bits)
    for shift in range(n - 24):
        if reg & (1 << (n - 1 - shift)):
            reg ^= MODE_S_POLY << (n - 25 - shift)
    return (reg & 0xFFFFFF) == 0


def df_from_hex(hex_msg: str) -> int:
    return int(hex_msg[:2], 16) >> 3


def scan_for_messages(power: np.ndarray, sps: int = 8) -> list[dict]:
    """Scan power array for Mode-S preambles and attempt decode."""
    n = len(power)
    results = []
    skip_until = 0

    # Preamble pulse positions (in samples)
    p0 = 0
    p1 = 2 * sps   # 16
    p2 = 7 * sps   # 56
    p3 = 9 * sps   # 72

    # Quiet zone positions
    q_a = 1 * sps   # 8  (between p0 and p1)
    q_b = 4 * sps   # 32 (between p1 and p2)

    # Data starts after preamble (8 us = 16 chips = 128 samples)
    data_start = 16 * sps  # 128

    # Window for pulse energy (5 samples)
    w = 5

    for i in range(n - data_start - 112 * 2 * sps):
        if i < skip_until:
            continue

        # Quick check: pulse 0 and pulse 2 must be above noise
        s0 = np.sum(power[i + p0 : i + p0 + w])
        s2 = np.sum(power[i + p2 : i + p2 + w])
        if s0 < 25000 or s2 < 25000:
            continue

        # Full check: all 4 pulses above threshold
        s1 = np.sum(power[i + p1 : i + p1 + w])
        s3 = np.sum(power[i + p3 : i + p3 + w])
        if s1 < 25000 or s3 < 25000:
            continue

        # Quiet zone check
        qa = np.sum(power[i + q_a : i + q_a + w])
        qb = np.sum(power[i + q_b : i + q_b + w])
        if qa > s0 / 4 or qb > s2 / 4:
            continue

        # Extract bits using PPM demodulation
        # Each bit is 2 chips (2*sps samples). High-then-low = 1, low-then-high = 0.
        bit_start = i + data_start
        bits = []
        for b in range(112):
            chip_hi = np.sum(power[bit_start + b * 2 * sps : bit_start + b * 2 * sps + sps])
            chip_lo = np.sum(power[bit_start + b * 2 * sps + sps : bit_start + (b + 1) * 2 * sps])
            bits.append(1 if chip_hi > chip_lo else 0)

        # Try long message (112 bits = 14 bytes) first
        hex_msg = bits_to_hex(bits)
        df = df_from_hex(hex_msg)

        if df in (16, 17, 18, 19, 20, 21):
            if modes_crc_ok(bits):
                results.append({
                    "sample": i,
                    "time_us": i / 16.0,
                    "hex": hex_msg,
                    "df": df,
                    "bits": 112,
                    "crc_ok": True,
                })
                skip_until = i + data_start + 112 * 2 * sps
                continue

        # Try short message (56 bits = 7 bytes)
        hex_short = bits_to_hex(bits[:56])
        df_short = df_from_hex(hex_short)
        if df_short in (0, 4, 5, 11):
            if modes_crc_ok(bits[:56]):
                results.append({
                    "sample": i,
                    "time_us": i / 16.0,
                    "hex": hex_short,
                    "df": df_short,
                    "bits": 56,
                    "crc_ok": True,
                })
                skip_until = i + data_start + 56 * 2 * sps
                continue

    return results


def main():
    parser = argparse.ArgumentParser(description="Scan IQ data for Mode-S messages")
    parser.add_argument("iq_file", help="Raw IQ file")
    parser.add_argument(
        "--format",
        choices=("auto", "iq16", "pluto4"),
        default="auto",
        help="Input format: host-captured I16/Q16 or legacy 4-channel Pluto dump",
    )
    parser.add_argument("--pair", type=int, default=0,
                        help="I/Q pair to analyze for 4-channel input (0 or 1, default: 0)")
    parser.add_argument("--sample-rate", type=float, default=16e6,
                        help="sample rate in Hz for reporting and SPS derivation (default: 16e6)")
    parser.add_argument("--ref", metavar="FILE",
                        help="Write dump1090-compatible reference (*hex;) for compare_results.py")
    args = parser.parse_args()

    path = args.iq_file
    print(f"Loading {path}...")
    power = load_iq(path, pair=args.pair, fmt=args.format)
    sps = int(round(args.sample_rate / 2e6))
    if sps <= 0:
        raise ValueError(f"invalid sample rate {args.sample_rate}")
    print(f"  pair={args.pair} samples={len(power)} sample_rate={args.sample_rate:.0f} SPS={sps}")
    print(f"  duration={len(power)/args.sample_rate*1000:.1f} ms")
    print(f"  power stats: min={power.min():.0f} max={power.max():.0f} "
          f"mean={power.mean():.0f} median={np.median(power):.0f}")

    print("Scanning for Mode-S messages...")
    msgs = scan_for_messages(power, sps=sps)

    print(f"\nFound {len(msgs)} messages:\n")
    print(f"  {'Sample':>10}  {'Time':>10}  {'DF':>3}  {'Bits':>4}  Message")
    print(f"  {'-'*10}  {'-'*10}  {'---':>3}  {'----':>4}  -------")
    for m in msgs:
        print(f"  {m['sample']:>10}  {m['time_us']:>8.1f}us  {m['df']:>3}  {m['bits']:>4}  {m['hex']}")

    if not msgs:
        print("  (none found)")
        threshold = np.percentile(power, 99.9)
        peaks = np.where(power > threshold)[0]
        if len(peaks) > 0:
            print(f"  Top 0.1% power threshold: {threshold:.0f}")
            print(f"  {len(peaks)} samples above threshold")
            gaps = np.diff(peaks)
            cluster_starts = [peaks[0]]
            for j, g in enumerate(gaps):
                if g > 1000:
                    cluster_starts.append(peaks[j + 1])
            print(f"  {len(cluster_starts)} signal clusters at samples: "
                  + ", ".join(str(s) for s in cluster_starts[:20]))

    # Write reference file for compare_results.py
    if args.ref and msgs:
        with open(args.ref, "w") as f:
            for m in msgs:
                f.write(f"*{m['hex']};\n")
        print(f"\nWrote {len(msgs)} messages to {args.ref}")


if __name__ == "__main__":
    main()
