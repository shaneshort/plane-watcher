# Smart ZYNQ SL — Phase 1 I/O constraints.
#
# Pin assignments sourced from docs/Smart_ZYNQ_SL_Schematic_Reference.md §5.3.
# The CH340N USB-UART bridge lives on PL-side pins in Bank 34 (L4 differential
# pair). PS UART0 is brought out via EMIO so `ttyPS0` maps to the on-board
# USB-UART — matching the vendor test firmware's console routing.
#
#   L17 = PL_UART_TX  (FPGA → CH340N RX)  ← driven by PS UART0 TXD
#   M17 = PL_UART_RX  (CH340N TX → FPGA)  ← feeds PS UART0 RXD

set_property -dict {PACKAGE_PIN L17 IOSTANDARD LVCMOS33} [get_ports UART_0_txd]
set_property -dict {PACKAGE_PIN M17 IOSTANDARD LVCMOS33} [get_ports UART_0_rxd]

# GPS receiver on spare J5 / Bank 35 header pins (3.3 V fixed). UART naming is
# from the PS perspective: GPS_UART_txd goes to the GPS module RX pin, and
# GPS_UART_rxd is driven by the GPS module TX pin.
set_property -dict {PACKAGE_PIN F16 IOSTANDARD LVCMOS33} [get_ports gps_pps]
set_property -dict {PACKAGE_PIN E16 IOSTANDARD LVCMOS33} [get_ports GPS_UART_txd]
set_property -dict {PACKAGE_PIN D18 IOSTANDARD LVCMOS33} [get_ports GPS_UART_rxd]

# -----------------------------------------------------------------------------
# Ethernet — PS GEM0 via EMIO → gmii_to_rgmii IP → RTL8211E RGMII PHY.
#
# Pin assignments sourced from the hellofpga "Petalinux Chapter 1" tutorial
# for the Smart ZYNQ SP/SP2/SL boards. The RTL8211E sits in Bank 35 on the
# PCB.
# -----------------------------------------------------------------------------

# MDIO
set_property -dict {PACKAGE_PIN G21 IOSTANDARD LVCMOS33} [get_ports MDIO_PHY_mdc]
set_property -dict {PACKAGE_PIN H22 IOSTANDARD LVCMOS33} [get_ports MDIO_PHY_mdio_io]

# RTL8211E reset (active-low). Held HIGH by a constant-1 in the BD so the
# PHY is deterministically out of reset, independent of board pull-up.
set_property -dict {PACKAGE_PIN H17 IOSTANDARD LVCMOS33} [get_ports ETH_RST]

# RGMII receive — from PHY into FPGA.
set_property -dict {PACKAGE_PIN A22 IOSTANDARD LVCMOS33} [get_ports {RGMII_rd[0]}]
set_property -dict {PACKAGE_PIN A18 IOSTANDARD LVCMOS33} [get_ports {RGMII_rd[1]}]
set_property -dict {PACKAGE_PIN A19 IOSTANDARD LVCMOS33} [get_ports {RGMII_rd[2]}]
set_property -dict {PACKAGE_PIN B20 IOSTANDARD LVCMOS33} [get_ports {RGMII_rd[3]}]
set_property -dict {PACKAGE_PIN A21 IOSTANDARD LVCMOS33} [get_ports RGMII_rx_ctl]
set_property -dict {PACKAGE_PIN B19 IOSTANDARD LVCMOS33} [get_ports RGMII_rxc]

# RGMII transmit — from FPGA out to PHY. SLEW FAST is required for the DDR
# signalling at gigabit speeds.
set_property -dict {PACKAGE_PIN E21 IOSTANDARD LVCMOS33} [get_ports {RGMII_td[0]}]
set_property -dict {PACKAGE_PIN F21 IOSTANDARD LVCMOS33} [get_ports {RGMII_td[1]}]
set_property -dict {PACKAGE_PIN F22 IOSTANDARD LVCMOS33} [get_ports {RGMII_td[2]}]
set_property -dict {PACKAGE_PIN G20 IOSTANDARD LVCMOS33} [get_ports {RGMII_td[3]}]
set_property -dict {PACKAGE_PIN G22 IOSTANDARD LVCMOS33} [get_ports RGMII_tx_ctl]
set_property -dict {PACKAGE_PIN D21 IOSTANDARD LVCMOS33} [get_ports RGMII_txc]

set_property SLEW FAST [get_ports {RGMII_td[*]}]
set_property SLEW FAST [get_ports RGMII_tx_ctl]
set_property SLEW FAST [get_ports RGMII_txc]

# Drive strength on the RGMII TX outputs and MDIO is deliberately left at
# the LVCMOS33 default (12 mA on 7-series). An earlier iteration of this
# file specified DRIVE 8, which was absent from the
# xillinux-eval-SmartZynq-SP2-1.0b reference design that works on this
# board family. DRIVE 8 is WEAKER than default, and at 125 MHz DDR rates
# the reduced drive strength is a credible cause of edges arriving at the
# PHY too slow to sample — matching the observed symptom of MAC-side TX
# counters incrementing while no valid frames reach the switch.

# Clock the RGMII RX domain at 125 MHz (8 ns period) — gigabit rate. The IP
# handles DDR sampling via IDELAY primitives.
create_clock -period 8.000 -name RGMII_rxc [get_ports RGMII_rxc]

# Declare the PS fabric clocks and the RGMII RX clock as mutually
# asynchronous. Mirrors the eval design — avoids the tools trying to hold
# pointless cross-domain timing relationships that can over-constrain
# routing.
set_clock_groups -asynchronous \
    -group [get_clocks -include_generated_clocks clk_fpga_0] \
    -group [get_clocks -include_generated_clocks clk_fpga_1] \
    -group [get_clocks -include_generated_clocks RGMII_rxc]

# adc_clk (MMCM output from clk_wiz_adc, 16 MHz) is generated from
# clk_fpga_0 (100 MHz). Vivado normally times paths between a source and
# its MMCM-generated child because they share a PLL reference, but every
# crossing between the two in this design is either an ASYNC_REG 2-flop
# synchroniser (diagnostic taps, config registers inside adsb_top) or a
# proper async FIFO (async_msg_fifo). The large frequency ratio and the
# CDC patterns make the tools' timing analysis pointless on these paths
# — declare them asynchronous so the static timing engine skips them.
set_clock_groups -asynchronous \
    -group [get_clocks clk_fpga_0] \
    -group [get_clocks -of_objects [get_pins -hier -filter {name =~ *clk_wiz_adc*CLKOUT0*}]]

# IDELAY tuning on the RGMII RX pads. Value=8 taps was empirically found to
# be optimal on this RTL8211E board; adjust if eye-scan shows margin issues.
# The IODELAY_GROUP links all delays to the same IDELAYCTRL instance.
set_property IDELAY_VALUE 8    [get_cells -hier -filter {name =~ *delay_rgmii_rx_ctl}]
set_property IDELAY_VALUE 8    [get_cells -hier -filter {name =~ *delay_rgmii_rxd*}]
set_property IODELAY_GROUP gpr1 [get_cells -hier -filter {name =~ *delay_rgmii_rx_ctl}]
set_property IODELAY_GROUP gpr1 [get_cells -hier -filter {name =~ *delay_rgmii_rxd*}]
set_property IODELAY_GROUP gpr1 [get_cells -hier -filter {name =~ *idelayctrl}]

# RGMII RX input delay: PHY drives data with known timing relative to RXC.
# The -1.0/+1.1 ns window matches the RGMII spec valid data window.
set_input_delay -clock [get_clocks RGMII_rxc] -max -1.0             [get_ports {RGMII_rd[*] RGMII_rx_ctl}]
set_input_delay -clock [get_clocks RGMII_rxc] -min  1.1             [get_ports {RGMII_rd[*] RGMII_rx_ctl}]
set_input_delay -clock [get_clocks RGMII_rxc] -clock_fall -max -1.0 -add_delay [get_ports {RGMII_rd[*] RGMII_rx_ctl}]
set_input_delay -clock [get_clocks RGMII_rxc] -clock_fall -min  1.1 -add_delay [get_ports {RGMII_rd[*] RGMII_rx_ctl}]

# RGMII TX timing: with RGMII_TXC_SKEW=0 ("Skew added by PHY"), the IP
# emits TXC aligned with the TX data and the RTL8211E's strap-configured
# internal delay provides the 2 ns TXC offset. The IP's packaged XDC
# handles source-synchronous TX timing internally; we don't add
# set_output_delay at this level to avoid referencing generated clock
# names that may not be stable across IP versions. If timing pressure
# shows up in impl reports, revisit with clock names read from the
# post-synth design.
#
# False paths for IP-internal synchronisers — harmless if the wildcard
# patterns don't match in future IP revisions.
set_false_path -to [get_pins -hier -filter {name =~ *idelayctrl_reset_gen/*reset_sync*/PRE}]
set_false_path -to [get_pins -of [get_cells -hier -filter {name =~ *i_MANAGEMENT/SYNC_*/data_sync*}] -filter {name =~ *D}]
set_false_path -to [get_pins -hier -filter {name =~ *reset_sync*/PRE}]
