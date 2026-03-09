---

# FPGA ADS-B / Mode-S Receiver

## Hardware Architecture Specification

---

# Overview

> **Note:** This is a **pre-hardware design document** (original concept phase, circa 2026-03).
> The actual deployed hardware is **Fishball** (PlutoSDR-compatible variant) with **Zynq-7020** and **AD9363 transceiver**.
> This doc describes the original aspirational design and serves as reference for past choices.

This document describes the **hardware architecture** of a standalone **ADS-B / Mode-S receiver** designed for:

* **MLAT participation**
* **FPGA-based timestamping**
* **GNSS disciplined timing**
* **Cost-optimised production**

The receiver is built around a **Zynq SoC** that combines:

* ARM CPU (Linux)
* FPGA fabric (DSP + timestamp engine)

---

# System Block Diagram

```
              ┌─────────────────────┐
              │     GNSS Module     │
              │                     │
              │  PPS  ──────────────┼─────► FPGA
              │  10 MHz ────────────┼─────► Clock Synth
              │  UART ──────────────┼─────► Linux
              └─────────┬───────────┘
                        │
                        ▼

      ┌───────────────────────────────────┐
      │            Clock Tree             │
      │                                   │
      │   10 MHz ref → PLL / Synth → ADC  │
      │                          → FPGA   │
      └───────────────┬───────────────────┘
                      │
                      ▼

1090 MHz Antenna
      │
      ▼
RF Frontend Brick
(SAW → LNA → SAW → attenuator)
      │
      ▼
ADC / Sampler
      │
      ▼
Zynq SoC
   │
   ├── FPGA (PL)
   │    - Correlator
   │    - Timestamp engine
   │    - Mode-S decode
   │
   └── ARM CPU (PS)
        - Linux
        - Beast output
        - Network stack

      │
      ▼
Ethernet PHY
      │
      ▼
RJ45
```

---

# Core Processing Platform

## SoC

Recommended devices:

| Device    | Notes                                   |
| --------- | --------------------------------------- |
| Zynq-7010 | sufficient resources for single channel |
| Zynq-7020 | additional headroom                     |

Primary responsibilities:

### FPGA (PL)

* sample processing
* preamble correlation
* timestamp capture
* Mode-S decode

### ARM CPU (PS)

* Linux runtime
* GNSS interface
* timestamp mapping
* network output

---

# Memory Architecture

Recommended configuration:

| Component  | Size   |
| ---------- | ------ |
| DDR3       | 256 MB |
| QSPI Flash | 16 MB  |

Purpose:

### DDR

* Linux runtime
* message buffers
* logging

### Flash

* bootloader
* FPGA bitstream
* root filesystem

---

# Ethernet Interface

Receiver operates as a **network appliance**.

Recommended:

```
Zynq GEM → Ethernet PHY → RJ45 with magnetics
```

Typical PHY examples:

* RTL8211
* KSZ9031
* LAN8720

Required capabilities:

* 10/100/1000 Ethernet
* RMII or RGMII interface

---

# Clock Tree

Clock design is critical for **MLAT accuracy**.

## Primary Reference

Preferred:

```
GNSS disciplined 10 MHz
```

Fallback:

```
local TCXO
```

## Clock Distribution

```
10 MHz reference
      │
      ▼
Clock synthesizer / PLL
      │
      ├── ADC sampling clock
      └── FPGA processing clock
```

Key requirements:

* low phase noise
* deterministic clock routing
* short trace lengths

---

# GNSS Timing Module

Candidate modules:

| Module         | Notes            |
| -------------- | ---------------- |
| u-blox LEA-6H  | inexpensive      |
| u-blox LEA-M8T | timing-optimised |

Required signals:

| Signal | Destination |
| ------ | ----------- |
| PPS    | FPGA GPIO   |
| 10 MHz | Clock synth |
| UART   | Linux       |

Optional:

* antenna bias supply
* lock status signal

---

# RF Frontend Brick

Implemented as a **separate module** to simplify design and allow iteration.

## Baseline Signal Chain

```
SMA
 → ESD protection
 → 1090 MHz SAW filter
 → LNA
 → second SAW filter
 → optional attenuation
 → receiver input
```

---

## RF Design Goals

* narrowband filtering
* high linearity
* overload tolerance
* minimal insertion loss

---

## Optional RF Features

### Attenuation Pads

Selectable attenuation:

```
0 dB
3 dB
6 dB
10 dB
```

Placement:

* before LNA
* after LNA

Useful for high signal environments (e.g. airport proximity).

---

### LNA Bypass

Allows configuration:

```
Filter → ADC
```

instead of

```
Filter → LNA → ADC
```

---

### Limiter

Optional protection for extreme signal levels.

---

### Bias Tee

Optional power for active antennas.

---

# ADC Architecture

ADC converts filtered RF signal into samples for FPGA processing.

## Target Specification

| Parameter   | Target     |
| ----------- | ---------- |
| Resolution  | 8–12 bits  |
| Sample Rate | 10–40 MSPS |
| Channels    | 1          |

---

## Architecture Options

### Gold Tier

```
12-bit ADC
~40 MSPS
```

Advantages:

* best dynamic range
* improved collision handling

---

### Silver Tier

```
8-bit ADC
~20 MSPS
```

Advantages:

* good performance
* lower cost
* simpler routing

---

### Bronze Tier

```
Analog envelope detector
Comparator
1-bit sampling
```

Advantages:

* minimal cost

Disadvantages:

* reduced decode performance
* worse collision handling

---

# FPGA I/O Allocation

## Required Interfaces

| Signal    | Description        |
| --------- | ------------------ |
| ADC Data  | sample stream      |
| ADC Clock | sampling clock     |
| PPS Input | GNSS timing        |
| UART      | GNSS communication |
| Ethernet  | network output     |

---

# FPGA Timestamp Core

Hardware counter characteristics:

```
64-bit counter
clocked from sampling clock
```

Captured events:

```
PPS edge
preamble detection
```

Registers:

```
counter_at_pps
counter_at_toa
```

Linux later maps timestamps to UTC.

---

# Power Architecture

Typical rails required:

| Rail  | Purpose      |
| ----- | ------------ |
| 1.0 V | Zynq core    |
| 1.8 V | DDR          |
| 3.3 V | IO           |
| 5 V   | input supply |

Power source options:

* external 5 V
* PoE (optional)

---

# Connectors

Recommended external interfaces:

| Interface    | Purpose  |
| ------------ | -------- |
| SMA          | RF input |
| RJ45         | Ethernet |
| GNSS antenna | optional |
| UART header  | debug    |

---

# Thermal Considerations

Zynq SoC may require:

* small heatsink
* airflow if enclosed

RF frontend should be:

* shielded
* isolated from digital noise

---

# PCB Layout Guidelines

## RF Section

* controlled impedance traces
* ground stitching
* shielding can

## Clock Routing

* short traces
* matched impedance
* minimal jitter sources

## Digital Section

* isolate high-speed digital from RF
* proper power decoupling

---

# Mechanical Design

Target form factor:

```
small appliance
external antenna
Ethernet connectivity
```

Potential enclosure types:

* aluminium RF enclosure
* ventilated plastic case

---

# Cost Optimisation Targets

Production BOM goals:

| Component    | Target |
| ------------ | ------ |
| Zynq SoC     | ~$50   |
| GNSS module  | ~$40   |
| ADC          | ~$10   |
| RF frontend  | ~$10   |
| Ethernet PHY | ~$5    |

Approximate BOM target:

```
$120–$150 AUD
```

---

# Future Enhancements

Possible upgrades:

* dual-channel receivers
* MLAT multi-antenna timing
* integrated PoE
* advanced collision recovery

---

# Immediate Hardware Tasks

1. Select ADC device
2. Design RF frontend schematic
3. Design clock tree
4. Define FPGA IO pin mapping
5. Build prototype board

---
