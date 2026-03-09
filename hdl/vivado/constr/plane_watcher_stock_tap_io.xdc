# =============================================================================
# plane_watcher_stock_tap_io.xdc — Board-pin constraints for the stock-tap flow
# =============================================================================
# This file contains only the PPS and UART pin constraints that the stock-tap
# flow adds on top of the vendor Pluto system_constr.xdc.
#
# Pin assignments verified against:
#   - Board schematic: 7020_936x_SDR_Schematic.pdf (2025-02-15)
#   - Package pinout:  AMD xc7z020clg400pkg.txt
#
# All signals are on JP5 (expansion header), PL Bank 13, VCCO = 3.3V.
# =============================================================================

# PPS input from F9P GPS module.
# JP5 pin 7 / schematic net 3V3_IO1 / IO_L20N_T3_13 = ball Y13.
# PPS input from F9P GPS module.
# JP5 pin 7 / schematic net 3V3_IO1 / IO_L21N_T3_DQS_13 = ball V10.
# (Corrected: schematic labels by ball not IO function; V10 confirmed
# against AMD xc7z020clg400pkg.txt and board schematic U1G Bank 13.)
set_property -dict {PACKAGE_PIN V10 IOSTANDARD LVCMOS33} [get_ports pps_in]

# F9P GPS UART via EMIO UART0 on JP5 (Bank 13, 3.3V).
# uart0_tx: JP5 pin 11 / schematic net 3V3_IO3 / IO_L12N_T1_MRCC_13 = ball U10.
# uart0_rx: JP5 pin 9  / schematic net 3V3_IO2 / IO_L17P_T2_13      = ball U9.
# (Corrected: uart0_rx was W10, now U9 per schematic U1G Bank 13.)
set_property -dict {PACKAGE_PIN U10 IOSTANDARD LVCMOS33} [get_ports uart0_tx]
set_property -dict {PACKAGE_PIN U9  IOSTANDARD LVCMOS33} [get_ports uart0_rx]
