#!/usr/bin/env python3
"""Convert HackRF raw captures to VHDL testbench format.

HackRF captures are 8-bit signed I/Q interleaved at 16 MSPS.
The VHDL testbench expects 16-bit signed I/Q little-endian pairs.

Capture command (10 seconds at 16 MSPS, 1090 MHz):
    hackrf_transfer -r capture.raw -f 1090000000 -s 16000000 -n 160000000

Convert to testbench format:
    uv run python hackrf_capture.py capture.raw --output ../hdl/sim/vectors/live.dat

Trim to a specific sample range (e.g. around a known message):
    uv run python hackrf_capture.py capture.raw --start 1000000 --length 100000 --output trimmed.dat

List detected power peaks (to find messages):
    uv run python hackrf_capture.py capture.raw --scan
"""

import argparse
import struct
from pathlib import Path

import numpy as np


def load_hackrf(path: Path) -> np.ndarray:
    """Load HackRF raw capture as complex float64 array."""
    raw = np.fromfile(path, dtype=np.int8)
    i_samples = raw[0::2].astype(np.float64)
    q_samples = raw[1::2].astype(np.float64)
    return i_samples + 1j * q_samples


def write_testbench(iq: np.ndarray, output_path: Path) -> None:
    """Write I/Q data in VHDL testbench format (16-bit signed LE pairs)."""
    # Scale 8-bit range (-128..127) to 12-bit range (-2048..2047)
    # to match the ADC resolution we're targeting
    scale = 2047.0 / 127.0
    i_out = np.clip(np.round(iq.real * scale), -2048, 2047).astype(np.int16)
    q_out = np.clip(np.round(iq.imag * scale), -2048, 2047).astype(np.int16)

    # Interleave I/Q into a single array for bulk write
    interleaved = np.empty(len(iq) * 2, dtype=np.int16)
    interleaved[0::2] = i_out
    interleaved[1::2] = q_out
    interleaved.tofile(output_path)

    n_bytes = len(iq) * 4
    duration_ms = len(iq) / 16e6 * 1000
    print(f"Wrote {output_path}: {len(iq)} samples, {n_bytes} bytes, {duration_ms:.1f} ms")


def scan_for_peaks(iq: np.ndarray, threshold_db: float = 10.0,
                   window: int = 1000) -> list[dict]:
    """Find power peaks that likely contain ADS-B messages.

    Returns list of {offset, power_db, duration_samples} dicts.
    """
    power = np.abs(iq) ** 2
    # Sliding window average
    kernel = np.ones(window) / window
    avg_power = np.convolve(power, kernel, mode='same')
    noise_floor = np.median(avg_power)

    if noise_floor <= 0:
        noise_floor = 1e-10

    power_db = 10 * np.log10(avg_power / noise_floor)

    # Find regions above threshold
    above = power_db > threshold_db
    transitions = np.diff(above.astype(int))
    starts = np.where(transitions == 1)[0]
    ends = np.where(transitions == -1)[0]

    # Handle edge cases
    if above[0]:
        starts = np.concatenate([[0], starts])
    if above[-1]:
        ends = np.concatenate([ends, [len(above) - 1]])

    peaks = []
    for s, e in zip(starts, ends):
        peak_idx = s + np.argmax(power_db[s:e])
        peaks.append({
            'offset': int(s),
            'peak_offset': int(peak_idx),
            'power_db': float(power_db[peak_idx]),
            'duration_samples': int(e - s),
        })

    return peaks


def main():
    parser = argparse.ArgumentParser(
        description='Convert HackRF captures to VHDL testbench format')
    parser.add_argument('input', type=Path, help='HackRF raw capture file')
    parser.add_argument('--output', '-o', type=Path,
                        help='Output testbench file')
    parser.add_argument('--start', type=int, default=0,
                        help='Start sample offset')
    parser.add_argument('--length', type=int, default=0,
                        help='Number of samples (0 = all)')
    parser.add_argument('--scan', action='store_true',
                        help='Scan for power peaks and print locations')
    parser.add_argument('--threshold', type=float, default=10.0,
                        help='Peak detection threshold in dB above noise (default: 10)')
    args = parser.parse_args()

    print(f"Loading {args.input}...")
    iq = load_hackrf(args.input)
    print(f"  {len(iq)} samples, {len(iq)/16e6*1000:.1f} ms")

    if args.scan:
        print(f"\nScanning for peaks (threshold: {args.threshold} dB)...")
        peaks = scan_for_peaks(iq, threshold_db=args.threshold)
        print(f"Found {len(peaks)} peaks:\n")
        # Mode-S extended message: 120 µs = 1920 samples at 16 MSPS
        for i, p in enumerate(peaks):
            dur_us = p['duration_samples'] / 16.0
            msg_type = "extended?" if 100 < dur_us < 150 else \
                       "short?" if 50 < dur_us < 80 else ""
            print(f"  Peak {i:3d}: offset {p['offset']:>10d}  "
                  f"+{p['power_db']:.1f} dB  "
                  f"{dur_us:.0f} µs  {msg_type}")
        return

    # Trim if requested
    if args.start > 0 or args.length > 0:
        end = args.start + args.length if args.length > 0 else len(iq)
        iq = iq[args.start:end]
        print(f"  Trimmed to samples {args.start}–{end}: {len(iq)} samples")

    if args.output:
        write_testbench(iq, args.output)
    else:
        print("No --output specified, use --scan or --output to do something")


if __name__ == '__main__':
    main()
