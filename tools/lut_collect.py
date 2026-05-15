#!/usr/bin/env python3
"""Interactive LUT calibration collector.

Collects one CSV row per injected RF level by combining:

- scope measurements over USBTMC / SCPI on the Linux host
- raw ADC capture and debug counters from the remote decoder target over ssh

Typical usage from the desktop that has the Siglent scope attached:

    python3 tools/lut_collect.py \
      --scope /dev/usbtmc3 \
      --target root@planewatcher \
      --levels=-80,-70,-60,-50,-40,-30,-20

The script prompts for each level, waits for the user to set the generator,
reads CH1/CH2/CH3 measurements from the scope, samples the FPGA raw ADC codes,
and appends a CSV row to stdout and optionally a file.
"""

from __future__ import annotations

import argparse
import csv
import os
import re
import shlex
import statistics
import subprocess
import sys
import time
from dataclasses import dataclass
from typing import Iterable


FLOAT_RE = re.compile(r"[-+]?\d+(?:\.\d+)?(?:[Ee][-+]?\d+)?")


@dataclass
class ScopeStats:
    mean: float
    pkpk: float


@dataclass
class AdcStats:
    code_center: float
    code_min: int
    code_max: int
    adc_otr: int
    sample_min: int
    sample_max: int


class Scope:
    def __init__(self, path: str, timeout: float = 2.0, debug: bool = False) -> None:
        self.path = path
        self.timeout = timeout
        self.debug = debug

    def _query(self, cmd: str) -> str:
        with open(self.path, "r+b", buffering=0) as dev:
            dev.write(cmd.encode("ascii") + b"\n")
            deadline = time.time() + self.timeout
            chunks: list[bytes] = []
            while time.time() < deadline:
                chunk = os.read(dev.fileno(), 4096)
                if chunk:
                    chunks.append(chunk)
                    if chunk.endswith(b"\n"):
                        break
                else:
                    time.sleep(0.02)
            if not chunks:
                raise RuntimeError(f"no response to SCPI query: {cmd}")
        resp = b"".join(chunks).decode("ascii", errors="replace").strip()
        if self.debug:
            print(f"# SCPI {cmd!r} -> {resp!r}", file=sys.stderr)
        return resp

    def command(self, cmd: str) -> None:
        with open(self.path, "r+b", buffering=0) as dev:
            dev.write(cmd.encode("ascii") + b"\n")
        if self.debug:
            print(f"# SCPI command {cmd!r}", file=sys.stderr)

    def command_sequence(self, cmds: str) -> None:
        for cmd in (part.strip() for part in cmds.split(";")):
            if cmd:
                self.command(cmd)

    def idn(self) -> str:
        return self._query("*IDN?")

    def pava(self, channel: int, parameter: str) -> float:
        resp = self._query(f"C{channel}:PAVA? {parameter}")
        matches = FLOAT_RE.findall(resp)
        if not matches:
            raise RuntimeError(f"unable to parse scope response for C{channel} {parameter}: {resp!r}")
        return float(matches[-1])

    def stats(self, channel: int) -> ScopeStats:
        mean = self.pava(channel, "MEAN")
        pkpk = self.pava(channel, "PKPK")
        if pkpk == 0.0:
            vmax = self.pava(channel, "MAX")
            vmin = self.pava(channel, "MIN")
            pkpk = max(0.0, vmax - vmin)
            if self.debug:
                print(
                    f"# C{channel}: PKPK came back 0, recomputed from MAX/MIN -> {pkpk!r}",
                    file=sys.stderr,
                )
        return ScopeStats(mean=mean, pkpk=pkpk)


def parse_levels(spec: str) -> list[str]:
    return [part.strip() for part in spec.split(",") if part.strip()]


def run_remote_adc_capture(
    target: str,
    ssh_options: list[str],
    sshpass_password: str,
    base_addr: str,
    settle_s: float,
    regdump_path: str,
    dump_capture_path: str,
    debug: bool,
) -> AdcStats:
    remote_cmd = (
        f"{shlex.quote(regdump_path)} --base-addr {shlex.quote(base_addr)} --reset >/dev/null ; "
        f"sleep {settle_s:.3f} ; "
        f"{shlex.quote(regdump_path)} --base-addr {shlex.quote(base_addr)} --snapshot | "
        "grep -E 'ADC_CODE_MIN|ADC_CODE_MAX|ADC_OTR_CT' ; "
        f"{shlex.quote(dump_capture_path)} --base-addr {shlex.quote(base_addr)} --raw"
    )
    ssh_cmd = ["ssh", *ssh_options, target, remote_cmd]
    if sshpass_password:
        ssh_cmd = ["sshpass", "-p", sshpass_password, *ssh_cmd]
    proc = subprocess.run(
        ssh_cmd,
        check=True,
        capture_output=True,
        text=True,
    )
    out = proc.stdout
    if debug:
        print(f"# SSH command: {' '.join(shlex.quote(x) for x in ssh_cmd)}", file=sys.stderr)
        print("# SSH stdout begin", file=sys.stderr)
        print(out.rstrip(), file=sys.stderr)
        print("# SSH stdout end", file=sys.stderr)
        if proc.stderr.strip():
            print("# SSH stderr begin", file=sys.stderr)
            print(proc.stderr.rstrip(), file=sys.stderr)
            print("# SSH stderr end", file=sys.stderr)

    min_match = re.search(r"ADC_CODE_MIN\s+raw=0x([0-9A-Fa-f]+)", out)
    max_match = re.search(r"ADC_CODE_MAX\s+raw=0x([0-9A-Fa-f]+)", out)
    otr_match = re.search(r"ADC_OTR_CT\s+(\d+)", out)
    if not (min_match and max_match and otr_match):
        raise RuntimeError(f"failed to parse regdump output:\n{out}")

    code_min = int(min_match.group(1), 16)
    code_max = int(max_match.group(1), 16)
    adc_otr = int(otr_match.group(1))

    samples: list[int] = []
    for line in out.splitlines():
        parts = line.strip().split(",")
        if len(parts) != 5:
            continue
        try:
            samples.append(int(parts[2]))
        except ValueError:
            continue
    if not samples:
        raise RuntimeError(f"failed to parse ADC samples from dump-capture output:\n{out}")

    return AdcStats(
        code_center=float(statistics.median(samples)),
        code_min=code_min,
        code_max=code_max,
        adc_otr=adc_otr,
        sample_min=min(samples),
        sample_max=max(samples),
    )


def format_row(level_dbm: str, ch1: ScopeStats, ch2: ScopeStats, ch3: ScopeStats, adc: AdcStats) -> list[str]:
    vdiff = abs(ch2.mean - ch3.mean)
    vcm = 0.5 * (ch2.mean + ch3.mean)
    return [
        level_dbm,
        f"{adc.code_center:.3f}",
        str(adc.code_min),
        str(adc.code_max),
        str(adc.adc_otr),
        f"{ch1.mean:.9f}",
        f"{ch1.pkpk:.9f}",
        f"{ch2.mean:.9f}",
        f"{ch2.pkpk:.9f}",
        f"{ch3.mean:.9f}",
        f"{ch3.pkpk:.9f}",
        f"{vdiff:.9f}",
        f"{vcm:.9f}",
    ]


def prompt(msg: str) -> str:
    try:
        return input(msg)
    except EOFError:
        raise SystemExit(1)


def main(argv: Iterable[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Interactive LUT calibration collector")
    ap.add_argument("--scope", default="/dev/usbtmc0", help="USBTMC device path")
    ap.add_argument("--target", required=True, help="ssh target for regdump/dump-capture, e.g. root@planewatcher")
    ap.add_argument("--levels", default="-80,-70,-60,-50,-40,-30,-20", help="comma-separated generator levels in dBm")
    ap.add_argument("--base-addr", default="0x43C03000", help="decoder AXI base address")
    ap.add_argument("--settle", type=float, default=2.0, help="seconds to wait after reset before ADC sampling")
    ap.add_argument("--regdump-path", default="/tmp/regdump", help="remote regdump path")
    ap.add_argument("--dump-capture-path", default="/tmp/dump-capture", help="remote dump-capture path")
    ap.add_argument("--output", default="", help="optional CSV file to append rows to")
    ap.add_argument("--channels", default="1,2,3", help="scope channels for -IN,+OUT,-OUT")
    ap.add_argument(
        "--scope-arm-command",
        default="TRMD SINGLE; ARM; FRTR; WAIT",
        help="SCPI command sent before reading scope measurements",
    )
    ap.add_argument(
        "--scope-refresh-seconds",
        type=float,
        default=0.75,
        help="delay after the scope arm command before querying measurements",
    )
    ap.add_argument(
        "--scope-stop-command",
        default="",
        help="optional SCPI command sent after reading scope measurements",
    )
    ap.add_argument(
        "--ssh-options",
        default="-F,/dev/null,-o,StrictHostKeyChecking=no,-o,UserKnownHostsFile=/dev/null,-o,CheckHostIP=no,-o,PubkeyAuthentication=no",
        help="comma-separated ssh options passed before the target host",
    )
    ap.add_argument(
        "--sshpass-password",
        default=os.environ.get("LUT_COLLECT_SSHPASS", ""),
        help="optional password passed via sshpass; defaults to LUT_COLLECT_SSHPASS env var",
    )
    ap.add_argument("--debug", action="store_true", help="print raw SCPI and remote capture details to stderr")
    args = ap.parse_args(list(argv) if argv is not None else None)

    channels = [int(x.strip()) for x in args.channels.split(",")]
    if len(channels) != 3:
        raise SystemExit("--channels must contain exactly three comma-separated channel numbers")
    ch_in, ch_plus, ch_minus = channels
    ssh_options = [x.strip() for x in args.ssh_options.split(",") if x.strip()]

    scope = Scope(args.scope, debug=args.debug)
    idn = scope.idn()
    print(f"# Scope: {idn}", file=sys.stderr)
    print(f"# Target: {args.target}", file=sys.stderr)

    header = [
        "level_dbm",
        "adc_code_center",
        "adc_code_min",
        "adc_code_max",
        "adc_otr",
        "ch_in_mean_v",
        "ch_in_pkpk_v",
        "ch_plus_mean_v",
        "ch_plus_pkpk_v",
        "ch_minus_mean_v",
        "ch_minus_pkpk_v",
        "vdiff_mean_v",
        "vcm_mean_v",
    ]

    writer = csv.writer(sys.stdout)
    file_writer = None
    output_fh = None
    if args.output:
        output_fh = open(args.output, "a", newline="")
        file_writer = csv.writer(output_fh)
        if output_fh.tell() == 0:
            file_writer.writerow(header)

    writer.writerow(header)

    for level in parse_levels(args.levels):
        print("", file=sys.stderr)
        print(f"# Set generator to {level} dBm and press Enter when stable.", file=sys.stderr)
        prompt("> ")

        if args.scope_arm_command:
            scope.command_sequence(args.scope_arm_command)
            time.sleep(args.scope_refresh_seconds)
        ch1 = scope.stats(ch_in)
        ch2 = scope.stats(ch_plus)
        ch3 = scope.stats(ch_minus)
        if args.scope_stop_command:
            scope.command(args.scope_stop_command)
        adc = run_remote_adc_capture(
            target=args.target,
            ssh_options=ssh_options,
            sshpass_password=args.sshpass_password,
            base_addr=args.base_addr,
            settle_s=args.settle,
            regdump_path=args.regdump_path,
            dump_capture_path=args.dump_capture_path,
            debug=args.debug,
        )

        row = format_row(level, ch1, ch2, ch3, adc)
        writer.writerow(row)
        if file_writer is not None:
            file_writer.writerow(row)
            output_fh.flush()

        print(
            f"# {level} dBm: adc={adc.code_center:.1f} [{adc.code_min}..{adc.code_max}] "
            f"samples=[{adc.sample_min}..{adc.sample_max}] "
            f"vcm={float(row[-1]):.3f} vdiff={float(row[-2]):.3f}",
            file=sys.stderr,
        )

    if output_fh is not None:
        output_fh.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
