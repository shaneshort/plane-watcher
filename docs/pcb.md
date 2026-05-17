# Prototype RF Board

The active PCB design lives in the separate hardware repo:

```text
/home/shanes/hardware/planewatcher/kicad
```

The current prototype KiCad design supersedes the older AD8318/AD8009/AD9238
requirements baseline that used to live in this file.

## Active Signal Chain

```text
1090 MHz RF input
  -> TQP3M9036 LNA
  -> TA2003A SAW filter
  -> TQP3M9036 LNA
  -> TA2003A SAW filter
  -> ADL5513 logarithmic detector
  -> AD8138 differential ADC driver
  -> AD9203 10-bit 40 MSPS ADC
  -> Smart ZYNQ SL FPGA headers
```

## Firmware/HDL Contract

- ADC data width: `10` bits.
- ADC output format: straight binary.
- ADC target sample clock: `40 MHz`.
- Default bring-up clock: `16 MHz` until the assembled board is validated.
- FPGA clock output to ADC: `adc_encode`.
- FPGA ADC bus input: `adc_data_a[9:0]`.
- FPGA ADC overrange input: `adc_otr_a`.

The active pin constraints are in
[smartzynq_adc_io.xdc](/home/shanes/plane_watcher/hdl/vivado/constr/smartzynq_adc_io.xdc).

## UART And PPS Pins

The Smart ZYNQ build routes PS UARTs through EMIO into PL pins:

| Function | Linux alias | RTL/XDC port | FPGA pin | Direction |
|---|---|---|---|---|
| Console TX | `serial0` / `uart0` | `UART_0_txd` | `L17` | FPGA -> CH340N RX |
| Console RX | `serial0` / `uart0` | `UART_0_rxd` | `M17` | CH340N TX -> FPGA |
| GPS TX | `serial1` / `uart1` | `GPS_UART_txd` | `E16` | FPGA -> GPS RX |
| GPS RX | `serial1` / `uart1` | `GPS_UART_rxd` | `D18` | GPS TX -> FPGA |
| GPS PPS | `pps-gpio` / EMIO GPIO 17 | `gps_pps` | `F16` | GPS PPS -> FPGA |

`gps_pps` is fanned to both the hardware timestamp counter and PS EMIO
GPIO[17]. In Linux device tree this is GPIO `71` (`MIO base 54 + EMIO 17`).

## KiCad Check Snapshot

Checked with KiCad `10.0.3` from the AppImage:

```text
ERC: 0 errors, 0 warnings
DRC: 5 violations
Schematic parity: 0 issues
Unconnected pads: 0
```

The DRC violations are the four mounting holes inside the `Corner Keep-Out`
rule plus one excluded back-layer text warning. Resolve the keepout/mounting
hole rule before fab release so future DRC output stays meaningful.

## Calibration

The ADL5513/AD8138/AD9203 chain still needs board-level bench calibration.
Until then, `log_to_linear.vhd` contains only a monotonic first-light LUT. See
[logdet_frontend_calibration.md](/home/shanes/plane_watcher/docs/logdet_frontend_calibration.md).
