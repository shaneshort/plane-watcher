#!/usr/bin/env python3
"""Parse GHDL simulation logs and decode ADS-B messages.

Reads FLIP_DBG report lines from a GHDL simulation log file and decodes
the Mode-S message fields into human-readable format.

Usage:
    uv run python decode_sim_log.py /tmp/sim_live_2s.log
    uv run python decode_sim_log.py /tmp/sim_live_2s.log --summary
"""

import argparse
import re
import sys
from pathlib import Path


# Type Code descriptions (TC field = bits 32-36 of extended message)
TC_DESCRIPTIONS = {
    range(1, 5): "Aircraft ID",
    range(5, 9): "Surface Position",
    range(9, 19): "Airborne Position (Baro)",
    (19,): "Airborne Velocity",
    range(20, 23): "Airborne Position (GNSS)",
    (28,): "Aircraft Status",
    (29,): "Target State & Status",
    (31,): "Aircraft Op Status",
}

# DF descriptions
DF_DESCRIPTIONS = {
    0: "Short Air-Air Surveillance (ACAS)",
    4: "Surveillance Altitude Reply",
    5: "Surveillance Identity Reply",
    11: "All-Call Reply",
    16: "Long Air-Air Surveillance (ACAS)",
    17: "Extended Squitter (ADS-B)",
    18: "Extended Squitter (TIS-B/ADS-R)",
    19: "Military Extended Squitter",
    20: "Comm-B Altitude Reply",
    21: "Comm-B Identity Reply",
    24: "Comm-D Extended Length Message",
}


def get_tc_description(tc: int) -> str:
    for key, desc in TC_DESCRIPTIONS.items():
        if tc in key:
            return desc
    return f"Unknown TC={tc}"


def decode_message(byte_values: list[int], sim_time_ps: int | None = None) -> dict:
    """Decode a 14-byte Mode-S message."""
    hex_str = ''.join(f'{b:02X}' for b in byte_values)
    df = byte_values[0] >> 3
    ca = byte_values[0] & 0x07
    icao = ''.join(f'{b:02X}' for b in byte_values[1:4])

    result = {
        'hex': hex_str,
        'df': df,
        'ca': ca,
        'icao': icao,
        'df_desc': DF_DESCRIPTIONS.get(df, f"DF{df}"),
    }

    if sim_time_ps is not None:
        result['time_us'] = sim_time_ps / 1e6

    # Extended squitter: decode type code and ME field
    if df >= 16:
        me_bytes = byte_values[4:11]
        tc = me_bytes[0] >> 3
        result['tc'] = tc
        result['tc_desc'] = get_tc_description(tc)
        result['me'] = ''.join(f'{b:02X}' for b in me_bytes)

        # Aircraft ID: decode callsign from TC 1-4
        if 1 <= tc <= 4:
            charset = "#ABCDEFGHIJKLMNOPQRSTUVWXYZ##### ###############0123456789######"
            me_bits = int(result['me'], 16)
            callsign = ''
            for i in range(8):
                idx = (me_bits >> (42 - 6 * i)) & 0x3F
                callsign += charset[idx] if idx < len(charset) else '?'
            result['callsign'] = callsign.strip()

    # CRC (last 3 bytes)
    result['crc'] = ''.join(f'{b:02X}' for b in byte_values[11:14])

    return result


def parse_log(log_path: Path) -> tuple[list[dict], dict]:
    """Parse a GHDL sim log and return decoded messages + stats."""
    messages = []
    stats = {
        'preambles': 0,
        'soms': 0,
        'df_skipped': 0,
        'crc_pass': 0,
        'crc_fail': 0,
    }

    # Match BSDs done lines followed by CRC PASS
    bsds_pattern = re.compile(
        r'@(\d+)ps:.*FLIP_DBG: BSDs done\. DF=(\d+) bytes: ([\d ]+)')
    crc_pass_pattern = re.compile(
        r'@(\d+)ps:.*FLIP_DBG: \*\*\* CRC PASS on iter (\d+)')
    decoded_pattern = re.compile(
        r'DECODED MESSAGE \[decoder (\d+)\] #(\d+): (\w+) TOA=(\d+)')
    # Fallback: BSDs done without DF= prefix (older format)
    bsds_pattern_old = re.compile(
        r'@(\d+)ps:.*FLIP_DBG: BSDs done\. All bytes: ([\d ]+)')

    with open(log_path) as f:
        lines = f.readlines()

    for line in lines:
        if 'PREAMBLE DETECTED' in line:
            stats['preambles'] += 1
        elif 'SOM_DBG' in line:
            stats['soms'] += 1
        elif 'Invalid DF' in line:
            stats['df_skipped'] += 1
        elif '32 iterations failed' in line:
            stats['crc_fail'] += 1

    # Collect TOA values from DECODED MESSAGE lines
    toa_by_msg_num = {}
    for line in lines:
        m = decoded_pattern.search(line)
        if m:
            msg_num = int(m.group(2))
            toa_by_msg_num[msg_num] = int(m.group(4))

    # Find CRC PASS messages and their preceding BSDs done lines
    pending_bsds = {}  # decoder_time -> (sim_time, bytes)
    for i, line in enumerate(lines):
        m = bsds_pattern.search(line)
        if not m:
            m = bsds_pattern_old.search(line)
            if m:
                sim_time = int(m.group(1))
                byte_str = m.group(2)
                pending_bsds[sim_time] = byte_str
                continue
        if m:
            sim_time = int(m.group(1))
            byte_str = m.group(3) if len(m.groups()) == 3 else m.group(2)
            pending_bsds[sim_time] = byte_str
            continue

        m = crc_pass_pattern.search(line)
        if m:
            crc_time = int(m.group(1))
            iter_num = int(m.group(2))
            stats['crc_pass'] += 1

            # Find the most recent BSDs done before this CRC PASS
            best_time = None
            for t in pending_bsds:
                if t < crc_time and (best_time is None or t > best_time):
                    best_time = t

            if best_time is not None:
                byte_values = [int(x) for x in pending_bsds[best_time].split()]
                msg = decode_message(byte_values, best_time)
                msg['iter'] = iter_num
                msg_num = len(messages) + 1
                if msg_num in toa_by_msg_num:
                    msg['toa'] = toa_by_msg_num[msg_num]
                messages.append(msg)
                del pending_bsds[best_time]

    return messages, stats


def main():
    parser = argparse.ArgumentParser(description='Decode ADS-B messages from GHDL sim logs')
    parser.add_argument('logfile', type=Path, help='GHDL simulation log file')
    parser.add_argument('--summary', action='store_true',
                        help='Print only summary statistics')
    args = parser.parse_args()

    if not args.logfile.exists():
        print(f"Error: {args.logfile} not found", file=sys.stderr)
        sys.exit(1)

    messages, stats = parse_log(args.logfile)

    # Print stats
    print(f"=== Simulation Decode Summary ===")
    print(f"  Preambles detected: {stats['preambles']}")
    print(f"  SOMs fired:         {stats['soms']}")
    print(f"  DF invalid (skip):  {stats['df_skipped']}")
    print(f"  CRC brute-force:    {stats['crc_pass'] + stats['crc_fail']}")
    print(f"  CRC passed:         {stats['crc_pass']}")
    print(f"  CRC failed:         {stats['crc_fail']}")

    if args.summary or not messages:
        if not messages:
            print("\nNo messages decoded.")
        return

    # Print decoded messages
    print(f"\n=== Decoded Messages ({len(messages)}) ===\n")

    # Collect unique ICAO addresses
    icao_set = set()
    for msg in messages:
        icao_set.add(msg['icao'])

        time_str = f"{msg['time_us']:10.1f} µs" if 'time_us' in msg else ""
        print(f"  {time_str}  {msg['hex']}")
        print(f"    DF={msg['df']:2d} ({msg['df_desc']})")
        print(f"    ICAO: {msg['icao']}  CA={msg['ca']}")
        if 'tc' in msg:
            print(f"    TC={msg['tc']:2d} ({msg['tc_desc']})")
            if 'callsign' in msg:
                print(f"    Callsign: {msg['callsign']}")
        if 'toa' in msg:
            toa_us = msg['toa'] / 16.0  # 16 MHz counter
            print(f"    TOA: {msg['toa']} ({toa_us:.1f} µs)")
        iter_val = msg['iter']
        bits_flipped = bin(iter_val).count('1')
        ec_desc = 'clean' if iter_val == 0 else f'{bits_flipped} bit(s) flipped'
        print(f"    Error correction: iter {iter_val} ({ec_desc})")
        print()

    # Summary
    print(f"=== Unique Aircraft: {len(icao_set)} ===")
    for icao in sorted(icao_set):
        count = sum(1 for m in messages if m['icao'] == icao)
        print(f"  {icao}: {count} message(s)")


if __name__ == '__main__':
    main()
