# GPS ToA Timestamping Design

**Date:** 2026-03-28
**Status:** Approved design, not yet implemented

## Overview

Bring up Time-of-Arrival (ToA) timestamping using an external u-blox ZED-F9P
GPS receiver connected to the Fishball board's JP5 header. The F9P provides a
PPS signal for precise timestamp discipline and UART for coarse time via gpsd
and chrony. This enables accurate Radarcape-format Beast timestamps for MLAT.

## Goals

1. PPS-disciplined FPGA timestamps with measured oscillator correction
2. PPS exposed to Linux as `/dev/pps0` for chrony
3. F9P UART routed to Linux as `/dev/ttyPS0` for gpsd
4. Continuous clock carryover tracking and corrected Beast timestamp encoding
5. Graceful degradation when GPS is unavailable

## Non-Goals

- Sub-sample interpolation (future work, uses existing `fractional` field)
- Custom Go UBX library (gpsd handles F9P communication)
- NTP server (chrony disciplines local clock only)

---

## 1. Physical Wiring

F9P connects to JP5 (2.54 mm Dupont header) on the Fishball board.

| JP5 Pin | Net      | Signal  | Direction     | Notes                      |
|---------|----------|---------|---------------|----------------------------|
| 3       | VCC5V    | Power   | Board → F9P   | F9P requires 5V supply     |
| 7       | 3V3_IO1  | PPS     | F9P → FPGA    | 3.3V logic, Bank 13        |
| 9       | 3V3_IO2  | UART TX | F9P → PS (RX) | 3.3V logic, Bank 13        |
| 11      | 3V3_IO3  | UART RX | PS (TX) → F9P | 3.3V logic, Bank 13        |
| 20      | GND      | Ground  | Common        |                            |

All 3V3_IO pins are on PL Bank 13 (VCCO = 3.3V), matching F9P logic levels.

## 2. FPGA Changes

The timestamp_counter PPS infrastructure (double-flop synchroniser, edge
detector, counter latch) is already built and tested. However, the hybrid
vendor build path currently ties `pps_in` to GND and has no external port
for it. Changes span the BD script, `system_top.v`, and constraints.

### 2.1 Hybrid BD: Export pps_in Port

The simpler non-vendor flow (`create_zynq_bd.tcl`) already creates a `pps_in`
BD port and connects it to the wrapper. The hybrid vendor flow
(`build_vendor.tcl`) instead ties the wrapper input to GND:

```tcl
ad_connect GND adsb_vendor_wrapper_0/pps_in    ;# line ~221
```

Replace with an external BD port:

```tcl
# Remove GND tie-off:
#   ad_connect GND adsb_vendor_wrapper_0/pps_in
# Add external port:
create_bd_port -dir I pps_in
ad_connect pps_in adsb_vendor_wrapper_0/pps_in
```

### 2.2 system_top.v: Add pps_in Port

The current `system_top.v` has no `pps_in` port. Add it:

```verilog
// Port declaration (alongside spi_miso etc.):
input           pps_in

// In the system_wrapper instantiation:
.pps_in (pps_in),
```

### 2.3 PPS Pin Constraint

Add to `plane_watcher_integration.xdc`:

```
set_property -dict {PACKAGE_PIN <3V3_IO1_ball> IOSTANDARD LVCMOS33} [get_ports pps_in]
```

The exact ball assignment for 3V3_IO1 must be confirmed from the schematic's
Bank 13 pinout (U1G section).

### 2.4 EMIO GPIO for PPS to PS

Route the PPS signal into the PS via EMIO GPIO. The existing 18-bit EMIO bus
is allocated as follows:

| Bits    | Usage                                      |
|---------|--------------------------------------------|
| [13:0]  | AD9361 physical GPIO (resetb, en_agc, ctl, status) |
| [14]    | Loopback (unused output)                   |
| [15]    | up_enable (radio control)                  |
| [16]    | up_txnrx (radio control)                   |
| [17]    | **Free — use for PPS**                     |

Use EMIO GPIO[17]. The PPS signal must enter on `gpio_i` (not `gpio_o`) so
the Linux PPS GPIO driver can read it. In `system_top.v`:

```verilog
// Replace the existing loopback for bit 17:
//   assign gpio_i[16:14] = gpio_o[16:14];  (bit 17 was implicitly undriven)
// With:
assign gpio_i[16:14] = gpio_o[16:14];
assign gpio_i[17]    = pps_in;              // PPS routed to PS via EMIO GPIO[17]
```

Linux GPIO number: 54 (MIO count) + 17 = **71**.

### 2.5 EMIO UART0

Enable PS UART0 via EMIO in the PS7 block design. Route UART0_TX and UART0_RX
through the PL to JP5:

```
set_property -dict {PACKAGE_PIN <3V3_IO3_ball> IOSTANDARD LVCMOS33} [get_ports uart0_tx]
set_property -dict {PACKAGE_PIN <3V3_IO2_ball> IOSTANDARD LVCMOS33} [get_ports uart0_rx]
```

Zynq assigns ttyPS devices by base address: uart0 (`0xe0000000`) → `ttyPS0`,
uart1 (`0xe0001000`) → `ttyPS1`. Since UART1 is already enabled on MIO8/9 for
the serial console, enabling UART0 via EMIO means:

- `/dev/ttyPS0` → UART0 (EMIO) → **GPS**
- `/dev/ttyPS1` → UART1 (MIO8/9) → **console**

The kernel boot args must be updated from `console=ttyPS0` to
`console=ttyPS1,115200` to keep the serial console working.

## 3. Linux Kernel & Device Tree

### 3.1 PPS GPIO

Add to the device tree overlay (`zynq-pluto-sdr-plane-watcher-hybrid.dtsi`):

```dts
pps {
    compatible = "pps-gpio";
    gpios = <&gpio0 71 GPIO_ACTIVE_HIGH>;  /* EMIO GPIO[17] = MIO base 54 + 17 */
    status = "okay";
};
```

Creates `/dev/pps0`. Zynq MIO occupies GPIO 0-53; EMIO starts at 54.
GPIO[17] in the EMIO space = Linux GPIO 71.

### 3.2 UART0

Enable UART0 in the device tree:

```dts
&uart0 {
    status = "okay";
};
```

### 3.3 Kernel Config

Verify `CONFIG_PPS_CLIENT_GPIO` is enabled in the Pluto kernel config. The
UART driver (`cadence-uart`) is already built-in.

## 4. GPS Stack

### 4.1 gpsd

Runs as a daemon on `/dev/ttyPS0` at 115200 baud. Speaks UBX to the F9P
natively. Provides SHM refclock segment 0 (coarse time from UBX-NAV-TIMEUTC)
for chrony.

### 4.2 F9P Configuration (ubxtool)

`ubxtool` (ships with gpsd) configures the F9P at first boot:

- Serial port: 115200 baud, UBX protocol only (disable NMEA output)
- Enable UBX-NAV-TIMEUTC at 1 Hz
- Enable UBX-MON-HW at 0.2 Hz (antenna and jamming monitoring)
- PPS: positive polarity, aligned to UTC, always on
- Constellations: GPS + Galileo + GLONASS
- Save config to F9P flash (`CFG-SAVE`) — survives power cycles

Once saved to flash, this step is only needed when changing configuration.

### 4.3 chrony

```
# Coarse time from gpsd SHM — used only to identify which second
refclock SHM 0 refid GPS offset 0.0 precision 1e-1 noselect

# Precise PPS from kernel PPS GPIO driver
refclock PPS /dev/pps0 refid PPS lock GPS precision 1e-9 prefer

# NTP fallback for cold start / GPS outage
server ntp.ubuntu.com iburst
```

- SHM 0 marked `noselect`: only used to associate PPS edges with UTC seconds
- `lock GPS` on PPS: ties PPS to the SHM coarse source
- `prefer` on PPS: primary time source when GPS-locked
- NTP: fallback when GPS unavailable

### 4.4 Startup Sequencing (init scripts)

Started sequentially in a single init script:

1. FPGA bitstream loads (PPS input active, counter running, GPS sync bit clear)
2. Start gpsd → opens `/dev/ttyPS0`, establishes SHM segment
3. Brief sleep to let gpsd initialise
4. Start chrony → opens SHM 0 + `/dev/pps0`
5. Start plane-feeder → begins polling FIFO and PPS registers
6. First valid PPS + SHM → chrony locks, plane-feeder asserts GPS sync bit

plane-feeder does not hard-depend on GPS. It degrades gracefully to
unsynchronised Beast output (free-running counter, no GPS sync bit).

## 5. plane-feeder Clock Discipline

### 5.1 Dedicated PPS Watcher

The main FIFO-read loop only polls PPS registers during message bursts or on
the 10-second stats tick. In quiet periods (no aircraft), `pps.Count` can jump
by more than 1, making per-second carryover math garbage.

**Solution:** a dedicated goroutine polls PPS registers at ~2 Hz (500 ms tick).
On each poll it compares the current `pps.Count` to the last seen value. When
the count increments by exactly 1, it computes a single-interval carryover.
When it increments by >1 (skipped edges), it computes an averaged estimate and
logs a warning — the averaged value is usable for timestamp correction but is
flagged as degraded for the sync bit gate.

The watcher publishes its latest `PpsTimeRef` (including `measuredTicks`) on a
channel or atomic pointer consumed by the FIFO-read loop.

#### Coherent PPS Register Reads

The current `ReadPps()` performs three separate MMIO reads (count, counter_lo,
counter_hi) with no atomicity guarantee. If a PPS edge lands between reads,
the software sees a mix of the old and new latch values — fabricating a bogus
delta, false skipped-edge event, or carryover spike.

The hardware latch is coherent (timestamp_counter latches `pps_count` and
`counter_at_pps` atomically on the PPS edge), but the three-word software read
is not. Fix with a **count-stable retry loop** in `ReadPps()`:

```go
func ReadPpsStable(r RegisterReader) PpsState {
    for {
        c1 := r.Read32(RegPpsCount)
        lo := r.Read32(RegPpsCtrLo)
        hi := r.Read32(RegPpsCtrHi)
        c2 := r.Read32(RegPpsCount)
        if c1 == c2 {
            return PpsState{Count: c1, CounterLo: lo, CounterHi: hi}
        }
        // PPS edge landed mid-read; retry (takes ~ns, PPS is 1 Hz)
    }
}
```

This is sufficient because PPS fires once per second while the MMIO reads
complete in microseconds — the retry will virtually never loop more than once.
No hardware changes needed.

### 5.2 Clock Carryover Tracking

On each PPS edge observed by the watcher (count incremented by exactly 1):

```
measuredTicks  = counter_at_pps[n] - counter_at_pps[n-1]
carryover      = measuredTicks - 100_000_000
oscillatorPPM  = float64(carryover) / 100e6 * 1e6
```

When PPS count jumps by `delta > 1` (skipped edges):

```
avgTicks       = (counter_at_pps[n] - counter_at_pps[n-1]) / delta
carryover      = avgTicks - 100_000_000   // degraded — averaged over gap
```

Maintain a rolling window (last 60 seconds):

| Metric          | Purpose                     |
|-----------------|--------------------------------------------|
| carryover       | Per-second drift (or averaged if skipped)  |
| avg carryover   | Drift trend                                |
| min/max         | Jitter envelope                            |
| oscillator PPM  | Smoothed frequency offset                  |
| skipped edges   | Count of missed PPS polls (health metric)  |

### 5.3 Corrected Beast Timestamp Encoding

Current (nominal clock rate):
```go
nanos = (msg.TOA - counterAtPps) * 1_000_000_000 / 100_000_000
```

Corrected (measured clock rate):
```go
nanos = (msg.TOA - counterAtPps) * 1_000_000_000 / measuredTicks
```

Eliminates systematic error from oscillator drift. At -15 ppm, this corrects
up to ~15 us of error per second.

### 5.4 GPS Sync Bit

Radarcape timestamp bit 47 (GPS sync) is asserted only when **all** conditions
are met:

1. PPS edges are arriving (`pps.Count` incrementing)
2. At least 2 consecutive PPS edges received (need a delta for `measuredTicks`)
3. No skipped PPS edges on the last interval (count delta == 1)
4. Carryover is sane: `|carryover| < 10,000` (within ~100 ppm)
5. **Chrony reports synchronised status** (see below)

If any condition fails, the sync bit clears. Downstream MLAT aggregators use
this bit to decide whether to trust the timestamps.

#### UTC Lock Verification

PPS alone does not establish which UTC second it is. The feeder must verify
that chrony has locked before asserting sync.

**chronyc tracking** (via Unix socket `/var/run/chrony/chronyd.sock`):
parse `Leap status` field. `Normal` = synced. Any other value = not synced.
This is the sole authority for UTC lock — chrony is the component that
actually disciplines the system clock.

**Poll cadence and expiry:** poll chrony every **2 seconds** from the PPS
watcher goroutine (it already ticks at 500 ms; query chrony every 4th tick).
The cached chrony-synced flag expires after **4 seconds** — if no successful
poll confirms sync within that window, the flag clears and the GPS sync bit
drops. This bounds the stale-sync window to 4 seconds worst case rather than
10. The PPS "always on" configuration means PPS cadence and carryover can
look healthy even after chrony loses lock, so the expiry is load-bearing.

**No gpsd fallback.** gpsd's `TPV.mode` field is fix dimension (0/1/2/3 =
unknown/no-fix/2D/3D), not time validity. The `time` field may be absent or
invalid even when mode >= 2. Using TPV.mode as a proxy for time lock would
re-introduce false GPS-sync assertions on cold boot or during leap-second
uncertainty. If chrony is not running or not yet synced, the sync bit stays
clear — this is the correct conservative behaviour.

The current Beast encoder relies on `time.Now()` for UTC second correlation
(`beast.go:41`). This is only valid once chrony has disciplined the system
clock. Without the chrony gate, cold-boot timestamps could be off by minutes.

### 5.5 Dashboard Stats

Surface alongside existing stats (CRC pass rate etc.):

| Stat                  | Example        |
|-----------------------|----------------|
| `gps_sync`            | `true`         |
| `oscillator_ppm`      | `-14.7`        |
| `carryover`           | `-8`           |
| `carryover_min`       | `-22`          |
| `carryover_max`       | `18`           |
| `pps_interval_ticks`  | `99998527`     |

## 6. Robustness & Edge Cases

### 6.1 GPS Cold Start / Signal Loss

- plane-feeder falls back to standard Beast timestamps (free-running 12 MHz
  scaled counter, GPS sync bit cleared)
- Carryover tracking pauses; last known `measuredTicks` held but not used for
  timestamp correction
- chrony falls back to NTP automatically
- gpsd reports fix status via its JSON interface (port 2947) — available for
  future dashboard integration

### 6.2 PPS Without Valid Time

PPS can arrive before gpsd has a valid time fix. chrony handles this: SHM 0
has no data, so the PPS source cannot `lock` and is ignored until SHM provides
a valid second. plane-feeder also guards: GPS sync bit requires chrony to
report synchronised status (§5.4), so PPS-only with no valid time leaves sync
bit clear and timestamps in free-running fallback mode.

### 6.3 Oscillator Glitches / Bad PPS Edges

Sanity-check `measuredTicks`: if it deviates more than 100 ppm from nominal
(`|carryover| > 10,000`), discard that interval, hold the previous value, and
log a warning. Catches missing PPS edges, double-edges, and electrical noise
on the PPS line.

### 6.4 F9P Power Cycle

- gpsd handles serial port reconnection automatically
- F9P config is saved to flash — comes back with correct settings
- No impact on FPGA or plane-feeder; they simply see PPS stop and then resume

## 7. Data Flow Summary

```
F9P UART ──→ 3V3_IO2 ──→ EMIO UART0 ──→ /dev/ttyPS0 ──→ gpsd ──→ SHM 0 ──→ chrony
F9P PPS  ──→ 3V3_IO1 ──→ FPGA ──┬──→ timestamp_counter (PPS latch, TOA discipline)
                                 └──→ EMIO GPIO[17] ──→ /dev/pps0 ──→ chrony

plane-feeder:
  ├── PPS watcher goroutine (polls PPS registers at ~2 Hz)
  ├── reads FIFO (msg + TOA + RPL)
  ├── consumes latest PpsTimeRef from watcher
  ├── computes carryover, corrected tick rate
  ├── queries chrony sync status (~2s interval, 4s expiry)
  ├── encodes Radarcape Beast timestamps with measured clock rate
  └── sets/clears GPS sync bit based on PPS health + chrony lock
```

## 8. Implementation Checklist

### Hardware / FPGA
- [ ] Confirm 3V3_IO1/IO2/IO3 ball assignments from Bank 13 schematic
- [ ] `build_vendor.tcl`: replace GND tie-off with external `pps_in` BD port
- [ ] `system_top.v`: add `pps_in` input port, connect through system_wrapper
- [ ] `system_top.v`: wire `gpio_i[17] = pps_in` for EMIO GPIO[17]
- [ ] `plane_watcher_integration.xdc`: add PPS pin constraint
- [ ] Vivado: enable EMIO UART0, add UART pin constraints
- [ ] Rebuild bitstream

### Linux / Device Tree
- [ ] Device tree: add pps-gpio node (GPIO 71), enable uart0
- [ ] Update kernel boot args: `console=ttyPS1,115200` (UART1 is now ttyPS1)
- [ ] Verify kernel has CONFIG_PPS_CLIENT_GPIO
- [ ] Cross-compile gpsd and chrony for the target
- [ ] Write init script: gpsd → chrony → plane-feeder
- [ ] Write ubxtool configuration script for F9P first-boot setup

### plane-feeder Software
- [ ] Replace `ReadPps()` with count-stable retry loop (`ReadPpsStable()`)
- [ ] Add dedicated PPS watcher goroutine (polls registers at ~2 Hz)
- [ ] Handle skipped PPS edges (count delta > 1): average ticks, flag degraded
- [ ] Add chrony sync status check (poll chronyc tracking via Unix socket, 2s cadence, 4s expiry)
- [ ] Use measuredTicks in Radarcape nanos calculation
- [ ] Gate GPS sync bit on: PPS health + no skipped edges + chrony synced (no gpsd fallback)
- [ ] Add GPS/clock stats to dashboard output

### Tests
- [ ] Unit: non-nominal measuredTicks (e.g. -15 ppm → 99,998,500 ticks/s)
- [ ] Unit: skipped PPS counts (delta > 1 → averaged carryover, sync bit clear)
- [ ] Unit: GPS lock → unlock → re-lock transitions (sync bit toggles correctly)
- [ ] Unit: midnight UTC rollover (seconds wrap from 86399 to 0)
- [ ] Unit: chrony not-yet-synced → sync bit stays clear even with valid PPS
- [ ] Unit: ReadPpsStable retries on torn read (mock reader that changes count mid-read)
- [ ] Unit: existing PPS boundary crossing and fallback tests still pass
- [ ] End-to-end: verify chrony locks to GPS, Beast timestamps correct
