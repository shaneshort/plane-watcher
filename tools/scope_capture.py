#!/usr/bin/env python3
"""Capture Siglent SDS USBTMC waveforms into CSV.

Reads raw waveform bytes via `<channel>:WF? DAT2`, converts them to volts and
time using the scope's current settings, and writes one CSV per channel.
"""

from __future__ import annotations

import argparse
import csv
import os
import time
from pathlib import Path


class Scope:
    def __init__(self, path: str, timeout: float = 5.0, debug: bool = False) -> None:
        self.path = path
        self.timeout = timeout
        self.debug = debug

    def _read_response(self, binary: bool = False) -> bytes:
        deadline = time.time() + self.timeout
        out = b""
        with open(self.path, "r+b", buffering=0) as dev:
            while time.time() < deadline:
                chunk = os.read(dev.fileno(), 65536)
                if chunk:
                    out += chunk
                    if binary:
                        if b"#" in out:
                            idx = out.find(b"#")
                            if len(out) >= idx + 2:
                                ndigits = int(chr(out[idx + 1]))
                                if len(out) >= idx + 2 + ndigits:
                                    size = int(out[idx + 2 : idx + 2 + ndigits].decode("ascii"))
                                    total = idx + 2 + ndigits + size
                                    if len(out) >= total:
                                        return out[:total]
                    else:
                        if out.endswith(b"\n"):
                            return out
                else:
                    time.sleep(0.02)
        if not out:
            raise TimeoutError("scope response timed out")
        return out

    def query(self, cmd: str) -> str:
        with open(self.path, "r+b", buffering=0) as dev:
            dev.write((cmd + "\n").encode("ascii"))
        out = self._read_response(binary=False).decode("ascii", errors="replace").strip()
        if self.debug:
            print(f"# SCPI {cmd!r} -> {out!r}")
        return out

    def command(self, cmd: str) -> None:
        with open(self.path, "r+b", buffering=0) as dev:
            dev.write((cmd + "\n").encode("ascii"))
        if self.debug:
            print(f"# SCPI command {cmd!r}")

    def query_binary(self, cmd: str) -> bytes:
        with open(self.path, "r+b", buffering=0) as dev:
            dev.write((cmd + "\n").encode("ascii"))
        out = self._read_response(binary=True)
        if self.debug:
            print(f"# SCPI binary {cmd!r} -> {len(out)} bytes")
        return out


def parse_float(resp: str) -> float:
    token = resp.strip().split()[-1]
    token = token.rstrip("VSa/s")
    return float(token)


def parse_ieee_block(block: bytes) -> tuple[str, bytes]:
    hash_idx = block.find(b"#")
    if hash_idx < 0:
        raise ValueError("missing IEEE block header")
    prefix = block[:hash_idx].decode("ascii", errors="replace").strip()
    ndigits = int(chr(block[hash_idx + 1]))
    size = int(block[hash_idx + 2 : hash_idx + 2 + ndigits].decode("ascii"))
    start = hash_idx + 2 + ndigits
    payload = block[start : start + size]
    if len(payload) != size:
        raise ValueError(f"incomplete payload: expected {size}, got {len(payload)}")
    return prefix, payload


def signed8(v: int) -> int:
    return v - 256 if v > 127 else v


def main() -> int:
    ap = argparse.ArgumentParser(description="Capture scope waveforms over USBTMC")
    ap.add_argument("--scope", default="/dev/usbtmc3")
    ap.add_argument("--channels", default="1,2,3")
    ap.add_argument("--output-dir", default="/tmp/scope_capture")
    ap.add_argument("--arm", action="store_true", help="Arm single acquisition before reading")
    ap.add_argument("--debug", action="store_true")
    args = ap.parse_args()

    scope = Scope(args.scope, debug=args.debug)
    out_dir = Path(args.output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    if args.arm:
        scope.command("TRMD SINGLE")
        scope.command("ARM")
        scope.command("WAIT")

    sample_rate = parse_float(scope.query("SARA?").replace("SARA ", ""))
    tdiv = parse_float(scope.query("TDIV?"))
    trdl = parse_float(scope.query("TRDL?"))
    grid = 14.0
    t0 = trdl - (tdiv * grid / 2.0)
    dt = 1.0 / sample_rate

    for chan_s in [p.strip() for p in args.channels.split(",") if p.strip()]:
        ch = int(chan_s)
        vdiv = parse_float(scope.query(f"C{ch}:VDIV?"))
        ofst = parse_float(scope.query(f"C{ch}:OFST?"))
        prefix, payload = parse_ieee_block(scope.query_binary(f"C{ch}:WF? DAT2"))
        if args.debug:
            print(f"# {prefix} payload={len(payload)} vdiv={vdiv} ofst={ofst}")
        path = out_dir / f"C{ch}.csv"
        with path.open("w", newline="", encoding="utf-8") as f:
            w = csv.writer(f)
            w.writerow(["second", "value"])
            for i, raw in enumerate(payload):
                code = signed8(raw)
                volts = code * (vdiv / 25.0) - ofst
                w.writerow([f"{t0 + i * dt:.12e}", f"{volts:.12e}"])
        print(path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
