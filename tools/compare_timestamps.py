#!/usr/bin/env python3
"""
compare_timestamps.py — Compare Beast timestamps between two receivers.

Connects to two Beast TCP streams, matches frames by payload, and reports
the timestamp delta distribution. Used to validate MLAT timing accuracy
of the plane_watcher FPGA receiver against a known-good Radarcape.

Usage:
    uv run tools/compare_timestamps.py [--duration 60]
    uv run tools/compare_timestamps.py --ref radarcape.local:10004 --dut pluto.local:30005
"""

import argparse
import socket
import statistics
import struct
import threading
import time
from collections import defaultdict

BEAST_ESCAPE = 0x1A
TYPE_SHORT = 0x32  # 7-byte Mode-S short
TYPE_LONG = 0x33   # 14-byte Mode-S long

# Radarcape timestamp format: bit 47 = GPS sync flag,
# bits [46:30] = seconds of day, bits [29:0] = nanoseconds.
TS_GPS_SYNC_BIT = 1 << 47
TS_SEC_SHIFT = 30
TS_NANO_MASK = 0x3FFFFFFF


def parse_radarcape_ts(ts48: int) -> tuple[float, bool]:
    """Decode a 48-bit Radarcape timestamp to (seconds_of_day, gps_synced)."""
    gps_sync = bool(ts48 & TS_GPS_SYNC_BIT)
    seconds = (ts48 >> TS_SEC_SHIFT) & 0x1FFFF
    nanos = ts48 & TS_NANO_MASK
    return seconds + nanos / 1e9, gps_sync


def unescape_beast(data: bytes) -> tuple[bytes, bytes]:
    """Remove Beast escape sequences from the buffer.

    Returns (unescaped_frame, remaining_buffer). The frame starts after
    the leading 0x1A and includes the type byte, 6 timestamp bytes,
    1 signal byte, and the message payload.
    """
    out = bytearray()
    i = 0
    while i < len(data):
        if data[i] == BEAST_ESCAPE:
            if i + 1 >= len(data):
                # Incomplete escape — return what we have so far and keep
                # the partial escape in the remaining buffer.
                return bytes(out), data[i:]
            if data[i + 1] == BEAST_ESCAPE:
                out.append(BEAST_ESCAPE)
                i += 2
            else:
                # New frame start marker — we've finished this frame.
                return bytes(out), data[i:]
        else:
            out.append(data[i])
            i += 1
    return bytes(out), b""


def parse_beast_frames(buf: bytes) -> tuple[list[tuple[int, bytes]], bytes]:
    """Parse complete Beast frames from a buffer.

    Returns (list of (ts48, payload_bytes), remaining_buffer).
    """
    frames = []
    while True:
        # Find the next frame start (0x1A followed by a type byte).
        idx = buf.find(bytes([BEAST_ESCAPE]))
        if idx == -1 or idx + 1 >= len(buf):
            break

        frame_type = buf[idx + 1]
        if frame_type == BEAST_ESCAPE:
            # Escaped 0x1A inside data — skip past it.
            buf = buf[idx + 2:]
            continue

        if frame_type not in (TYPE_SHORT, TYPE_LONG):
            # Unknown type — skip this byte.
            buf = buf[idx + 1:]
            continue

        msg_len = 7 if frame_type == TYPE_SHORT else 14
        # Frame content: type(1) + timestamp(6) + signal(1) + message(msg_len)
        expected_raw_len = 1 + 6 + 1 + msg_len

        # Find the content after the 0x1A marker.
        content_start = idx + 1  # skip the leading 0x1A
        # We need to unescape to get the actual frame bytes.
        raw = buf[content_start:]
        unescaped, remainder = unescape_beast(raw)

        if len(unescaped) < expected_raw_len:
            # Incomplete frame — wait for more data.
            break

        # Parse the unescaped frame.
        # byte 0: type, bytes 1-6: timestamp, byte 7: signal, bytes 8+: message
        ts_bytes = unescaped[1:7]
        ts48 = struct.unpack(">Q", b"\x00\x00" + ts_bytes)[0]
        msg_bytes = unescaped[8:8 + msg_len]

        frames.append((ts48, bytes(msg_bytes)))

        # Advance the buffer past the consumed data.
        consumed = len(raw) - len(remainder)
        buf = buf[content_start + consumed:]

    return frames, buf


def receiver_thread(host: str, port: int, label: str,
                    store: dict, lock: threading.Lock,
                    stop_event: threading.Event,
                    require_sync: bool = True,
                    diag_samples: int = 5):
    """Connect to a Beast TCP source and collect timestamped frames."""
    try:
        sock = socket.create_connection((host, port), timeout=5)
    except (socket.error, OSError) as e:
        print(f"[{label}] connection failed: {e}")
        stop_event.set()
        return

    sync_note = "sync required" if require_sync else "sync not required"
    print(f"[{label}] connected to {host}:{port} ({sync_note})")
    sock.settimeout(1.0)
    buf = b""
    frame_count = 0
    total_frames = 0
    no_sync_count = 0

    while not stop_event.is_set():
        try:
            chunk = sock.recv(4096)
            if not chunk:
                print(f"[{label}] connection closed")
                break
            buf += chunk
        except socket.timeout:
            continue
        except (socket.error, OSError) as e:
            print(f"[{label}] recv error: {e}")
            break

        frames, buf = parse_beast_frames(buf)
        with lock:
            for ts48, msg in frames:
                total_frames += 1
                ts_sod, gps_sync = parse_radarcape_ts(ts48)

                # Print diagnostic info for the first few frames.
                if total_frames <= diag_samples:
                    print(f"[{label}] frame {total_frames}: "
                          f"ts48=0x{ts48:012X} "
                          f"sync={gps_sync} "
                          f"sod={ts_sod:.6f}s "
                          f"msg={msg[:4].hex()}...")

                if require_sync and not gps_sync:
                    no_sync_count += 1
                    continue
                # Only match long frames (DF17/18, 14 bytes) to avoid
                # false payload collisions on 7-byte short frames.
                if len(msg) != 14:
                    continue
                # Key by raw message bytes for matching.
                store[msg].append((ts_sod, label))
                frame_count += 1

    sock.close()
    print(f"[{label}] finished — {frame_count} accepted, "
          f"{no_sync_count} no-sync, {total_frames} total")


def main():
    parser = argparse.ArgumentParser(
        description="Compare Beast timestamps between two receivers")
    parser.add_argument("--ref", default="radarcape.local:10004",
                        help="Reference receiver host:port (default: radarcape.local:10004)")
    parser.add_argument("--dut", default="pluto.local:30005",
                        help="Device under test host:port (default: pluto.local:30005)")
    parser.add_argument("--duration", type=int, default=60,
                        help="Collection duration in seconds (default: 60)")
    args = parser.parse_args()

    ref_host, ref_port = args.ref.rsplit(":", 1)
    dut_host, dut_port = args.dut.rsplit(":", 1)

    # Shared store: message_bytes -> [(seconds_of_day, label), ...]
    store: dict[bytes, list[tuple[float, str]]] = defaultdict(list)
    lock = threading.Lock()
    stop_event = threading.Event()

    ref_thread = threading.Thread(
        target=receiver_thread,
        args=(ref_host, int(ref_port), "REF", store, lock, stop_event),
        kwargs={"require_sync": False},
        daemon=True)
    dut_thread = threading.Thread(
        target=receiver_thread,
        args=(dut_host, int(dut_port), "DUT", store, lock, stop_event),
        daemon=True)

    print(f"Collecting for {args.duration}s... (Ctrl-C to stop early)")
    ref_thread.start()
    dut_thread.start()

    try:
        start = time.monotonic()
        while time.monotonic() - start < args.duration:
            time.sleep(5)
            elapsed = int(time.monotonic() - start)
            with lock:
                ref_n = sum(1 for entries in store.values()
                            for _, l in entries if l == "REF")
                dut_n = sum(1 for entries in store.values()
                            for _, l in entries if l == "DUT")
                matched = sum(1 for entries in store.values()
                              if any(l == "REF" for _, l in entries)
                              and any(l == "DUT" for _, l in entries))
            print(f"  [{elapsed:3d}s] REF={ref_n} DUT={dut_n} matched={matched}")
    except KeyboardInterrupt:
        print("\nInterrupted.")
    stop_event.set()

    ref_thread.join(timeout=3)
    dut_thread.join(timeout=3)

    # Find matched frames (seen by both receivers).
    # Only use payloads seen exactly once by each source — repeated squitters
    # (same content at 2 Hz) can't be reliably paired to the same RF event.
    deltas_us = []
    with lock:
        for msg, entries in store.items():
            ref_times = [t for t, l in entries if l == "REF"]
            dut_times = [t for t, l in entries if l == "DUT"]
            if len(ref_times) != 1 or len(dut_times) != 1:
                continue
            delta_s = dut_times[0] - ref_times[0]
            # Handle day wraparound.
            if delta_s > 43200:
                delta_s -= 86400
            elif delta_s < -43200:
                delta_s += 86400
            # Sanity: same RF event should be < 1ms apart.
            if abs(delta_s) > 0.001:
                continue
            deltas_us.append(delta_s * 1e6)

    # Count totals for context.
    ref_total = sum(1 for entries in store.values()
                    for _, l in entries if l == "REF")
    dut_total = sum(1 for entries in store.values()
                    for _, l in entries if l == "DUT")

    print(f"\n{'=' * 60}")
    print(f"REF frames: {ref_total}  DUT frames: {dut_total}")
    print(f"Matched frames: {len(deltas_us)}")

    if not deltas_us:
        print("No matched frames found.")
        return

    deltas_us.sort()
    med = statistics.median(deltas_us)
    avg = statistics.mean(deltas_us)
    sd = statistics.stdev(deltas_us) if len(deltas_us) > 1 else 0.0
    outliers = [d for d in deltas_us if abs(d - med) > 1000]

    print(f"\nTimestamp delta (DUT - REF) in microseconds:")
    print(f"  Min:    {deltas_us[0]:+.1f} us")
    print(f"  Max:    {deltas_us[-1]:+.1f} us")
    print(f"  Median: {med:+.1f} us")
    print(f"  Mean:   {avg:+.1f} us")
    print(f"  StdDev: {sd:.1f} us")
    print(f"  Outliers (>1ms from median): {len(outliers)}")

    # Show distribution buckets.
    buckets = [0.1, 0.5, 1.0, 5.0, 10.0, 50.0, 100.0]
    print(f"\nDistribution (absolute delta from median):")
    for limit in buckets:
        count = sum(1 for d in deltas_us if abs(d - med) <= limit)
        pct = count / len(deltas_us) * 100
        print(f"  <= {limit:6.1f} us: {count:5d} ({pct:5.1f}%)")
    print(f"{'=' * 60}")


if __name__ == "__main__":
    main()
