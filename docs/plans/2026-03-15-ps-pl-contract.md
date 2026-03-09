# PS/PL Interface Contract

Frozen interface between the FPGA decode pipeline (PL) and the Linux
consumer software (PS).  Changes to any item below require both sides
to be updated in lockstep.

## 1. AXI Register Map

AXI4-Lite slave, 32-bit aligned, accessed from Linux via `/dev/mem` mmap.

| Offset | Name         | Access | Description                                              |
|--------|-------------|--------|----------------------------------------------------------|
| 0x00   | MSG_DATA_0  | R      | Message bits [31:0] (first transmitted bytes in low lanes) |
| 0x04   | MSG_DATA_1  | R      | Message bits [63:32]                                     |
| 0x08   | MSG_DATA_2  | R      | Message bits [95:64]                                     |
| 0x0C   | MSG_DATA_3  | R      | Message bits [111:96] (last transmitted bytes in [15:0], upper 16 zero) |
| 0x10   | TOA_LO      | R      | 64-bit timestamp [31:0]                                  |
| 0x14   | TOA_HI      | R      | 64-bit timestamp [63:32]                                 |
| 0x18   | RPL         | R      | Signal level [23:0] — **reading pops the FIFO**          |
| 0x1C   | STATUS      | R      | [0] not_empty, [1] full, [2] overflow (sticky), [14:8] fill count |
| 0x20   | PPS_COUNT   | R      | PPS edge count [31:0]                                    |
| 0x24   | PPS_CTR_LO  | R      | Counter at last PPS [31:0]                               |
| 0x28   | PPS_CTR_HI  | R      | Counter at last PPS [63:32]                              |
| 0x2C   | CONTROL     | RW     | [0] soft_reset (auto-clear), [1] enable, [2] debug snapshot request (write-1 pulses) |
| 0x30   | VERSION     | R      | Hardware version (v1.0.0 = 0x00010000)                   |
| 0x34   | DBG_INDEX   | RW     | Debug counter selector                                   |
| 0x38   | DBG_DATA    | R      | Selected debug counter value                             |
| 0x3C   | CONFIG      | RW     | [2:0] quiet_score_shift, [5:3] snr_ratio_shift           |

## 2. FIFO Pop Semantics

1. Check `STATUS` bit 0 (`not_empty`).
2. Read registers in this fixed order: `MSG_DATA_0`, `MSG_DATA_1`,
   `MSG_DATA_2`, `MSG_DATA_3`, `TOA_LO`, `TOA_HI`.  The RTL does not
   provide an atomic snapshot across these reads, so the order must match
   the PS implementation to avoid data-tearing if the FIFO advances
   unexpectedly.
3. Read `RPL` **last** — this atomically pops the FIFO and advances to
   the next message.
4. Repeat from step 1.

Reading `RPL` when the FIFO is empty is a no-op (no pop issued).

## 3. Message Byte Order at the AXI Boundary

The FPGA's `bit_flipper` applies a `swizzle()` that reverses the 14-byte
message before storing in `msg_bits[111:0]`.  As a result:

- `MSG_DATA_0` [7:0] holds the **first transmitted byte** (DF field).
- `MSG_DATA_3` [15:0] holds the **last transmitted bytes** (PI/CRC tail).

The PS extracts all 14 bytes MSB-first from DATA_3 down to DATA_0, then
reverses the full array to recover standard Mode-S order (first
transmitted byte at index 0).

This is a known wart inherited from the bladeRF-adsb port.  The PL
exposes the swizzled byte order; the PS is responsible for the reversal.
Not worth normalising in hardware unless a v2 register interface is
designed.

## 4. Timestamp Semantics

### FPGA counter

100 MHz free-running 64-bit counter.  Wraps after ~5,800 years.

### PPS capture

On each rising PPS edge the counter value is latched into
`counter_at_pps` (registers `PPS_CTR_LO`/`PPS_CTR_HI`) and `PPS_COUNT`
increments.  Double-flop synchroniser + rising-edge detect in
`timestamp_counter`.

### Standard Beast timestamps

The PS scales 100 MHz to the 12 MHz Beast convention:

    ts12 = floor(toa * 12 / 100)

Truncated to 48 bits (6 bytes).

### Radarcape timestamps

48-bit field layout:

    [47:30]  seconds since midnight UTC  (18 bits, max 86400)
    [29:0]   nanosecond fraction         (30 bits)

UTC seconds are derived from the NTP-disciplined system clock via coarse
software correlation — the PS calls `time.Now()` when it notices
`PPS_COUNT` has changed, which may be milliseconds after the actual PPS
edge.  NTP keeps the system clock within ~10 ms of truth, which is
sufficient for second-level accuracy.  This is **not** hardware-latched
at the PPS edge.

Nanosecond fraction:

    nanos = (msg.TOA - counter_at_pps) * 1e9 / 100e6

This gives sub-microsecond precision from the FPGA counter.

PPS boundary crossing: if `msg.TOA < counter_at_pps`, the message is
assigned to the previous second.

### Current limitations

- No true GNSS/UTC integration — seconds field relies on NTP + software
  PPS correlation.
- True UTC/GNSS integration needs a dedicated time-source path (future
  work).

## 5. Beast Binary Wire Format

Frame structure:

    0x1A <type> <timestamp:6> <signal:1> <message:7|14>

| Field     | Size    | Encoding                                          |
|-----------|---------|---------------------------------------------------|
| Start     | 1 byte  | `0x1A` — not escaped                              |
| Type      | 1 byte  | `0x32` short (DF 0–15), `0x33` long (DF 16–31)   |
| Timestamp | 6 bytes | Big-endian (MSB first), standard or Radarcape     |
| Signal    | 1 byte  | `sqrt(min(RPL, 2621440) / 2621440) * 255`, min 1 if RPL > 0 |
| Message   | 7 or 14 | Standard Mode-S byte order (first transmitted first) |

**Escaping:** any `0x1A` in the payload is doubled to `0x1A 0x1A`.
The leading frame marker is never escaped.

**Transport:** raw Beast binary stream over TCP (default port 30005).
Multiple clients via fan-out; dead clients cleaned up on write error.
