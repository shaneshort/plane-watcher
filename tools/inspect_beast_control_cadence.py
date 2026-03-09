#!/usr/bin/env python3
"""
Inspect Beast/Radarcape control-frame cadence.

Connects to one or more Beast TCP endpoints and reports how often control
frames appear, especially:
  - type '4' (0x34): Radarcape status/config
  - type '5' (0x35): Radarcape position

Examples:
  python3 tools/inspect_beast_control_cadence.py --endpoint radarcape.local:10003
  python3 tools/inspect_beast_control_cadence.py \
    --endpoint REF=radarcape.local:10003 \
    --endpoint DUT=pluto.local:30005 \
    --duration 30
"""

import argparse
import socket
import struct
import time
from collections import defaultdict
from statistics import mean

ESC = 0x1A
TYPE_SHORT = ord("2")
TYPE_LONG = ord("3")
TYPE_STATUS = ord("4")
TYPE_POSITION = ord("5")
TS_SYNC_BIT = 1 << 47


def parse_endpoint(value: str) -> tuple[str, tuple[str, int]]:
    if "=" in value:
        label, addr = value.split("=", 1)
    else:
        label, addr = value, value
    host, port = addr.rsplit(":", 1)
    return label, (host, int(port))


def parse_ts(ts48: int) -> tuple[bool, int, int, float]:
    sync = bool(ts48 & TS_SYNC_BIT)
    sec = (ts48 >> 30) & 0x1FFFF
    ns = ts48 & 0x3FFFFFFF
    return sync, sec, ns, sec + ns / 1e9


def extract_frames(buf: bytearray) -> tuple[list[bytes], bytearray]:
    frames = []
    i = 0
    while True:
        try:
            start = buf.index(ESC, i)
        except ValueError:
            return frames, buf[i:]

        if start + 2 > len(buf):
            return frames, buf[start:]

        frame_type = buf[start + 1]
        if frame_type == ESC:
            i = start + 2
            continue

        if frame_type not in (TYPE_SHORT, TYPE_LONG, TYPE_STATUS, TYPE_POSITION):
            i = start + 1
            continue

        payload_len = {
            TYPE_SHORT: 1 + 6 + 1 + 7,
            TYPE_LONG: 1 + 6 + 1 + 14,
            TYPE_STATUS: 1 + 6 + 1 + 14,
            TYPE_POSITION: 1 + 21,
        }[frame_type]

        payload = bytearray()
        j = start + 1
        while len(payload) < payload_len:
            if j >= len(buf):
                return frames, buf[start:]
            b = buf[j]
            if b == ESC:
                if j + 1 >= len(buf):
                    return frames, buf[start:]
                if buf[j + 1] != ESC:
                    return frames, buf[start:]
                payload.append(ESC)
                j += 2
            else:
                payload.append(b)
                j += 1

        frames.append(bytes(payload))
        i = j


def fmt_intervals(times: list[float]) -> str:
    if len(times) < 2:
        return "n/a"
    deltas = [b - a for a, b in zip(times, times[1:])]
    return (
        f"count={len(times)} avg={mean(deltas):.3f}s "
        f"min={min(deltas):.3f}s max={max(deltas):.3f}s"
    )


def main() -> None:
    parser = argparse.ArgumentParser(description="Inspect Beast control-frame cadence")
    parser.add_argument(
        "--endpoint",
        action="append",
        default=[],
        help="Endpoint as host:port or LABEL=host:port. Can be repeated.",
    )
    parser.add_argument("--duration", type=float, default=30.0, help="Capture duration in seconds")
    parser.add_argument(
        "--show",
        type=int,
        default=5,
        help="Number of control frames per type to print per endpoint",
    )
    args = parser.parse_args()

    endpoint_args = args.endpoint or ["REF=radarcape.local:10003"]
    endpoints = dict(parse_endpoint(value) for value in endpoint_args)

    socks = {}
    for label, (host, port) in endpoints.items():
        sock = socket.create_connection((host, port), timeout=5)
        sock.settimeout(0.5)
        socks[label] = sock

    buffers = {label: bytearray() for label in endpoints}
    seen_times = defaultdict(lambda: defaultdict(list))
    samples = defaultdict(lambda: defaultdict(list))
    counts = defaultdict(lambda: defaultdict(int))
    status_deltas = defaultdict(list)

    end = time.time() + args.duration
    while time.time() < end:
        for label, sock in socks.items():
            try:
                chunk = sock.recv(4096)
                if not chunk:
                    continue
                buffers[label].extend(chunk)
            except socket.timeout:
                continue

            frames, remainder = extract_frames(buffers[label])
            buffers[label] = bytearray(remainder)
            now = time.time()
            for payload in frames:
                frame_type = payload[0]
                counts[label][frame_type] += 1

                if frame_type == TYPE_STATUS:
                    ts48 = int.from_bytes(payload[1:7], "big")
                    sync, sec, ns, _ = parse_ts(ts48)
                    body = payload[8:]
                    settings = body[0]
                    delta = struct.unpack("b", body[1:2])[0]
                    gps = body[2]
                    seen_times[label][frame_type].append(now)
                    status_deltas[label].append(delta)
                    if len(samples[label][frame_type]) < args.show:
                        samples[label][frame_type].append(
                            f"ts=0x{ts48:012X} sync={sync} sec={sec} ns={ns} "
                            f"settings=0x{settings:02X} delta={delta} gps=0x{gps:02X}"
                        )
                elif frame_type == TYPE_POSITION:
                    body = payload[1:]
                    seen_times[label][frame_type].append(now)
                    if len(samples[label][frame_type]) < args.show:
                        samples[label][frame_type].append(
                            f"body={body.hex().upper()}"
                        )

    for sock in socks.values():
        sock.close()

    for label in endpoints:
        print(f"[{label}]")
        print(
            f"  data counts: short={counts[label][TYPE_SHORT]} "
            f"long={counts[label][TYPE_LONG]} "
            f"status={counts[label][TYPE_STATUS]} "
            f"position={counts[label][TYPE_POSITION]}"
        )
        print(f"  status cadence:   {fmt_intervals(seen_times[label][TYPE_STATUS])}")
        print(f"  position cadence: {fmt_intervals(seen_times[label][TYPE_POSITION])}")
        if status_deltas[label]:
            uniq = sorted(set(status_deltas[label]))
            print(
                f"  PPS delta byte:   count={len(status_deltas[label])} "
                f"min={min(status_deltas[label])} max={max(status_deltas[label])} "
                f"unique={uniq[:12]}{'...' if len(uniq) > 12 else ''}"
            )
        else:
            print("  PPS delta byte:   n/a")

        if samples[label][TYPE_STATUS]:
            print("  status samples:")
            for line in samples[label][TYPE_STATUS]:
                print(f"    {line}")
        else:
            print("  status samples: none")

        if samples[label][TYPE_POSITION]:
            print("  position samples:")
            for line in samples[label][TYPE_POSITION]:
                print(f"    {line}")
        else:
            print("  position samples: none")


if __name__ == "__main__":
    main()
