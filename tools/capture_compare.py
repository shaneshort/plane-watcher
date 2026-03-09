#!/usr/bin/env python3
"""Compare FPGA beast output against dump1090 ground truth from a dual capture.

Takes a beast binary file and a raw IQ file captured simultaneously,
decodes the IQ through dump1090, and diffs the two frame sets.

Examples:
    uv run python tools/capture_compare.py \\
        --beast /tmp/dual_g24_beast.bin \\
        --iq /tmp/dual_g24_iq.raw

    uv run python tools/capture_compare.py \\
        --beast /tmp/dual_g24_beast.bin \\
        --iq /tmp/dual_g24_iq.raw \\
        --output /tmp/compare_results.csv
"""

from __future__ import annotations

import argparse
import csv
import json
import subprocess
import sys
import tempfile
from collections import Counter
from pathlib import Path

import numpy as np


# ---------------------------------------------------------------------------
# Beast binary parser
# ---------------------------------------------------------------------------

def parse_beast(data: bytes) -> list[dict]:
    """Parse a Beast binary stream into a list of frame dicts."""
    frames = []
    i = 0
    while i < len(data):
        if data[i] != 0x1A:
            i += 1
            continue
        if i + 1 >= len(data):
            break
        msg_type = data[i + 1]
        if msg_type == 0x1A:
            i += 2
            continue

        if msg_type == 0x32:
            payload_len = 7
        elif msg_type == 0x33:
            payload_len = 14
        elif msg_type == 0x34:
            payload_len = 14
        elif msg_type == 0x35:
            payload_len = 21
        else:
            i += 2
            continue

        # Read header (6-byte timestamp + 1-byte signal) + payload,
        # un-escaping 0x1A 0x1A → 0x1A.
        need = 6 + 1 + payload_len
        raw = bytearray()
        j = i + 2
        while len(raw) < need and j < len(data):
            if data[j] == 0x1A and j + 1 < len(data) and data[j + 1] == 0x1A:
                raw.append(0x1A)
                j += 2
            else:
                raw.append(data[j])
                j += 1

        if len(raw) < need:
            break

        ts = int.from_bytes(raw[0:6], "big")
        sig = raw[6]
        payload = raw[7 : 7 + payload_len]
        payload_hex = payload.hex().upper()

        if msg_type in (0x34,):
            # Status frame — skip for comparison purposes.
            i = j
            continue

        df = (payload[0] >> 3) & 0x1F
        frame = {
            "source": "fpga",
            "type": "short" if msg_type == 0x32 else "long",
            "df": df,
            "payload": payload_hex,
            "signal": sig,
            "timestamp": ts,
        }
        frames.append(frame)
        i = j

    return frames


# ---------------------------------------------------------------------------
# IQ resampling (inline, avoids subprocess for resample_pluto_capture.py)
# ---------------------------------------------------------------------------

def resample_iq(
    input_path: Path,
    output_path: Path,
    in_rate: float = 30_720_000,
    out_rate: float = 2_400_000,
) -> int:
    """Resample interleaved I16/Q16 from in_rate to out_rate. Returns output sample count."""
    raw = np.fromfile(input_path, dtype="<i2")
    if raw.size % 2 != 0:
        raise SystemExit(f"expected interleaved I16/Q16, got {raw.size} samples")

    i_in = raw[0::2].astype(np.float64)
    q_in = raw[1::2].astype(np.float64)

    out_len = int(np.floor(len(i_in) * out_rate / in_rate))
    if out_len <= 1:
        raise SystemExit("output too short after resampling")

    src_idx = np.arange(len(i_in), dtype=np.float64)
    dst_idx = np.arange(out_len, dtype=np.float64) * (in_rate / out_rate)

    i_out = np.interp(dst_idx, src_idx, i_in)
    q_out = np.interp(dst_idx, src_idx, q_in)

    iq = np.empty(out_len * 2, dtype="<i2")
    iq[0::2] = np.clip(np.rint(i_out), -32768, 32767).astype("<i2")
    iq[1::2] = np.clip(np.rint(q_out), -32768, 32767).astype("<i2")
    iq.tofile(output_path)
    return out_len


# ---------------------------------------------------------------------------
# dump1090 decoder
# ---------------------------------------------------------------------------

def run_dump1090(sc16_path: Path, dump1090_bin: Path) -> list[dict]:
    """Run dump1090 on an SC16 file and return decoded frames."""
    result = subprocess.run(
        [str(dump1090_bin), "--ifile", str(sc16_path), "--iformat", "SC16", "--raw", "--fix"],
        capture_output=True,
        text=True,
        timeout=120,
    )
    frames = []
    for line in result.stdout.splitlines():
        line = line.strip()
        if not line.startswith("*") or not line.endswith(";"):
            continue
        hex_msg = line[1:-1].upper()
        raw = bytes.fromhex(hex_msg)
        df = (raw[0] >> 3) & 0x1F
        frame = {
            "source": "dump1090",
            "type": "short" if len(raw) == 7 else "long",
            "df": df,
            "payload": hex_msg,
        }
        frames.append(frame)
    return frames


# ---------------------------------------------------------------------------
# Frame comparison
# ---------------------------------------------------------------------------

def compare_frames(
    fpga_frames: list[dict],
    d1090_frames: list[dict],
) -> dict:
    """Compare FPGA and dump1090 frame sets by payload match."""
    fpga_payloads = Counter(f["payload"] for f in fpga_frames)
    d1090_payloads = Counter(f["payload"] for f in d1090_frames)

    matched_payloads = set(fpga_payloads) & set(d1090_payloads)
    fpga_only_payloads = set(fpga_payloads) - set(d1090_payloads)
    d1090_only_payloads = set(d1090_payloads) - set(fpga_payloads)

    # Count matched frames (min of the two counts for each payload).
    matched_count = sum(min(fpga_payloads[p], d1090_payloads[p]) for p in matched_payloads)

    # Build per-DF breakdown.
    def df_for_payload(payload: str, frames: list[dict]) -> int:
        for f in frames:
            if f["payload"] == payload:
                return f["df"]
        return -1

    fpga_only_by_df: Counter[int] = Counter()
    for p in fpga_only_payloads:
        df = df_for_payload(p, fpga_frames)
        fpga_only_by_df[df] += fpga_payloads[p]

    d1090_only_by_df: Counter[int] = Counter()
    for p in d1090_only_payloads:
        df = df_for_payload(p, d1090_frames)
        d1090_only_by_df[df] += d1090_payloads[p]

    # Check for FPGA duplicate payloads within 140 µs (potential re-trigger duplicates).
    # Group FPGA frames by payload and check TOA gaps.
    duplicates = 0
    by_payload: dict[str, list[int]] = {}
    for f in fpga_frames:
        by_payload.setdefault(f["payload"], []).append(f.get("timestamp", 0))
    for payload, toas in by_payload.items():
        if len(toas) > 1:
            toas_sorted = sorted(toas)
            for k in range(1, len(toas_sorted)):
                # 140 µs at 12 MHz beast clock = 1,680 ticks.
                if toas_sorted[k] - toas_sorted[k - 1] < 1680:
                    duplicates += 1

    return {
        "fpga_total": sum(fpga_payloads.values()),
        "d1090_total": sum(d1090_payloads.values()),
        "matched": matched_count,
        "fpga_only": sum(fpga_payloads[p] for p in fpga_only_payloads),
        "d1090_only": sum(d1090_payloads[p] for p in d1090_only_payloads),
        "fpga_only_by_df": dict(fpga_only_by_df),
        "d1090_only_by_df": dict(d1090_only_by_df),
        "fpga_duplicates_140us": duplicates,
        "fpga_unique_payloads": len(fpga_payloads),
        "d1090_unique_payloads": len(d1090_payloads),
    }


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def find_dump1090() -> Path:
    """Locate the dump1090 binary relative to the repo root."""
    candidates = [
        Path(__file__).parent.parent / "contrib" / "dump1090" / "dump1090",
        Path("contrib/dump1090/dump1090"),
    ]
    for c in candidates:
        if c.is_file():
            return c
    raise SystemExit("dump1090 not found — expected at contrib/dump1090/dump1090")


def main() -> int:
    ap = argparse.ArgumentParser(description="Compare FPGA beast output against dump1090 ground truth")
    ap.add_argument("--beast", required=True, help="Beast binary capture file")
    ap.add_argument("--iq", required=True, help="Raw IQ capture file (I16/Q16 interleaved)")
    ap.add_argument("--sample-rate", type=float, default=30_720_000, help="IQ sample rate in Hz")
    ap.add_argument("--dump1090", help="Path to dump1090 binary (auto-detected if omitted)")
    ap.add_argument("--output", help="CSV output file (summary to stderr regardless)")
    ap.add_argument("--json", action="store_true", help="Output results as JSON instead of table")
    args = ap.parse_args()

    dump1090_bin = Path(args.dump1090) if args.dump1090 else find_dump1090()
    if not dump1090_bin.is_file():
        print(f"dump1090 not found at {dump1090_bin}", file=sys.stderr)
        return 1

    beast_path = Path(args.beast)
    iq_path = Path(args.iq)

    # Parse beast binary.
    print(f"Parsing beast: {beast_path} ({beast_path.stat().st_size} bytes)", file=sys.stderr)
    beast_data = beast_path.read_bytes()
    fpga_frames = parse_beast(beast_data)
    print(f"  FPGA frames: {len(fpga_frames)}", file=sys.stderr)

    # Resample IQ and run dump1090.
    with tempfile.NamedTemporaryFile(suffix=".raw", delete=True) as tmp:
        sc16_path = Path(tmp.name)
        print(f"Resampling IQ: {iq_path} → {sc16_path} (2.4 MSPS)", file=sys.stderr)
        n_samples = resample_iq(iq_path, sc16_path, args.sample_rate, 2_400_000)
        print(f"  Output: {n_samples} IQ samples", file=sys.stderr)

        print(f"Running dump1090: {dump1090_bin}", file=sys.stderr)
        d1090_frames = run_dump1090(sc16_path, dump1090_bin)
        print(f"  dump1090 frames: {len(d1090_frames)}", file=sys.stderr)

    # Compare.
    results = compare_frames(fpga_frames, d1090_frames)

    # Output.
    if args.json:
        print(json.dumps(results, indent=2))
    else:
        print(file=sys.stderr)
        print("=== Capture Comparison ===", file=sys.stderr)
        print(f"  FPGA total:      {results['fpga_total']:>6d}  ({results['fpga_unique_payloads']} unique)", file=sys.stderr)
        print(f"  dump1090 total:  {results['d1090_total']:>6d}  ({results['d1090_unique_payloads']} unique)", file=sys.stderr)
        print(f"  Matched:         {results['matched']:>6d}", file=sys.stderr)
        print(f"  FPGA only:       {results['fpga_only']:>6d}  {dict(results['fpga_only_by_df']) or ''}", file=sys.stderr)
        print(f"  dump1090 only:   {results['d1090_only']:>6d}  {dict(results['d1090_only_by_df']) or ''}", file=sys.stderr)
        print(f"  FPGA duplicates: {results['fpga_duplicates_140us']:>6d}  (same payload within 140 µs)", file=sys.stderr)

        if results["d1090_total"] > 0:
            pct = results["matched"] / results["d1090_total"] * 100
            print(f"  Match rate:      {pct:>5.1f}%", file=sys.stderr)

    if args.output:
        out_path = Path(args.output)
        out_path.parent.mkdir(parents=True, exist_ok=True)
        with out_path.open("w", newline="") as fh:
            writer = csv.DictWriter(fh, fieldnames=sorted(results.keys()))
            writer.writeheader()
            # Flatten dict values for CSV.
            row = {}
            for k, v in results.items():
                row[k] = json.dumps(v) if isinstance(v, dict) else v
            writer.writerow(row)
        print(f"\nCSV written to {out_path}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
