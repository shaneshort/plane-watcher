#!/usr/bin/env python3
"""Resample raw IQ captures to a target I16/Q16 sample rate.

Input format:
- 4 interleaved little-endian int16 channels from `cf-ad9361-lpc`
- `--pair 0` uses channels 0/1 as I/Q
- `--pair 1` uses channels 2/3 as I/Q
- plain little-endian interleaved I16/Q16 files

Output format:
- little-endian I16,Q16 interleaved
- intended for downstream tools such as scan_iq.py or dump1090
"""

from __future__ import annotations

import argparse
from pathlib import Path

import numpy as np


def linear_resample(x: np.ndarray, in_rate: float, out_rate: float) -> np.ndarray:
    if len(x) == 0 or in_rate <= 0 or out_rate <= 0:
        return np.empty(0, dtype=np.float64)
    out_len = int(np.floor(len(x) * out_rate / in_rate))
    if out_len <= 1:
        return np.empty(0, dtype=np.float64)
    src_idx = np.arange(len(x), dtype=np.float64)
    dst_idx = np.arange(out_len, dtype=np.float64) * (in_rate / out_rate)
    return np.interp(dst_idx, src_idx, x.astype(np.float64))


def load_iq(raw: np.ndarray, fmt: str, pair: int) -> tuple[np.ndarray, np.ndarray, int]:
    if fmt == "pluto4":
        if raw.size % 4 != 0:
            raise SystemExit(f"expected 4-channel int16 input, got {raw.size} samples")
        data = raw.reshape(-1, 4)
        base = pair * 2
        return data[:, base], data[:, base + 1], len(data)

    if fmt == "iq16":
        if raw.size % 2 != 0:
            raise SystemExit(f"expected interleaved I16/Q16 input, got {raw.size} samples")
        return raw[0::2], raw[1::2], raw.size // 2

    raise SystemExit(f"unsupported format: {fmt}")


def main() -> None:
    ap = argparse.ArgumentParser(description="Resample raw capture to target-rate I16/Q16")
    ap.add_argument("input", help="input raw capture")
    ap.add_argument("output", help="output I16/Q16 file")
    ap.add_argument(
        "--format",
        default="pluto4",
        choices=("pluto4", "iq16"),
        help="input format: 4-channel Pluto dump or plain I16/Q16",
    )
    ap.add_argument("--pair", type=int, default=0, choices=(0, 1), help="I/Q pair to extract")
    ap.add_argument("--input-rate", type=float, default=30_720_000, help="input sample rate in Hz")
    ap.add_argument("--output-rate", type=float, default=16_000_000, help="output sample rate in Hz")
    args = ap.parse_args()

    raw = np.fromfile(args.input, dtype="<i2")
    i, q, frame_count = load_iq(raw, args.format, args.pair)

    i_res = linear_resample(i, args.input_rate, args.output_rate)
    q_res = linear_resample(q, args.input_rate, args.output_rate)

    n = min(len(i_res), len(q_res))
    iq = np.empty(n * 2, dtype="<i2")
    iq[0::2] = np.clip(np.rint(i_res[:n]), -32768, 32767).astype("<i2")
    iq[1::2] = np.clip(np.rint(q_res[:n]), -32768, 32767).astype("<i2")

    out = Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    iq.tofile(out)

    extra = f" pair={args.pair}" if args.format == "pluto4" else ""
    print(f"input_frames={frame_count}{extra} input_rate={args.input_rate:.0f} format={args.format}")
    print(f"output_iq_samples={n} output_rate={args.output_rate:.0f}")
    print(f"wrote {out}")


if __name__ == "__main__":
    main()
