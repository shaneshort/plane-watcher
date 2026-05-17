# Smart ZYNQ SL — AD9203 log-detector frontend I/O constraints.
#
# Prototype board chain:
#   TQP3M9036 -> TA2003A -> TQP3M9036 -> TA2003A ->
#   ADL5513 -> AD8138 -> AD9203 -> FPGA
#
# The KiCad prototype sheet maps the AD9203 10-bit straight-binary CMOS bus
# directly to Bank 33/35 header nets named by FPGA package ball.
#
# Physical pin map (AD9203 signal -> FPGA pin -> RTL port):
#
#   CLK  U22   adc_encode       (FPGA -> ADC clock)
#   D0   V13   adc_data_a[0]
#   D1   Y13   adc_data_a[1]
#   D2   AB14  adc_data_a[2]
#   D3   Y18   adc_data_a[3]
#   D4   AB16  adc_data_a[4]
#   D5   AA19  adc_data_a[5]
#   D6   AB19  adc_data_a[6]
#   D7   AB21  adc_data_a[7]
#   D8   AB22  adc_data_a[8]
#   D9   Y21   adc_data_a[9]
#   OTR  W22   adc_otr_a
#
# The AD9203 DRVDD rail is 3 V on the prototype, so the FPGA bank must remain
# configured for compatible LVCMOS signalling before this bitstream is used on
# hardware. PULLDOWN parks the FPGA inputs when the ADC/prototype board is not
# fitted.

# AD9203 sample clock (FPGA -> ADC CLK).
set_property -dict {PACKAGE_PIN U22 IOSTANDARD LVCMOS33 SLEW FAST DRIVE 8} [get_ports adc_encode]

# AD9203 data bus (ADC -> FPGA).
set_property -dict {PACKAGE_PIN V13  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[0]}]
set_property -dict {PACKAGE_PIN Y13  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[1]}]
set_property -dict {PACKAGE_PIN AB14 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[2]}]
set_property -dict {PACKAGE_PIN Y18  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[3]}]
set_property -dict {PACKAGE_PIN AB16 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[4]}]
set_property -dict {PACKAGE_PIN AA19 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[5]}]
set_property -dict {PACKAGE_PIN AB19 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[6]}]
set_property -dict {PACKAGE_PIN AB21 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[7]}]
set_property -dict {PACKAGE_PIN AB22 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[8]}]
set_property -dict {PACKAGE_PIN Y21  IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports {adc_data_a[9]}]

# AD9203 out-of-range flag.
set_property -dict {PACKAGE_PIN W22 IOSTANDARD LVCMOS33 PULLTYPE PULLDOWN} [get_ports adc_otr_a]
