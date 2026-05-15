# Smart ZYNQ SL — AD9238 log-detector frontend I/O constraints.
#
# A 65 MSPS ADC breakout ("High_Speed_ADC_V1.0") is plugged directly onto
# the J6 / Bank 33 header — no jumper wires. The breakout header is shorter
# than J6, so its BCL/BOT row physically overhangs the end of J6 and makes
# no electrical contact. The breakout is powered separately (5V terminal
# block on-board, not through the J6 header).
#
# The carrier board accepts either an AD9248 (14-bit) or AD9238 (12-bit) in
# the same socket; the data bus on the header is laid out as a 14-bit field
# so the AD9238's 12 output bits land on the upper twelve positions D[13:2].
# We are populating an AD9238, so chip D[13:2] carry the full 12-bit sample
# and map to FPGA adc_data_a[11:0]. Header positions D[1:0] (FPGA pins T22
# and U22) are unused on the AD9238 and are deliberately omitted from this
# XDC — the FPGA input buffers harmlessly receive whatever floats on those
# lines and nothing consumes them.
#
# Channel B is not wired. Inside the RTL, adsb_logdet_bringup ties the
# ad9238_ingress B-channel inputs to '0' for completeness.
#
# Physical pin map (breakout pin → FPGA pin → signal). Header bit numbering
# follows the AD9248 (14-bit) layout; on the fitted AD9238 the chip's 12
# output bits drive header positions D[13:2] and D[1:0] are unused.
#
#   ACL AB16   adc_encode (FPGA → ADC CLK)
#   AOT AA16   adc_otr_a
#   D0  T22    (unused — AD9238 fitted)
#   D1  U22    (unused — AD9238 fitted)
#   D2  V22    adc_data_a[0]   (AD9238 D0)
#   D3  W22    adc_data_a[1]   (AD9238 D1)
#   D4  Y20    adc_data_a[2]   (AD9238 D2)
#   D5  Y21    adc_data_a[3]   (AD9238 D3)
#   D6  AA22   adc_data_a[4]   (AD9238 D4)
#   D7  AB22   adc_data_a[5]   (AD9238 D5)
#   D8  AA21   adc_data_a[6]   (AD9238 D6)
#   D9  AB21   adc_data_a[7]   (AD9238 D7)
#   D10 AB20   adc_data_a[8]   (AD9238 D8)
#   D11 AB19   adc_data_a[9]   (AD9238 D9)
#   D12 Y19    adc_data_a[10]  (AD9238 D10)
#   D13 AA19   adc_data_a[11]  (AD9238 D11, MSB)

# -----------------------------------------------------------------------------
# AD9238 encode clock (FPGA → ADC CLK, breakout pin ACL).
#
# Driven via an ODDR at the top level. SLEW FAST is required so the edge
# meets the AD9238's CMOS ENCODE input slew-rate specification (≤ ~2 ns
# rise/fall) — slowing the driver causes the receiver to dwell in its
# linear region, which produces aperture jitter and double-clocking under
# noise.
#
# DRIVE is dropped from the default 12 mA to 8 mA to soften the di/dt step
# that the encode toggle dumps into VCCIO_33 each edge. SSN on the bank
# supply was coupling onto the breakout's Vocm reference via the shared
# ground. At 8 mA SLEW FAST the source-side edge is still ~1 ns, so the
# receiver edge stays comfortably inside the AD9238 aperture-jitter spec
# even after the 22 Ω series R and the trace.
#
# Do not drop further to 4 mA — that pushes the receiver edge to ~2 ns,
# i.e. right at the datasheet limit. Do not select SLEW SLOW under any
# circumstances; that is unconditionally out of spec for the AD9238 ENCODE
# input.
#
# The unterminated header trace into the high-Z CMOS input rings; that is
# expected to be controlled by a series source-termination resistor (~22 Ω,
# fitted at the FPGA end of the trace), not by weakening the driver.
# -----------------------------------------------------------------------------
# Test route: move ADC encode to the far end of J6 / Bank 33 so the clock can
# be jumpered as signal+ground to the ADC side of the lifted 0R link.
set_property -dict {PACKAGE_PIN AB16 IOSTANDARD LVCMOS33 SLEW FAST DRIVE 8} [get_ports adc_encode]

# -----------------------------------------------------------------------------
# AD9238 channel A data bus (ADC → FPGA).
# Chip D[11:0] land on header positions D[13:2] → FPGA adc_data_a[11:0].
# -----------------------------------------------------------------------------
# PULLTYPE PULLDOWN parks the input buffer when no ADC is fitted. Without a
# pull, the floating CMOS input dwells near threshold, drawing shoot-through
# current and injecting switching noise into VCCIO_33 — which couples onto
# nearby analog rails (e.g. the breakout's Vocm reference). The ~75 kΩ weak
# pull is overwhelmed by the AD9238 output driver when fitted.
set_property -dict {PACKAGE_PIN V22  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[0]}]
set_property -dict {PACKAGE_PIN W22  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[1]}]
set_property -dict {PACKAGE_PIN Y20  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[2]}]
set_property -dict {PACKAGE_PIN Y21  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[3]}]
set_property -dict {PACKAGE_PIN AA22 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[4]}]
set_property -dict {PACKAGE_PIN AB22 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[5]}]
set_property -dict {PACKAGE_PIN AA21 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[6]}]
set_property -dict {PACKAGE_PIN AB21 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[7]}]
set_property -dict {PACKAGE_PIN AB20 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[8]}]
set_property -dict {PACKAGE_PIN AB19 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[9]}]
set_property -dict {PACKAGE_PIN Y19  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[10]}]
set_property -dict {PACKAGE_PIN AA19 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[11]}]

# -----------------------------------------------------------------------------
# AD9238 channel A out-of-range flag (breakout pin AOT). PULLDOWN for the same
# reason as the data bus.
# -----------------------------------------------------------------------------
set_property -dict {PACKAGE_PIN AA16 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports adc_otr_a]
