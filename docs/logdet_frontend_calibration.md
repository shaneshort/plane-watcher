# Log-Detector Front-End Calibration

This note tracks the calibration assumptions used by
`hdl/rtl/log_to_linear.vhd` for the prototype Plane Watcher RF board.

Current hardware chain:

```text
TQP3M9036 -> TA2003A -> TQP3M9036 -> TA2003A ->
ADL5513 -> AD8138 -> AD9203 -> FPGA
```

Related files:

- [log_to_linear.vhd](/home/shanes/plane_watcher/hdl/rtl/log_to_linear.vhd)
- [ad9203_ingress.vhd](/home/shanes/plane_watcher/hdl/rtl/ad9203_ingress.vhd)
- [smartzynq_adc_io.xdc](/home/shanes/plane_watcher/hdl/vivado/constr/smartzynq_adc_io.xdc)

## Current State

The active LUT is a first-light placeholder, not a calibrated RPL model.

Known facts from the prototype design and datasheets:

- The ADL5513 measurement output rises with RF input level in dB.
- The nominal ADL5513 slope is `21 mV/dB`.
- The AD9203 is a `10-bit`, `40 MSPS`, `3 V` CMOS ADC.
- The prototype uses straight-binary ADC output into the FPGA.
- The default FPGA encode clock remains `16 MHz` for first power-up; `40 MHz`
  is the board target after SI and capture validation.

## Required Bench Capture

Before treating reported power as calibrated, capture the actual FPGA ADC codes
at `1090 MHz` with known CW input levels at the detector input.

Minimum useful sweep:

| Input level | Capture |
|---:|---|
| `-80 dBm` | quiet/floor anchor |
| `-70 dBm` | weak-signal anchor |
| `-60 dBm` | lower linear-in-dB anchor |
| `-50 dBm` | decode reference candidate |
| `-40 dBm` | mid-range anchor |
| `-30 dBm` | strong-signal anchor |
| `-20 dBm` | upper anchor / compression check |

Record the ADC center code, min/max, OTR count, and whether the bit-toggle
health register shows all ten data bits moving as expected.

## Updating The LUT

Replace the placeholder `CODE_FLOOR`, `CODE_POINTS`, and `DBM_POINTS` arrays in
`log_to_linear.vhd` with measured prototype-board ADC codes. Keep the
calibration in FPGA code space; this automatically includes detector slope,
driver offset/gain, ADC reference/span, and pin-capture polarity.
