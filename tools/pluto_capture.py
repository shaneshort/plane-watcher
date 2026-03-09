#!/usr/bin/env python3
"""Capture raw IQ from a PlutoSDR to an interleaved little-endian I16/Q16 file.

Examples:
    uv run --with pyadi-iio python tools/pluto_capture.py
    uv run --with pyadi-iio python tools/pluto_capture.py --seconds 10 --output /tmp/pluto_10s_iq.raw
"""

from __future__ import annotations

import argparse
import json
import math
import sys
import time
from pathlib import Path

import numpy as np


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Capture raw Pluto IQ to disk")
    ap.add_argument("--uri", default="ip:pluto.local", help="IIO URI for Pluto")
    ap.add_argument("--rx-lo", type=float, default=1_090_000_000, help="RX LO in Hz")
    ap.add_argument("--sample-rate", type=float, default=30_720_000, help="Sample rate in Hz")
    ap.add_argument("--bandwidth", type=float, default=2_000_000, help="RF bandwidth in Hz")
    ap.add_argument(
        "--gain-mode",
        default="slow_attack",
        choices=["manual", "slow_attack", "fast_attack"],
        help="AD936x gain control mode",
    )
    ap.add_argument("--gain-db", type=float, default=60.0, help="Manual RX gain in dB")
    ap.add_argument("--channel", type=int, default=0, help="RX channel index")
    ap.add_argument("--buffer-size", type=int, default=262144, help="Samples per read")
    ap.add_argument("--seconds", type=float, default=10.0, help="Capture duration in seconds")
    ap.add_argument("--output", default="/tmp/pluto_capture_i16.raw", help="Output raw IQ file")
    ap.add_argument("--stats-out", help="Optional JSON file for capture summary stats")
    return ap.parse_args()


def open_pluto(uri: str):
    import adi

    try:
        return adi.Pluto(uri)
    except Exception:
        return adi.ad9361(uri=uri)


def to_i16_interleaved(iq: np.ndarray) -> np.ndarray:
    i = np.clip(np.rint(np.real(iq)), -32768, 32767).astype("<i2", copy=False)
    q = np.clip(np.rint(np.imag(iq)), -32768, 32767).astype("<i2", copy=False)
    out = np.empty(i.size * 2, dtype="<i2")
    out[0::2] = i
    out[1::2] = q
    return out


def capture_stats(iq: np.ndarray) -> dict[str, float]:
    i = np.real(iq).astype(np.float64, copy=False)
    q = np.imag(iq).astype(np.float64, copy=False)
    abs_iq = np.abs(np.concatenate((i, q)))
    power = np.abs(iq) ** 2
    peak = float(np.max(abs_iq)) if abs_iq.size else 0.0
    rms = float(np.sqrt(np.mean(power))) if power.size else 0.0
    clip_ratio = float(np.mean(abs_iq >= 2047.0)) if abs_iq.size else 0.0
    return {
        "samples": float(iq.size),
        "max_abs_code": peak,
        "rms_amplitude": rms,
        "crest_db": float(20.0 * np.log10(max(peak / max(rms, 1e-12), 1e-12))),
        "clip_ratio": clip_ratio,
        "p95_abs_code": float(np.percentile(abs_iq, 95.0)) if abs_iq.size else 0.0,
        "p99_abs_code": float(np.percentile(abs_iq, 99.0)) if abs_iq.size else 0.0,
        "p99_9_abs_code": float(np.percentile(abs_iq, 99.9)) if abs_iq.size else 0.0,
        "median_power": float(np.median(power)) if power.size else 0.0,
        "p99_power": float(np.percentile(power, 99.0)) if power.size else 0.0,
        "p99_9_power": float(np.percentile(power, 99.9)) if power.size else 0.0,
        "dc_i": float(np.mean(i)) if i.size else 0.0,
        "dc_q": float(np.mean(q)) if q.size else 0.0,
    }


def main() -> int:
    args = parse_args()

    try:
        sdr = open_pluto(args.uri)
    except Exception as exc:
        print(f"failed to open Pluto at {args.uri}: {exc}", file=sys.stderr)
        return 1

    sdr.sample_rate = int(args.sample_rate)
    sdr.rx_rf_bandwidth = int(args.bandwidth)
    sdr.rx_lo = int(args.rx_lo)
    sdr.rx_buffer_size = int(args.buffer_size)
    sdr.rx_enabled_channels = [int(args.channel)]
    sdr.gain_control_mode_chan0 = args.gain_mode
    if args.gain_mode == "manual":
        sdr.rx_hardwaregain_chan0 = float(args.gain_db)

    out = Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)

    target_samples = int(round(args.seconds * args.sample_rate))
    loops = max(1, math.ceil(target_samples / args.buffer_size))
    written_samples = 0
    started = time.time()
    stats_chunk = None

    print(f"Capturing from {args.uri}")
    print(f"  LO={int(args.rx_lo)} Hz sample_rate={int(args.sample_rate)} Hz bandwidth={int(args.bandwidth)} Hz")
    print(f"  gain_mode={args.gain_mode}" + (f" gain_db={args.gain_db:.1f}" if args.gain_mode == "manual" else ""))
    print(f"  target_duration={args.seconds:.2f} s target_samples={target_samples}")
    print(f"  buffer_size={args.buffer_size} loops={loops}")
    print(f"  output={out}")

    with out.open("wb") as fh:
        for idx in range(loops):
            data = sdr.rx()
            if isinstance(data, list):
                data = data[0]
            iq = np.asarray(data)
            if iq.size == 0:
                print(f"empty read at loop {idx}", file=sys.stderr)
                break

            remaining = target_samples - written_samples
            if remaining <= 0:
                break
            if iq.size > remaining:
                iq = iq[:remaining]

            raw = to_i16_interleaved(iq)
            fh.write(raw.tobytes())
            written_samples += iq.size
            stats_chunk = iq

            if idx == 0 or (idx + 1) % 10 == 0 or written_samples >= target_samples:
                elapsed = max(time.time() - started, 1e-6)
                rate = written_samples / elapsed / 1e6
                pct = written_samples / target_samples * 100.0
                print(
                    f"  loop {idx + 1}/{loops}: samples={written_samples}/{target_samples} "
                    f"({pct:.1f}%) effective_rate={rate:.2f} MSPS"
                )

    elapsed = time.time() - started
    size_mb = out.stat().st_size / (1024 * 1024)
    print(f"wrote {out}")
    print(f"  samples={written_samples}")
    print(f"  duration={written_samples / args.sample_rate:.3f} s")
    print(f"  file_size={size_mb:.1f} MiB")
    print(f"  elapsed={elapsed:.2f} s")
    if stats_chunk is not None:
        stats = capture_stats(stats_chunk)
        print(
            "  stats:"
            f" max_abs={stats['max_abs_code']:.1f}"
            f" clip_ratio={stats['clip_ratio'] * 100:.3f}%"
            f" p99_abs={stats['p99_abs_code']:.1f}"
            f" p99.9_abs={stats['p99_9_abs_code']:.1f}"
            f" crest={stats['crest_db']:.1f} dB"
        )
        if args.stats_out:
            stats_path = Path(args.stats_out)
            stats_path.parent.mkdir(parents=True, exist_ok=True)
            payload = {
                "uri": args.uri,
                "rx_lo_hz": int(args.rx_lo),
                "sample_rate_hz": int(args.sample_rate),
                "bandwidth_hz": int(args.bandwidth),
                "gain_mode": args.gain_mode,
                "gain_db": float(args.gain_db),
                "written_samples": written_samples,
                "capture_seconds": written_samples / args.sample_rate,
                "stats": stats,
            }
            stats_path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
            print(f"  stats_out={stats_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
