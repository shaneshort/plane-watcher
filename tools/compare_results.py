#!/usr/bin/env python3
"""Compare FPGA simulation decoded messages against dump1090 reference output.

Usage:
    uv run python compare_results.py <sim_log> <reference_log>

Example:
    uv run python compare_results.py /tmp/sim_capture2.log capture2_reference.log
"""

import re
import sys
from collections import Counter
from dataclasses import dataclass

import pyModeS as pms


@dataclass
class SimMessage:
    """A decoded message from simulation with metadata."""
    hex_msg: str       # Normalised hex (dump1090 byte order)
    toa: int           # TOA in counter ticks (16 MHz)
    sample_us: float   # Approximate microseconds into capture
    decoder: int       # Which decoder instance
    raw_hex: str       # Original hex from sim (reversed byte order)


def reverse_bytes(hex_str: str) -> str:
    """Reverse byte order of a hex string (e.g. 'AABB' -> 'BBAA')."""
    bs = [hex_str[i:i+2] for i in range(0, len(hex_str), 2)]
    return "".join(reversed(bs))


def extract_sim_messages(path: str) -> list[SimMessage]:
    """Extract decoded messages with TOA from GHDL simulation log."""
    messages = []
    pattern = re.compile(
        r"DECODED MESSAGE \[decoder (\d+)\] #\d+: ([0-9A-Fa-f]{14,28})"
        r" TOA=(\d+)"
    )
    with open(path) as f:
        for line in f:
            m = pattern.search(line)
            if m:
                decoder = int(m.group(1))
                raw_hex = m.group(2).upper()
                toa = int(m.group(3))
                norm_hex = reverse_bytes(raw_hex)
                messages.append(SimMessage(
                    hex_msg=norm_hex,
                    toa=toa,
                    sample_us=toa / 16.0,  # 16 MHz clock
                    decoder=decoder,
                    raw_hex=raw_hex,
                ))
    return messages


def extract_ref_messages(path: str) -> list[str]:
    """Extract message hex strings from dump1090 reference log (*hex;)."""
    messages = []
    pattern = re.compile(r"^\*([0-9A-Fa-f]+);")
    with open(path) as f:
        for line in f:
            m = pattern.match(line.strip())
            if m:
                messages.append(m.group(1).upper())
    return messages


def get_df(hex_msg: str) -> int:
    """Extract Downlink Format from first 5 bits of message."""
    first_byte = int(hex_msg[:2], 16)
    return first_byte >> 3


def df_name(df: int) -> str:
    names = {
        0: "Short Air-Air (ACAS)",
        4: "Altitude Reply",
        5: "Identity Reply",
        11: "All-Call Reply",
        16: "Long Air-Air (ACAS)",
        17: "Extended Squitter (ADS-B)",
        18: "Extended Squitter (TIS-B)",
        19: "Military Extended Squitter",
        20: "Comm-B Altitude Reply",
        21: "Comm-B Identity Reply",
    }
    return names.get(df, f"DF{df}")


def validate_message(hex_msg: str) -> dict:
    """Validate a message using pyModeS. Returns dict of findings."""
    result = {"crc_ok": False, "df": get_df(hex_msg), "details": ""}

    try:
        # pyModeS CRC check
        crc = pms.crc(hex_msg)
        result["crc_ok"] = (crc == 0)

        df = result["df"]

        if df == 17 or df == 18:
            tc = pms.adsb.typecode(hex_msg)
            icao = pms.icao(hex_msg)
            parts = [f"ICAO={icao}", f"TC={tc}"]

            if 1 <= tc <= 4:
                cs = pms.adsb.callsign(hex_msg)
                parts.append(f"call={cs}")
            elif 9 <= tc <= 18:
                parts.append("airborne_pos")
            elif tc == 19:
                parts.append("velocity")
            elif 5 <= tc <= 8:
                parts.append("surface_pos")

            result["details"] = " ".join(parts)

        elif df == 11:
            icao = pms.icao(hex_msg)
            result["details"] = f"ICAO={icao}"

        elif df in (0, 4, 5, 16, 20, 21):
            # For interrogation-dependent DFs, CRC contains ICAO XOR interrogator code
            # A non-zero CRC doesn't mean invalid — it means we'd need an ICAO table
            alt = None
            if df in (0, 4, 16, 20):
                try:
                    alt = pms.common.altcode(hex_msg)
                    if alt is not None:
                        result["details"] = f"alt={alt}ft"
                except Exception:
                    pass
            elif df in (5, 21):
                try:
                    sq = pms.common.idcode(hex_msg)
                    result["details"] = f"squawk={sq}"
                except Exception:
                    pass

    except Exception as e:
        result["details"] = f"error: {e}"

    return result


def estimate_ref_positions(ref_msgs: list[str], sim_msgs: list[SimMessage]) -> dict[str, float]:
    """Estimate sample positions for reference messages based on sim TOA.

    Uses matched messages to build a time mapping, then interpolates
    positions for unmatched reference messages based on their sequential
    order in the reference file.
    """
    sim_by_hex = {}
    for sm in sim_msgs:
        if sm.hex_msg not in sim_by_hex:
            sim_by_hex[sm.hex_msg] = sm

    # Build ordered list of (ref_index, sample_us) for matched messages
    anchors = []
    for i, msg in enumerate(ref_msgs):
        if msg in sim_by_hex:
            anchors.append((i, sim_by_hex[msg].sample_us))

    if not anchors:
        return {}

    # For each reference message, interpolate position from nearest anchors
    positions = {}
    for i, msg in enumerate(ref_msgs):
        if msg in sim_by_hex:
            positions[msg] = sim_by_hex[msg].sample_us
        else:
            # Find surrounding anchors
            before = [(idx, us) for idx, us in anchors if idx < i]
            after = [(idx, us) for idx, us in anchors if idx > i]

            if before and after:
                # Linear interpolation
                bi, bus = before[-1]
                ai, aus = after[0]
                frac = (i - bi) / (ai - bi) if ai != bi else 0
                positions[msg] = bus + frac * (aus - bus)
            elif before:
                positions[msg] = before[-1][1]
            elif after:
                positions[msg] = after[0][1]

    return positions


def main():
    if len(sys.argv) != 3:
        print(f"Usage: {sys.argv[0]} <sim_log> <reference_log>")
        sys.exit(1)

    sim_path = sys.argv[1]
    ref_path = sys.argv[2]

    sim_msgs = extract_sim_messages(sim_path)
    ref_msgs = extract_ref_messages(ref_path)

    sim_hexes = [m.hex_msg for m in sim_msgs]
    sim_set = set(sim_hexes)
    ref_set = set(ref_msgs)

    matched = sim_set & ref_set
    sim_only = sim_set - ref_set
    ref_only = ref_set - sim_set

    # Build lookup for sim messages
    sim_by_hex = {}
    for sm in sim_msgs:
        if sm.hex_msg not in sim_by_hex:
            sim_by_hex[sm.hex_msg] = sm

    # Categorize reference-only misses by message length
    ref_only_short = {m for m in ref_only if len(m) == 14}
    ref_only_long = {m for m in ref_only if len(m) == 28}

    # DF breakdown
    ref_only_df = Counter(get_df(m) for m in ref_only)
    sim_only_df = Counter(get_df(m) for m in sim_only)
    matched_df = Counter(get_df(m) for m in matched)

    # Unique ICAO addresses
    def get_icao(msg: str) -> str | None:
        df = get_df(msg)
        if df in (11, 17, 18):
            return msg[2:8]
        return None

    sim_icaos = {get_icao(m) for m in sim_hexes if get_icao(m)}
    ref_icaos = {get_icao(m) for m in ref_msgs if get_icao(m)}

    # =========================================================================
    # Summary
    # =========================================================================
    print("=" * 70)
    print("FPGA Simulation vs dump1090 Reference Comparison")
    print("=" * 70)
    print(f"  Sim log:  {sim_path}")
    print(f"  Ref log:  {ref_path}")
    print()
    print(f"  Sim total messages:  {len(sim_msgs):>5}  (unique: {len(sim_set)})")
    print(f"  Ref total messages:  {len(ref_msgs):>5}  (unique: {len(ref_set)})")
    print()
    print(f"  Matched:             {len(matched):>5}")
    print(f"  Sim-only:            {len(sim_only):>5}")
    print(f"  Ref-only (missed):   {len(ref_only):>5}")
    print(f"    - Short (56-bit):  {len(ref_only_short):>5}")
    print(f"    - Long (112-bit):  {len(ref_only_long):>5}")
    print()
    print(f"  Match rate (of ref): {len(matched)/len(ref_set)*100:.1f}%")
    print()

    # ICAO summary
    print(f"  Unique ICAO (sim):   {len(sim_icaos):>5}")
    print(f"  Unique ICAO (ref):   {len(ref_icaos):>5}")
    print(f"  ICAO matched:        {len(sim_icaos & ref_icaos):>5}")
    print(f"  ICAO ref-only:       {len(ref_icaos - sim_icaos):>5}")
    print()

    # DF breakdown table
    all_dfs = sorted(set(list(matched_df.keys()) + list(ref_only_df.keys()) + list(sim_only_df.keys())))
    print("  DF Breakdown:")
    print(f"  {'DF':>4}  {'Name':<30}  {'Match':>5}  {'Missed':>6}  {'SimOnly':>7}")
    print(f"  {'----':>4}  {'-'*30}  {'-----':>5}  {'------':>6}  {'-------':>7}")
    for df in all_dfs:
        name = df_name(df)
        print(f"  {df:>4}  {name:<30}  {matched_df.get(df,0):>5}  {ref_only_df.get(df,0):>6}  {sim_only_df.get(df,0):>7}")
    print()

    # =========================================================================
    # Validate sim-only messages with pyModeS
    # =========================================================================
    print("=" * 70)
    print("Sim-Only Message Validation (pyModeS)")
    print("=" * 70)
    print()

    valid_count = 0
    invalid_count = 0
    sim_only_sorted = sorted(sim_only, key=lambda m: sim_by_hex[m].sample_us if m in sim_by_hex else 0)

    for msg in sim_only_sorted:
        v = validate_message(msg)
        sm = sim_by_hex.get(msg)
        toa_str = f"{sm.sample_us:>12.1f} us" if sm else "?"

        if v["crc_ok"]:
            valid_count += 1
            tag = "CRC OK "
        else:
            invalid_count += 1
            tag = "CRC BAD"

        print(f"  [{tag}] {toa_str}  DF={v['df']:>2} {msg}  {v['details']}")

    print()
    print(f"  CRC valid:   {valid_count}")
    print(f"  CRC invalid: {invalid_count}")
    print()

    # =========================================================================
    # Locate missed messages in capture timeline
    # =========================================================================
    print("=" * 70)
    print("Missed Message Positions (estimated from capture timeline)")
    print("=" * 70)
    print()

    ref_positions = estimate_ref_positions(ref_msgs, sim_msgs)

    # Focus on missed long messages (DF17 especially)
    missed_with_pos = []
    for msg in ref_only:
        pos = ref_positions.get(msg)
        v = validate_message(msg)
        missed_with_pos.append((pos or 0, msg, v))

    missed_with_pos.sort(key=lambda x: x[0])

    # Group into time windows for potential test vector extraction
    # Show all missed DF17/18 with positions
    print("  Missed DF17/18 messages with estimated sample positions:")
    print(f"  {'Position':>14}  {'Sample#':>10}  {'DF':>3}  {'Message':<28}  Details")
    print(f"  {'-'*14}  {'-'*10}  {'---':>3}  {'-'*28}  -------")

    missed_df17_positions = []
    for pos_us, msg, v in missed_with_pos:
        df = get_df(msg)
        if df in (17, 18):
            sample_num = int(pos_us * 16)  # 16 samples/us
            print(f"  {pos_us:>12.1f} us  {sample_num:>10d}  {df:>3}  {msg}  {v['details']}")
            missed_df17_positions.append((pos_us, sample_num, msg))

    print()

    # Also show missed DF16 (ACAS) and DF20/21 (Comm-B)
    print("  Missed DF16/20/21 messages (need ICAO lookup table):")
    print(f"  {'Position':>14}  {'DF':>3}  {'Message':<28}  Details")
    print(f"  {'-'*14}  {'---':>3}  {'-'*28}  -------")
    for pos_us, msg, v in missed_with_pos:
        df = get_df(msg)
        if df in (16, 20, 21):
            print(f"  {pos_us:>12.1f} us  {df:>3}  {msg}  {v['details']}")
    print()

    # =========================================================================
    # Suggest test vector extraction commands
    # =========================================================================
    if missed_df17_positions:
        print("=" * 70)
        print("Test Vector Extraction Commands")
        print("=" * 70)
        print()
        print("  Extract regions around missed DF17 messages from the raw capture.")
        print("  Each window is 500 us before to 500 us after the estimated position.")
        print("  Samples are 4 bytes each (I16 + Q16 little-endian).")
        print()

        # Cluster nearby misses into windows
        windows = []
        margin_us = 500  # 500 us margin each side
        for pos_us, sample_num, msg in missed_df17_positions:
            start_us = max(0, pos_us - margin_us)
            end_us = pos_us + margin_us
            # Merge with previous window if overlapping
            if windows and start_us <= windows[-1][1]:
                windows[-1] = (windows[-1][0], max(end_us, windows[-1][1]), windows[-1][2] + [msg])
            else:
                windows.append((start_us, end_us, [msg]))

        for i, (start_us, end_us, msgs) in enumerate(windows):
            start_sample = int(start_us * 16)
            end_sample = int(end_us * 16)
            num_samples = end_sample - start_sample
            byte_offset = start_sample * 4  # 4 bytes per IQ sample
            byte_count = num_samples * 4
            fname = f"missed_region_{i}.dat"

            print(f"  Window {i}: {start_us:.0f} - {end_us:.0f} us ({len(msgs)} missed msg(s))")
            for msg in msgs:
                print(f"    {msg}")
            print(f"  dd if=vectors/capture2.dat of=vectors/{fname} bs=1 skip={byte_offset} count={byte_count}")
            print(f"  # {num_samples} samples, {byte_count} bytes")
            print()


if __name__ == "__main__":
    main()
