---

# FPGA ADS-B Receiver

## DSP & Timing Architecture (Reference Design)

## Overview

> **Note:** This is a **pre-hardware design document** (reference architecture).
> **Actual hardware:** Fishball (PlutoSDR-compatible). **Actual sampling path:** 30.72 MHz complex receive stream → squared-power conversion → fractional resampler → 16 MHz effective to decoder.
> Clock domains: rx_clk ~61.44 MHz (vendor RX interface / ingress), S_AXI_ACLK 100 MHz (decode core + timestamp).

This document describes the **signal processing pipeline and timing design** for an FPGA-based ADS-B / Mode-S receiver.

The design goal is to provide:

* **hardware-accelerated ADS-B decode**
* **precise MLAT timestamps**
* **robust decoding in high-traffic airspace**

The architecture draws inspiration from the open-source **bladeRF hardware ADS-B decoder**, which performs **signal processing directly in FPGA rather than sending raw IQ to the host CPU**. ([GitHub][1])

---

# Mode-S / ADS-B Signal Characteristics

| Parameter         | Value                     |
| ----------------- | ------------------------- |
| Carrier frequency | 1090 MHz                  |
| Modulation        | Pulse Position Modulation |
| Bit rate          | 1 Mbps                    |
| Pulse width       | 0.5 µs                    |
| Preamble length   | 8 µs                      |
| Long frame        | 112 bits                  |
| Short frame       | 56 bits                   |

---

# DSP Pipeline Overview

```
ADC samples
   │
   ▼
Magnitude / Envelope
   │
   ▼
Preamble Correlator
   │
   ▼
Peak Detection
   │
   ▼
Timestamp Capture
   │
   ▼
Bit Sampling
   │
   ▼
Mode-S Decode
   │
   ▼
CRC Validation
   │
   ▼
Message FIFO → Linux
```

---

# Sampling Strategy

Recommended configurations:

| Tier   | ADC          | Sample Rate |
| ------ | ------------ | ----------- |
| Gold   | 12-bit       | 40 MSPS     |
| Silver | 8-bit        | 20 MSPS     |
| Bronze | 1-bit slicer | 10 MSPS     |

Higher sample rates provide:

* improved timestamp precision
* better pulse shape reconstruction
* improved collision recovery

The bladeRF reference decoder uses a **12-bit 40 MSPS frontend**, extracting maximum decode performance from the hardware pipeline. ([FlightAware Discussions][2])

---

# Magnitude / Envelope Calculation

Current `plane_watcher` RTL uses squared power:

```
power = (I² + Q²) >> 2
```

The right shift keeps the 12-bit AD9361 payload within the signed 24-bit
decoder power path while preserving a non-negative scalar power quantity.

---

# Preamble Correlation

ADS-B frames begin with a fixed **8-pulse preamble pattern**.

Typical structure:

```
1 0 1 0 0 0 1 0
```

At known timing offsets.

Matched filtering against this pattern produces a correlation peak.

Correlation output:

```
corr[n] = Σ samples[n+i] * coeff[i]
```

Peak detection identifies candidate frames.

---

# Candidate Detection Strategy

## Silver Mode

Single candidate per detection window.

Pros:

* low FPGA usage
* simple pipeline

Cons:

* collisions may be missed

---

## Gold Mode

Track top N correlation peaks.

Workflow:

```
detect peaks
rank by magnitude
attempt decode on each
CRC gate results
```

Benefits:

* collision tolerance
* higher decode rate in dense traffic

---

# Timestamp Capture

Timestamp is taken at **correlation peak**.

FPGA logic:

```
timestamp = sample_counter + fractional_offset
```

Where:

```
sample_counter = free-running hardware counter
fractional_offset = interpolation correction
```

---

# Fractional Timing Interpolation

To improve resolution beyond one sample:

Use quadratic interpolation around the peak.

Example:

```
peak_offset =
    (y[-1] - y[+1]) /
    (2*(y[-1] - 2*y[0] + y[+1]))
```

This gives sub-sample precision.

---

# Bit Sampling

Bit timing:

```
1 µs per bit
0.5 µs pulse width
```

Bit decision:

```
bit = pulse early ? 1 : 0
```

Sampling positions derived from preamble timing.

---

# Mode-S Frame Decode

Steps:

1. Extract 56 or 112 bits
2. Determine frame type
3. Perform CRC check

If CRC passes:

```
frame → output FIFO
```

---

# Collision Handling

Typical ADS-B receivers fail when pulses overlap.

Mitigation strategies:

### Multiple candidate peaks

Try decode on multiple peaks.

### Soft decision metrics

Use pulse amplitude information to resolve ambiguous bits.

### CRC gating

Discard invalid frames.

The bladeRF FPGA decoder demonstrates that hardware pipelines can **resolve many bit errors and packet collisions**, enabling decode ranges greater than 270 miles. ([Nuand][3])

---

# Hardware Timestamp Engine

Timestamping occurs **entirely inside FPGA**.

## Counter

```
64-bit free-running counter
clocked from sample clock
```

---

## PPS Synchronization

GNSS PPS input captured by FPGA.

Registers:

```
counter_at_pps
```

---

## Message Timestamp

When preamble detected:

```
counter_at_toa
```

---

## Output Structure

```
struct adsb_frame {
    uint64 timestamp_counter;
    uint16 fractional_offset;
    uint16 signal_quality;
    uint8  data[14];
};
```

---

# Linux Integration

Linux performs:

* GNSS time management
* timestamp conversion
* network output

Timestamp calculation:

```
timestamp =
    UTC_second +
    (counter - counter_at_pps) / sample_rate
```

---

# Output Protocol

Recommended:

**Beast binary protocol**

Reasons:

* widely supported
* used by dump1090/readsb
* compatible with MLAT networks

---

# Performance Targets

| Metric               | Target          |
| -------------------- | --------------- |
| Timestamp resolution | <100 ns         |
| Maximum message rate | >2500 msgs/s    |
| Decode range         | >200 miles      |
| Collision resilience | multi-candidate |

---

# FPGA Resource Estimate

Approximate usage (Zynq-7010):

| Block          | LUT      |
| -------------- | -------- |
| Magnitude      | low      |
| Correlator     | medium   |
| Decode engine  | medium   |
| Timestamp core | very low |

Total expected utilization:

```
~30–50% of Zynq-7010
```

---

# Key Design Principles

1. Timestamp in **FPGA hardware**
2. Correlate before decoding
3. Keep amplitude information when possible
4. Use CRC to validate frames
5. Separate RF frontend from digital platform

---

# Future Enhancements

Potential upgrades:

* multi-channel MLAT receiver
* adaptive noise floor estimation
* advanced collision separation
* FPGA-based ML inference for signal classification

---

# Recommended Next Step

Before writing FPGA code, define:

1. **exact sample rate**
2. **correlator coefficients**
3. **timestamp clock source**
4. **FIFO interface between FPGA and Linux**

These form the **hardware/software contract**.

---
[1]: https://github.com/Nuand/bladeRF/wiki/FPGA-Development?utm_source=chatgpt.com "FPGA Development · Nuand/bladeRF Wiki"
[2]: https://discussions.flightaware.com/t/high-performance-vhdl-ads-b-decoder-open-sourced/18390?utm_source=chatgpt.com "High performance VHDL ADS-B decoder open sourced"
[3]: https://www.nuand.com/bladerf-vhdl-ads-b-decoder/?utm_source=chatgpt.com "bladeRF VHDL ADS-B decoder"
