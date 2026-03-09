---

# FPGA ADS-B Receiver

## RF Frontend & ADC Selection Guide

---

# Overview

> **Note:** This is a **pre-hardware design document** (ADC selection rationale).
> **Actual ADC choice:** AD9363 transceiver (internal to Fishball, PlutoSDR-compatible).
> No separate ADC brick — RF + sampling are unified in the Fishball module.

This document describes the **RF and sampling architecture** for an FPGA-based ADS-B / Mode-S receiver.

Goals:

* maintain good **decode performance in busy airspace**
* support **precise MLAT timestamping**
* keep **hardware cost reasonable**
* maintain **simple PCB layout**

The RF frontend is implemented as a **separate module ("RF brick")** feeding an ADC connected to the FPGA.
[Note: In actual deployment, the PlutoSDR integrates these into a single module.]

---

# ADS-B Signal Characteristics

| Parameter               | Value                     |
| ----------------------- | ------------------------- |
| Frequency               | 1090 MHz                  |
| Modulation              | Pulse Position Modulation |
| Bit rate                | 1 Mbps                    |
| Pulse width             | 0.5 µs                    |
| Typical signal strength | −90 dBm to −10 dBm        |

Receivers must tolerate **extreme dynamic range** due to:

* nearby aircraft transmissions
* distant weak aircraft
* high traffic density near airports

---

# RF Frontend Architecture

Baseline RF chain:

```
SMA antenna input
    │
    ▼
ESD protection
    │
    ▼
1090 MHz SAW filter
    │
    ▼
Low Noise Amplifier
    │
    ▼
Second SAW filter
    │
    ▼
Optional attenuation pad
    │
    ▼
ADC input
```

---

# RF Design Principles

### Narrowband filtering

1090 MHz SAW filters suppress:

* LTE
* GSM
* radar
* other out-of-band interference

---

### Linearity over gain

Near airports, strong signals can overload receivers.

Therefore:

* prefer **moderate gain**
* avoid excessive LNA gain

---

### Adjustable attenuation

Include configurable pads:

| Pad      | Purpose                   |
| -------- | ------------------------- |
| Pre-LNA  | protect LNA from overload |
| Post-LNA | control level into ADC    |

Typical values:

```
0 dB
3 dB
6 dB
10 dB
```

---

# LNA Selection

Key parameters:

| Parameter    | Target   |
| ------------ | -------- |
| Noise figure | <2 dB    |
| Gain         | 10–15 dB |
| IP3          | high     |

Example devices:

* Mini-Circuits ERA series
* PSA4-5043+
* SPF5189

Trade-off:

Higher gain increases sensitivity but risks overload near airports.

---

# LNA Bypass Option

Include jumper or switch to bypass LNA:

```
Filter → ADC
```

instead of

```
Filter → LNA → ADC
```

Useful for installations directly under flight paths.

---

# Limiter (Optional)

A limiter protects downstream circuits from strong signals.

Pros:

* prevents ADC overload
* protects LNA

Cons:

* slight insertion loss
* additional cost

Often omitted in first revision.

---

# ADC Sampling Strategies

Three main architectures are possible.

---

# Option 1 — Direct RF Sampling

ADC samples the 1090 MHz signal directly.

Requirements:

* very fast ADC (>2 GSPS)
* expensive
* complex design

Not recommended for cost-optimised receiver.

---

# Option 2 — Low-IF Sampling (Recommended)

Convert RF to intermediate frequency.

Example:

```
1090 MHz RF
    │
    ▼
Mixer
    │
    ▼
IF filter (~10–20 MHz)
    │
    ▼
ADC (~20–40 MSPS)
```

Advantages:

* lower ADC speed
* good signal quality

Disadvantages:

* additional mixer
* LO generation

---

# Option 3 — Envelope Detection / Slicer

Convert RF to pulse envelope.

Architecture:

```
RF → envelope detector → comparator → FPGA
```

Advantages:

* lowest cost
* minimal ADC requirements

Disadvantages:

* loses amplitude information
* weaker collision recovery
* less accurate timing

---

# Recommended Approach

For this project:

**Low-IF sampling or baseband sampling** provides the best balance.

Preferred ADC specification:

| Parameter   | Target     |
| ----------- | ---------- |
| Resolution  | 8–12 bits  |
| Sample rate | 20–40 MSPS |
| Channels    | 1          |

---

# Candidate ADC Devices

Example devices worth evaluating:

### AD9280

```
8-bit
32 MSPS
parallel interface
```

Pros:

* inexpensive
* simple interface

---

### AD9235

```
12-bit
65 MSPS
parallel interface
```

Pros:

* higher precision

Cons:

* higher cost

---

### ADS4149

```
14-bit
250 MSPS
LVDS interface
```

Pros:

* very high performance

Cons:

* overkill for ADS-B

---

# ADC Interface to FPGA

Typical interface types:

| Interface     | Notes                  |
| ------------- | ---------------------- |
| Parallel CMOS | easiest                |
| LVDS          | faster, cleaner        |
| JESD204       | unnecessary complexity |

For cost-optimised design:

**parallel CMOS ADC interface** is ideal.

---

# Clocking Requirements

ADC sampling clock must be:

* stable
* low jitter

Clock sources:

```
GNSS disciplined 10 MHz
        │
        ▼
Clock synthesizer
        │
        ▼
ADC clock
```

Fallback:

```
local TCXO
```

---

# Jitter Considerations

Clock jitter affects:

* timestamp precision
* sampling accuracy

Rule of thumb:

```
jitter < 1% of sample period
```

Example:

For 20 MSPS:

```
sample period = 50 ns
target jitter < 500 ps
```

---

# Dynamic Range Considerations

ADS-B receivers must handle:

```
−90 dBm weak aircraft
−10 dBm nearby aircraft
```

Dynamic range required:

```
~80 dB
```

Mitigation strategies:

* adjustable attenuation
* moderate LNA gain
* high-linearity components

---

# RF Layout Guidelines

### RF Section

* controlled impedance traces
* minimal trace length
* solid ground plane
* shielding can

---

### LNA Placement

LNA must be placed:

```
directly after first SAW filter
```

to minimize noise contribution.

---

### ADC Placement

ADC should be:

* close to FPGA
* short data traces
* controlled impedance

---

# PCB Partitioning

Divide board into three zones:

```
RF zone
Clock zone
Digital zone
```

Avoid mixing:

* high speed digital signals
* RF traces

---

# RF Testing Strategy

Useful test points:

| Location  | Purpose               |
| --------- | --------------------- |
| RF input  | verify antenna signal |
| Post LNA  | check gain            |
| ADC input | verify level          |

Optional directional coupler can help monitor signal.

---

# Performance Targets

| Metric              | Target     |
| ------------------- | ---------- |
| Sensitivity         | −90 dBm    |
| Max input           | −10 dBm    |
| Timestamp precision | <100 ns    |
| Decode range        | >200 miles |

---

# Recommended Hardware Configuration

Initial production target:

| Component   | Choice           |
| ----------- | ---------------- |
| ADC         | 8-bit 20–40 MSPS |
| RF frontend | SAW + LNA + SAW  |
| FPGA        | Zynq-7010        |
| GNSS        | LEA-M8T          |
| Clock       | GNSS 10 MHz      |

---

# Cost Targets

Approximate BOM allocation:

| Component    | Cost |
| ------------ | ---- |
| Zynq SoC     | $50  |
| GNSS module  | $40  |
| ADC          | $10  |
| RF frontend  | $10  |
| Ethernet PHY | $5   |

Estimated total:

```
$120–$150 AUD
```

---

# Future Enhancements

Possible upgrades:

* multi-channel receivers
* diversity antennas
* advanced collision recovery
* adaptive gain control

---

# Next Design Tasks

1. Select RF filters
2. Select LNA device
3. Select ADC device
4. Design clock tree
5. Simulate RF chain
6. Prototype RF frontend

---
