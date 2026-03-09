#!/usr/bin/env python3
"""
Inspect live Beast/Radarcape timestamp streams from two receivers.

Connects to two Beast TCP endpoints, prints a few decoded timestamp samples
from each, reports whether either stream goes backwards locally, and matches
shared long frames by payload to compare timestamp deltas.

Examples:
  python3 tools/inspect_live_timestamps.py
  python3 tools/inspect_live_timestamps.py --ref radarcape.local:10003 --dut pluto.local:30005
"""

import argparse
import socket
import struct
import time
from collections import defaultdict

ESC = 0x1A
TYPE_SHORT = ord("2")
TYPE_LONG = ord("3")
TYPE_STATUS = ord("4")
TYPE_POSITION = ord("5")
TS_SYNC_BIT = 1 << 47


def decode_settings(settings: int) -> str:
    """Decode the 0x34 settings byte into human-readable flags."""
    flags = []
    flags.append("binary" if settings & 0x01 else "AVR")
    if settings & 0x10:
        flags.append("GPS")
    else:
        flags.append("12MHz")
    if settings & 0x02:
        flags.append("filtered")
    if settings & 0x08:
        flags.append("no-CRC")
    if settings & 0x20:
        flags.append("RTSCTS")
    if settings & 0x40:
        flags.append("no-FEC")
    if settings & 0x80:
        flags.append("ModeAC")
    return "+".join(flags)


def parse_endpoint(value: str) -> tuple[str, int]:
    host, port = value.rsplit(":", 1)
    return host, int(port)


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


def main() -> None:
    parser = argparse.ArgumentParser(description="Inspect live Beast timestamp streams")
    parser.add_argument("--ref", default="radarcape.local:10003", help="Reference endpoint host:port")
    parser.add_argument("--dut", default="pluto.local:30005", help="Device-under-test endpoint host:port")
    parser.add_argument("--duration", type=float, default=12.0, help="Capture duration in seconds")
    parser.add_argument("--samples", type=int, default=12, help="Per-endpoint frame samples to print")
    parser.add_argument("--matches", type=int, default=20, help="Matched long-frame comparisons to print")
    args = parser.parse_args()

    endpoints = {
        "REF": parse_endpoint(args.ref),
        "DUT": parse_endpoint(args.dut),
    }

    socks = {}
    for label, (host, port) in endpoints.items():
        sock = socket.create_connection((host, port), timeout=5)
        sock.settimeout(0.5)
        socks[label] = sock

    buffers = {label: bytearray() for label in endpoints}
    last_ts = {}
    out_of_order = defaultdict(int)
    first_samples = defaultdict(list)
    status_samples = defaultdict(list)
    seen_by_payload = defaultdict(dict)
    matched = []

    end = time.time() + args.duration
    while time.time() < end and len(matched) < args.matches:
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
            for payload in frames:
                frame_type = payload[0]
                ts48 = int.from_bytes(payload[1:7], "big")
                sig = payload[7]
                body = payload[8:]
                sync, sec, ns, sod = parse_ts(ts48)

                prev = last_ts.get(label)
                if prev is not None and ts48 < prev:
                    out_of_order[label] += 1
                last_ts[label] = ts48

                if len(first_samples[label]) < args.samples and frame_type in (TYPE_SHORT, TYPE_LONG):
                    first_samples[label].append(
                        (chr(frame_type), ts48, sync, sec, ns, sig, body.hex().upper())
                    )

                if frame_type == TYPE_STATUS and len(status_samples[label]) < 4:
                    settings = body[0]
                    delta = struct.unpack("b", body[1:2])[0]
                    gps = body[2]
                    status_samples[label].append((ts48, settings, delta, gps))
                    continue

                if frame_type == TYPE_POSITION:
                    # Position frame: no ts/sig, just 21 bytes of data.
                    # Lat at bytes 5-8, lon at 9-12, alt at 13-16 (LE float32).
                    pos_data = payload[1:]  # skip type byte
                    pos_lat = struct.unpack("<f", pos_data[4:8])[0]
                    pos_lon = struct.unpack("<f", pos_data[8:12])[0]
                    pos_alt = struct.unpack("<f", pos_data[12:16])[0]
                    print(
                        f"[{label}] position lat={pos_lat:.6f} lon={pos_lon:.6f} alt={pos_alt:.1f}m"
                    )
                    continue

                if frame_type != TYPE_LONG:
                    continue

                if label in seen_by_payload[body]:
                    continue
                seen_by_payload[body][label] = (ts48, sync, sec, ns, sod, sig)
                if len(seen_by_payload[body]) == 2:
                    ref = seen_by_payload[body]["REF"]
                    dut = seen_by_payload[body]["DUT"]
                    delta_us = (dut[4] - ref[4]) * 1e6
                    if delta_us > 43200 * 1e6:
                        delta_us -= 86400 * 1e6
                    elif delta_us < -43200 * 1e6:
                        delta_us += 86400 * 1e6
                    matched.append((body.hex().upper(), ref, dut, delta_us))

    for sock in socks.values():
        sock.close()

    print("FIRST SAMPLES")
    for label in ("REF", "DUT"):
        print(f"[{label}] out_of_order={out_of_order[label]}")
        for frame_type, ts48, sync, sec, ns, sig, msghex in first_samples[label]:
            print(
                f"[{label}] type={frame_type} ts=0x{ts48:012X} "
                f"sync={sync} sec={sec} ns={ns} sig={sig} msg={msghex[:28]}"
            )
        if status_samples[label]:
            for ts48, settings, delta, gps in status_samples[label]:
                sync, sec, ns, sod = parse_ts(ts48)
                print(
                    f"[{label}] status ts=0x{ts48:012X} sync={sync} sec={sec} ns={ns} "
                    f"settings=0x{settings:02X}({decode_settings(settings)}) "
                    f"delta={delta} gps=0x{gps:02X}"
                )
        else:
            print(f"[{label}] no status frames seen")

    print("\nMATCHED LONG FRAMES")
    for i, (msghex, ref, dut, delta_us) in enumerate(matched[: args.matches], 1):
        print(
            f"#{i} delta_us={delta_us:.1f} msg={msghex[:28]} "
            f"ref_sec={ref[2]} ref_ns={ref[3]} dut_sec={dut[2]} dut_ns={dut[3]}"
        )


if __name__ == "__main__":
    main()
