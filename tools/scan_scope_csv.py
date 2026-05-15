#!/usr/bin/env python3
"""Scan scope CSV captures for Mode-S / ADS-B messages.

This is intended for envelope captures from detector outputs such as AD8313.
Unlike dump1090-style IQ tools, this script works directly from amplitude
versus time samples exported by an oscilloscope.

Usage:
    uv run python tools/scan_scope_csv.py capture.csv
    uv run python tools/scan_scope_csv.py capture.csv --ref out.log
"""

from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pyModeS as pms


MODE_S_POLY = 0xFFF409


@dataclass
class DecodeResult:
    source: str
    sample: int
    time_us: float
    bits: int
    df: int
    hex_msg: str
    crc_ok: bool
    score: float
    details: str


def sample_rate_to_sps(sample_rate: float) -> int:
    sps = int(round(sample_rate / 2e6))
    if sps < 2:
        raise ValueError(f"sample rate too low for ADS-B demodulation: {sample_rate}")
    return sps


def load_scope_csv(path: str) -> tuple[np.ndarray, np.ndarray]:
    """Load a scope CSV containing time and voltage columns after metadata rows."""
    times_list: list[float] = []
    volts_list: list[float] = []
    data_started = False

    with open(path, "r", encoding="utf-8", newline="") as handle:
        reader = csv.reader(handle)
        for row in reader:
            if len(row) < 2:
                continue

            if not data_started:
                if row[0].strip() == "Second" and row[1].strip() == "Value":
                    data_started = True
                continue

            try:
                times_list.append(float(row[0]))
                volts_list.append(float(row[1]))
            except ValueError as exc:
                raise ValueError(
                    f"unexpected non-numeric data row in {path}: {row[:2]}"
                ) from exc

    if not data_started:
        raise ValueError(f"could not find 'Second,Value' header in {path}")
    if not times_list:
        raise ValueError(f"no sample rows found in {path}")

    times = np.asarray(times_list, dtype=np.float64)
    volts = np.asarray(volts_list, dtype=np.float64)
    return times, volts


def expand_inputs(paths: list[str]) -> list[str]:
    expanded: list[str] = []
    for raw in paths:
        path = Path(raw)
        if path.is_dir():
            expanded.extend(str(candidate) for candidate in sorted(path.glob("*.csv")))
            continue
        expanded.append(str(path))
    if not expanded:
        raise ValueError("no CSV inputs found")
    return expanded


def normalize_envelope(volts: np.ndarray, polarity: str) -> tuple[np.ndarray, str]:
    """Center, orient, and clip an envelope trace so pulses are positive."""
    baseline = np.median(volts)
    centered = volts - baseline

    if polarity == "auto":
        pos_span = np.percentile(centered, 99.5)
        neg_span = abs(np.percentile(centered, 0.5))
        chosen = "positive" if pos_span >= neg_span else "negative"
    else:
        chosen = polarity

    if chosen == "negative":
        centered = -centered

    centered -= np.percentile(centered, 10)
    centered = np.maximum(centered, 0.0)
    return centered, chosen


def bits_to_hex(bits: list[int]) -> str:
    hex_str = ""
    for i in range(0, len(bits), 4):
        nibble = bits[i] * 8 + bits[i + 1] * 4 + bits[i + 2] * 2 + bits[i + 3]
        hex_str += f"{nibble:X}"
    return hex_str


def bits_to_int(bits: list[int]) -> int:
    value = 0
    for bit in bits:
        value = (value << 1) | (bit & 1)
    return value


def modes_crc_ok(bits: list[int]) -> bool:
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


def validate_message(hex_msg: str) -> tuple[bool, str]:
    """Return pyModeS CRC result plus a short summary."""
    try:
        crc_ok = (pms.crc(hex_msg) == 0)
        df = df_from_hex(hex_msg)

        if df in (17, 18):
            icao = pms.icao(hex_msg)
            tc = pms.adsb.typecode(hex_msg)
            parts = [f"ICAO={icao}", f"TC={tc}"]
            if 1 <= tc <= 4:
                parts.append(f"call={pms.adsb.callsign(hex_msg)}")
            elif 9 <= tc <= 18:
                parts.append("airborne_pos")
            elif tc == 19:
                parts.append("velocity")
            elif 5 <= tc <= 8:
                parts.append("surface_pos")
            return crc_ok, " ".join(parts)

        if df == 11:
            return crc_ok, f"ICAO={pms.icao(hex_msg)}"

        return crc_ok, ""
    except Exception as exc:
        return False, f"error: {exc}"


def mean_slice(trace: np.ndarray, start: int, width: int) -> float:
    lo = max(0, start)
    hi = min(len(trace), start + width)
    if hi <= lo:
        return 0.0
    return float(np.mean(trace[lo:hi]))


def scan_for_messages(
    source: str,
    env: np.ndarray,
    sample_rate: float,
    threshold_scale: float = 5.0,
    quiet_ratio: float = 0.45,
) -> list[DecodeResult]:
    """Scan envelope samples for Mode-S preambles and PPM payloads."""
    sps = sample_rate_to_sps(sample_rate)

    # Preamble timing in half-microsecond chips.
    p0 = 0
    p1 = 2 * sps
    p2 = 7 * sps
    p3 = 9 * sps
    q0 = 1 * sps
    q1 = 4 * sps
    data_start = 16 * sps

    pulse_w = max(1, int(round(0.35 * sps)))
    chip_w = sps

    noise_floor = float(np.median(env))
    spread = float(np.percentile(env, 99) - np.percentile(env, 50))
    min_pulse = noise_floor + max(spread / threshold_scale, 1e-6)

    min_needed = data_start + 112 * 2 * sps
    results: list[DecodeResult] = []
    seen: set[tuple[int, str]] = set()
    skip_until = 0

    for i in range(0, len(env) - min_needed):
        if i < skip_until:
            continue

        s0 = mean_slice(env, i + p0, pulse_w)
        s1 = mean_slice(env, i + p1, pulse_w)
        s2 = mean_slice(env, i + p2, pulse_w)
        s3 = mean_slice(env, i + p3, pulse_w)
        if min(s0, s1, s2, s3) < min_pulse:
            continue

        qa = mean_slice(env, i + q0, pulse_w)
        qb = mean_slice(env, i + q1, pulse_w)
        pulse_mean = (s0 + s1 + s2 + s3) / 4.0
        if qa > pulse_mean * quiet_ratio or qb > pulse_mean * quiet_ratio:
            continue

        bits: list[int] = []
        confidence = 0.0
        bit_start = i + data_start
        for b in range(112):
            a = mean_slice(env, bit_start + b * 2 * sps, chip_w)
            c = mean_slice(env, bit_start + b * 2 * sps + sps, chip_w)
            bits.append(1 if a >= c else 0)
            confidence += abs(a - c)

        long_hex = bits_to_hex(bits)
        long_df = df_from_hex(long_hex)
        long_crc = modes_crc_ok(bits)

        short_bits = bits[:56]
        short_hex = bits_to_hex(short_bits)
        short_df = df_from_hex(short_hex)
        short_crc = modes_crc_ok(short_bits)

        chosen: DecodeResult | None = None
        if long_df in (16, 17, 18, 19, 20, 21) and long_crc:
            crc_ok, details = validate_message(long_hex)
            chosen = DecodeResult(
                source=source,
                sample=i,
                time_us=i / sample_rate * 1e6,
                bits=112,
                df=long_df,
                hex_msg=long_hex,
                crc_ok=crc_ok,
                score=confidence / 112.0,
                details=details,
            )
            skip_until = i + data_start + 112 * 2 * sps
        elif short_df in (0, 4, 5, 11) and short_crc:
            crc_ok, details = validate_message(short_hex)
            chosen = DecodeResult(
                source=source,
                sample=i,
                time_us=i / sample_rate * 1e6,
                bits=56,
                df=short_df,
                hex_msg=short_hex,
                crc_ok=crc_ok,
                score=confidence / 56.0,
                details=details,
            )
            skip_until = i + data_start + 56 * 2 * sps

        if chosen is None:
            continue

        dedupe_key = (int(round(chosen.time_us)), chosen.hex_msg)
        if dedupe_key in seen:
            continue
        seen.add(dedupe_key)
        results.append(chosen)

    return results


def plot_capture(
    csv_file: str,
    times: np.ndarray,
    env: np.ndarray,
    result: DecodeResult,
    sample_rate: float,
    output_path: str,
    window_us: float,
) -> None:
    """Write an annotated plot around a decoded frame."""
    try:
        import matplotlib
        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
    except ImportError as exc:
        raise SystemExit(
            "matplotlib is required for --plot; reinstall tools deps with uv"
        ) from exc

    sps = sample_rate_to_sps(sample_rate)
    preamble_samples = 16 * sps
    payload_samples = result.bits * 2 * sps
    frame_start = result.sample
    frame_end = frame_start + preamble_samples + payload_samples

    half_window = int(round(window_us * 1e-6 * sample_rate / 2.0))
    start = max(0, frame_start - half_window)
    end = min(len(env), frame_end + half_window)

    t_us = (times[start:end] - times[frame_start]) * 1e6
    y = env[start:end]

    fig, ax = plt.subplots(figsize=(14, 6), constrained_layout=True)
    ax.plot(t_us, y, color="#1f77b4", linewidth=1.4, label="Envelope")

    preamble_offsets = [0, 1.0, 3.5, 4.5]
    for off in preamble_offsets:
        ax.axvline(off, color="#d62728", linestyle="--", linewidth=1.0, alpha=0.8)
    ax.axvspan(0, 8.0, color="#d62728", alpha=0.08, label="Preamble")

    for bit in range(result.bits):
        bit_start_us = 8.0 + bit
        ax.axvline(bit_start_us, color="#7f7f7f", linewidth=0.35, alpha=0.18)
    ax.axvspan(8.0, 8.0 + result.bits, color="#2ca02c", alpha=0.06, label="Payload")
    ax.axvline(8.0 + result.bits, color="#2ca02c", linestyle="--", linewidth=1.0, alpha=0.8)

    header = f"DF{result.df} {result.bits}-bit {result.hex_msg}"
    subtitle = f"{Path(csv_file).name}  t0={result.time_us:.1f} us  score={result.score:.5f}"
    if result.details:
        subtitle = f"{subtitle}  {result.details}"
    ax.set_title(f"{header}\n{subtitle}")
    ax.set_xlabel("Time relative to detected preamble start (us)")
    ax.set_ylabel("Normalized envelope")
    ax.grid(True, alpha=0.25)
    ax.legend(loc="upper right")

    ymax = float(np.max(y)) if len(y) else 1.0
    text_y = ymax * 0.92 if ymax > 0 else 0.1
    ax.text(0.1, text_y, "Preamble start", color="#d62728", fontsize=9, va="top")
    ax.text(8.1, text_y, "Payload start", color="#2ca02c", fontsize=9, va="top")
    ax.text(8.0 + result.bits + 0.1, text_y, "Payload end", color="#2ca02c", fontsize=9, va="top")

    fig.savefig(output_path, dpi=160)
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser(description="Scan scope CSV captures for Mode-S / ADS-B messages")
    parser.add_argument(
        "csv_files",
        nargs="+",
        help="One or more scope CSV files, or directories containing CSV files",
    )
    parser.add_argument(
        "--polarity",
        choices=("auto", "positive", "negative"),
        default="auto",
        help="Detector polarity; 'negative' flips downward pulses upward (default: auto)",
    )
    parser.add_argument(
        "--sample-rate",
        type=float,
        help="Override sample rate in Hz; by default this is inferred from the CSV timestamps",
    )
    parser.add_argument(
        "--threshold-scale",
        type=float,
        default=5.0,
        help="Lower values require stronger pulses; higher values are more permissive (default: 5.0)",
    )
    parser.add_argument(
        "--quiet-ratio",
        type=float,
        default=0.45,
        help="Maximum quiet-zone level as a fraction of preamble pulse level (default: 0.45)",
    )
    parser.add_argument(
        "--ref",
        metavar="FILE",
        help="Write dump1090-style reference lines (*HEX;) for recovered messages",
    )
    parser.add_argument(
        "--plot",
        metavar="PNG",
        help="Write an annotated PNG plot around the first recovered message",
    )
    parser.add_argument(
        "--plot-index",
        type=int,
        default=0,
        help="Which recovered message to plot when multiple are found (default: 0)",
    )
    parser.add_argument(
        "--plot-window-us",
        type=float,
        default=40.0,
        help="Total plot window in microseconds around the frame (default: 40)",
    )
    args = parser.parse_args()

    csv_files = expand_inputs(args.csv_files)
    if args.plot and len(csv_files) != 1:
        raise SystemExit("--plot currently requires exactly one input CSV")

    all_results: list[DecodeResult] = []
    plot_context: tuple[str, np.ndarray, np.ndarray, float] | None = None

    for csv_file in csv_files:
        times, volts = load_scope_csv(csv_file)
        dt = np.diff(times)
        inferred_sample_rate = 1.0 / float(np.median(dt))
        sample_rate = args.sample_rate or inferred_sample_rate

        env, chosen_polarity = normalize_envelope(volts, args.polarity)
        results = scan_for_messages(
            csv_file,
            env,
            sample_rate=sample_rate,
            threshold_scale=args.threshold_scale,
            quiet_ratio=args.quiet_ratio,
        )
        all_results.extend(results)

        print(f"Loaded {csv_file}")
        print(f"  samples={len(times)}")
        print(f"  duration={times[-1] - times[0]:.9f}s")
        print(f"  inferred_sample_rate={inferred_sample_rate:.0f} Hz")
        print(f"  sample_rate={sample_rate:.0f} Hz")
        print(f"  polarity={chosen_polarity}")
        print(f"  envelope stats: min={env.min():.6f} max={env.max():.6f} mean={env.mean():.6f}")

        print(f"\nFound {len(results)} messages:\n")
        print(f"  {'Sample':>8}  {'Time':>9}  {'DF':>3}  {'Bits':>4}  {'CRC':>3}  Message")
        print(f"  {'-' * 8}  {'-' * 9}  {'-' * 3}  {'-' * 4}  {'-' * 3}  -------")
        for res in results:
            crc_text = "ok" if res.crc_ok else "no"
            print(f"  {res.sample:>8}  {res.time_us:>7.1f}us  {res.df:>3}  {res.bits:>4}  {crc_text:>3}  {res.hex_msg}")
            if res.details:
                print(f"    {res.details}")

        if not results:
            threshold = np.percentile(env, 99.9)
            peaks = np.where(env >= threshold)[0]
            print("  (none found)")
            print(f"  99.9th percentile threshold={threshold:.6f}, peaks={len(peaks)}")
            if len(peaks):
                print(f"  strongest region starts near sample {int(peaks[0])} ({peaks[0] / sample_rate * 1e6:.1f} us)")

        print()
        if len(csv_files) == 1:
            plot_context = (csv_file, times, env, sample_rate)

    unique_messages = len({res.hex_msg for res in all_results})
    print(f"Scanned {len(csv_files)} file(s); recovered {len(all_results)} frame(s), {unique_messages} unique message(s).")

    if args.ref and all_results:
        with open(args.ref, "w", encoding="ascii") as handle:
            for res in all_results:
                handle.write(f"*{res.hex_msg};\n")
        print(f"Wrote {len(all_results)} messages to {args.ref}")

    if args.plot:
        if not all_results:
            raise SystemExit("--plot requested but no messages were recovered")
        if args.plot_index < 0 or args.plot_index >= len(all_results):
            raise SystemExit(f"--plot-index out of range: {args.plot_index} (found {len(all_results)} messages)")
        if plot_context is None:
            raise SystemExit("internal error: plot context missing")
        csv_file, times, env, sample_rate = plot_context
        plot_capture(
            csv_file,
            times,
            env,
            all_results[args.plot_index],
            sample_rate,
            args.plot,
            args.plot_window_us,
        )
        print(f"Wrote plot to {args.plot}")


if __name__ == "__main__":
    main()
