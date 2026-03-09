#!/usr/bin/env python3
"""Sweep preamble detector knobs and capture stats at each setting.

Sets knob values via the plane-feeder HTTP API, waits for stats to
settle, then captures a delta over the dwell period. Restores original
values when done.

Examples:
    uv run python tools/knob_sweep.py --holdoff 128,512,1024,2240
    uv run python tools/knob_sweep.py --quiet-score-shift 1,2,3 --dwell 60
    uv run python tools/knob_sweep.py --holdoff 512,1024 --output /tmp/sweep.csv
"""

from __future__ import annotations

import argparse
import csv
import json
import sys
import time
import urllib.request
import urllib.error
from pathlib import Path


# ---------------------------------------------------------------------------
# API helpers
# ---------------------------------------------------------------------------

def api_get(host: str, path: str) -> dict:
    """GET a JSON endpoint from plane-feeder."""
    url = f"http://{host}{path}"
    with urllib.request.urlopen(url, timeout=10) as resp:
        return json.loads(resp.read())


def api_post(host: str, path: str, value: int) -> dict:
    """POST a JSON value to a plane-feeder endpoint."""
    url = f"http://{host}{path}"
    body = json.dumps({"value": value}).encode()
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())


def get_stats(host: str) -> dict:
    """Read current stats from the plane-feeder API."""
    return api_get(host, "/api/stats")


def get_drops(host: str) -> dict:
    """Read current drop summary from the plane-feeder API."""
    return api_get(host, "/api/drops")


def read_current_knobs(host: str) -> dict[str, int]:
    """Read the current knob values from the stats API.

    The config register values may be in the top-level stats response
    (detector field) or in the debug counters. If neither is available,
    returns -1 for unknown values — the sweep still works because the
    API writes are authoritative.
    """
    stats = get_stats(host)
    # Try the detector config field first (always-on path).
    det = stats.get("detector", {})
    # Fall back to debug counters (requires deep debug build).
    debug = stats.get("debug", {})
    merged = {**debug, **det}
    return {
        "holdoff": merged.get("holdoff", -1),
        "quiet_score_shift": merged.get("quiet_score_shift", -1),
        "snr_ratio_shift": merged.get("snr_ratio_shift", -1),
    }


def set_knob(host: str, name: str, value: int) -> None:
    """Set a single detector knob via the API."""
    endpoints = {
        "holdoff": "/api/detector/holdoff",
        "quiet_score_shift": "/api/detector/quiet-score-shift",
        "snr_ratio_shift": "/api/detector/snr-ratio-shift",
    }
    path = endpoints.get(name)
    if not path:
        raise ValueError(f"unknown knob: {name}")
    api_post(host, path, value)


# ---------------------------------------------------------------------------
# Sweep logic
# ---------------------------------------------------------------------------

def capture_delta(host: str, dwell: float, settle: float) -> dict:
    """Capture stats delta over a dwell period (after settling)."""
    # Settle: let the new setting take effect.
    time.sleep(settle)

    # Snapshot at start.
    stats_start = get_stats(host)
    drops_start = get_drops(host)

    # Dwell.
    time.sleep(dwell)

    # Snapshot at end.
    stats_end = get_stats(host)
    drops_end = get_drops(host)

    # Compute deltas.
    msg_delta = stats_end.get("msg_count", 0) - stats_start.get("msg_count", 0)
    drop_delta = stats_end.get("drop_count", 0) - stats_start.get("drop_count", 0)

    # Per-reason drop deltas from the recent buffer (approximate — the
    # buffer is a fixed-size ring, so we use totals where available).
    by_reason_end = drops_end.get("by_reason", {})

    return {
        "msg_delta": msg_delta,
        "drop_delta": drop_delta,
        "msg_rate": round(msg_delta / dwell, 1) if dwell > 0 else 0,
        "drop_rate": round(drop_delta / dwell, 1) if dwell > 0 else 0,
        "total_rate": round((msg_delta + drop_delta) / dwell, 1) if dwell > 0 else 0,
        "drop_reasons": by_reason_end,
        "icao_count": stats_end.get("icao_count", 0),
        "dwell_s": dwell,
    }


def run_sweep(
    host: str,
    knob_name: str,
    values: list[int],
    dwell: float,
    settle: float,
    other_knobs: dict[str, int],
) -> list[dict]:
    """Sweep a single knob through the given values and capture stats."""
    results = []
    total = len(values)

    for idx, val in enumerate(values):
        print(
            f"  [{idx + 1}/{total}] {knob_name}={val}  "
            f"(settle {settle:.0f}s + dwell {dwell:.0f}s)",
            file=sys.stderr,
        )
        set_knob(host, knob_name, val)
        delta = capture_delta(host, dwell, settle)

        row = {
            "knob": knob_name,
            "value": val,
            **{f"other_{k}": v for k, v in other_knobs.items()},
            **delta,
        }
        results.append(row)

        # Progress output.
        print(
            f"         msg_rate={delta['msg_rate']}/s  "
            f"drop_rate={delta['drop_rate']}/s  "
            f"total={delta['total_rate']}/s  "
            f"aircraft={delta['icao_count']}",
            file=sys.stderr,
        )

    return results


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def parse_int_list(s: str) -> list[int]:
    """Parse a comma-separated list of integers."""
    return [int(x.strip()) for x in s.split(",") if x.strip()]


def main() -> int:
    ap = argparse.ArgumentParser(description="Sweep preamble detector knobs and capture stats")
    ap.add_argument("--host", default="pluto.local:8080", help="plane-feeder host:port")
    ap.add_argument("--dwell", type=float, default=30, help="measurement period per setting in seconds")
    ap.add_argument("--settle", type=float, default=5, help="settling time after knob change in seconds")
    ap.add_argument("--holdoff", help="comma-separated holdoff values to sweep (e.g., 128,512,1024,2240)")
    ap.add_argument("--quiet-score-shift", help="comma-separated quiet_score_shift values (e.g., 1,2,3)")
    ap.add_argument("--snr-ratio-shift", help="comma-separated snr_ratio_shift values (e.g., 0,1,2)")
    ap.add_argument("--output", help="CSV output file")
    ap.add_argument("--json", action="store_true", help="output JSON instead of table")
    args = ap.parse_args()

    sweeps: list[tuple[str, list[int]]] = []
    if args.holdoff:
        sweeps.append(("holdoff", parse_int_list(args.holdoff)))
    if args.quiet_score_shift:
        sweeps.append(("quiet_score_shift", parse_int_list(args.quiet_score_shift)))
    if args.snr_ratio_shift:
        sweeps.append(("snr_ratio_shift", parse_int_list(args.snr_ratio_shift)))

    if not sweeps:
        print("No knobs specified. Use --holdoff, --quiet-score-shift, or --snr-ratio-shift.", file=sys.stderr)
        return 1

    # Check connectivity.
    try:
        stats = get_stats(args.host)
    except Exception as e:
        print(f"Cannot reach plane-feeder at {args.host}: {e}", file=sys.stderr)
        return 1

    print(f"Connected to {args.host} (uptime={stats.get('uptime_s', '?')}s)", file=sys.stderr)

    # Read current knob values to restore later.
    original = read_current_knobs(args.host)
    print(f"Current knobs: {original}", file=sys.stderr)

    all_results: list[dict] = []

    try:
        for knob_name, values in sweeps:
            # Build the "other knobs" context (values of knobs not being swept).
            other_knobs = {k: v for k, v in original.items() if k != knob_name and v >= 0}
            print(f"\n=== Sweeping {knob_name}: {values} ===", file=sys.stderr)
            results = run_sweep(args.host, knob_name, values, args.dwell, args.settle, other_knobs)
            all_results.extend(results)
    finally:
        # Restore original knob values.
        print("\nRestoring original knobs...", file=sys.stderr)
        for k, v in original.items():
            if v >= 0:
                try:
                    set_knob(args.host, k, v)
                except Exception as e:
                    print(f"  WARNING: failed to restore {k}={v}: {e}", file=sys.stderr)
        print("Done.", file=sys.stderr)

    # Output results.
    if args.json:
        print(json.dumps(all_results, indent=2))
    else:
        # Print a summary table to stderr.
        print("\n=== Sweep Summary ===", file=sys.stderr)
        print(f"{'Knob':<22s} {'Value':>6s} {'msg/s':>7s} {'drop/s':>7s} {'total/s':>8s} {'aircraft':>8s}", file=sys.stderr)
        print("-" * 65, file=sys.stderr)
        for row in all_results:
            print(
                f"{row['knob']:<22s} {row['value']:>6d} "
                f"{row['msg_rate']:>7.1f} {row['drop_rate']:>7.1f} "
                f"{row['total_rate']:>8.1f} {row['icao_count']:>8d}",
                file=sys.stderr,
            )

    if args.output:
        out_path = Path(args.output)
        out_path.parent.mkdir(parents=True, exist_ok=True)
        fieldnames = [
            "knob", "value", "msg_rate", "drop_rate", "total_rate",
            "msg_delta", "drop_delta", "icao_count", "dwell_s", "drop_reasons",
        ]
        # Include any other_* columns.
        extra = sorted({k for r in all_results for k in r if k.startswith("other_")})
        fieldnames = fieldnames[:2] + extra + fieldnames[2:]

        with out_path.open("w", newline="") as fh:
            writer = csv.DictWriter(fh, fieldnames=fieldnames, extrasaction="ignore")
            writer.writeheader()
            for row in all_results:
                flat = dict(row)
                if isinstance(flat.get("drop_reasons"), dict):
                    flat["drop_reasons"] = json.dumps(flat["drop_reasons"])
                writer.writerow(flat)
        print(f"CSV written to {out_path}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
