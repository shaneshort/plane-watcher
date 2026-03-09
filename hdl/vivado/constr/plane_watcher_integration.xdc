# Hybrid vendor-RX BD notes:
# - These constraints are intentionally board-specific and are derived from the
#   vendor Pluto-style `hdl/projects/pluto/system_constr.xdc`.
# - They apply to the generated BD wrapper top where the AD936x LVDS interface
#   and the physical ENSM pins remain external package pins.
# - "enable" and "txnrx" are the real physical AD936x control pins.
# - "up_enable" and "up_txnrx" are *not* constrained here because they are not
#   board pins in the current design; the hybrid BD hard-drives those internal
#   control-side inputs to a fixed RX bootstrap state.
# - "pps_in" is intentionally *not* constrained here because the current board
#   revision has no real PPS source. The hybrid BD ties PPS inactive for now.

# AD936x physical interface (from vendor `system_constr.xdc`).
set_property -dict {PACKAGE_PIN U18 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports rx_clk_in_p]
set_property -dict {PACKAGE_PIN U19 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports rx_clk_in_n]
set_property -dict {PACKAGE_PIN Y16 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports rx_frame_in_p]
set_property -dict {PACKAGE_PIN Y17 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports rx_frame_in_n]
set_property -dict {PACKAGE_PIN Y18 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_p[0]}]
set_property -dict {PACKAGE_PIN Y19 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_n[0]}]
set_property -dict {PACKAGE_PIN T16 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_p[1]}]
set_property -dict {PACKAGE_PIN U17 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_n[1]}]
set_property -dict {PACKAGE_PIN V20 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_p[2]}]
set_property -dict {PACKAGE_PIN W20 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_n[2]}]
set_property -dict {PACKAGE_PIN T17 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_p[3]}]
set_property -dict {PACKAGE_PIN R18 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_n[3]}]
set_property -dict {PACKAGE_PIN T20 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_p[4]}]
set_property -dict {PACKAGE_PIN U20 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_n[4]}]
set_property -dict {PACKAGE_PIN W18 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_p[5]}]
set_property -dict {PACKAGE_PIN W19 IOSTANDARD LVDS_25 DIFF_TERM TRUE} [get_ports {rx_data_in_n[5]}]
set_property -dict {PACKAGE_PIN U14 IOSTANDARD LVDS_25} [get_ports tx_clk_out_p]
set_property -dict {PACKAGE_PIN U15 IOSTANDARD LVDS_25} [get_ports tx_clk_out_n]
set_property -dict {PACKAGE_PIN V16 IOSTANDARD LVDS_25} [get_ports tx_frame_out_p]
set_property -dict {PACKAGE_PIN W16 IOSTANDARD LVDS_25} [get_ports tx_frame_out_n]
set_property -dict {PACKAGE_PIN V15 IOSTANDARD LVDS_25} [get_ports {tx_data_out_p[0]}]
set_property -dict {PACKAGE_PIN W15 IOSTANDARD LVDS_25} [get_ports {tx_data_out_n[0]}]
set_property -dict {PACKAGE_PIN V12 IOSTANDARD LVDS_25} [get_ports {tx_data_out_p[1]}]
set_property -dict {PACKAGE_PIN W13 IOSTANDARD LVDS_25} [get_ports {tx_data_out_n[1]}]
set_property -dict {PACKAGE_PIN W14 IOSTANDARD LVDS_25} [get_ports {tx_data_out_p[2]}]
set_property -dict {PACKAGE_PIN Y14 IOSTANDARD LVDS_25} [get_ports {tx_data_out_n[2]}]
set_property -dict {PACKAGE_PIN T12 IOSTANDARD LVDS_25} [get_ports {tx_data_out_p[3]}]
set_property -dict {PACKAGE_PIN U12 IOSTANDARD LVDS_25} [get_ports {tx_data_out_n[3]}]
set_property -dict {PACKAGE_PIN T11 IOSTANDARD LVDS_25} [get_ports {tx_data_out_p[4]}]
set_property -dict {PACKAGE_PIN T10 IOSTANDARD LVDS_25} [get_ports {tx_data_out_n[4]}]
set_property -dict {PACKAGE_PIN U13 IOSTANDARD LVDS_25} [get_ports {tx_data_out_p[5]}]
set_property -dict {PACKAGE_PIN V13 IOSTANDARD LVDS_25} [get_ports {tx_data_out_n[5]}]

# Physical ENSM control pins to the AD936x (from vendor `system_constr.xdc` and
# confirmed on the board schematic as G6 = ENABLE and H4 = TXNRX).
set_property -dict {PACKAGE_PIN T15 IOSTANDARD LVCMOS25} [get_ports enable]
set_property -dict {PACKAGE_PIN P18 IOSTANDARD LVCMOS25} [get_ports txnrx]

# Additional AD936x board control pins from the shipped Pluto top-level:
# - gpio_en_agc drives the AD936x EN_AGC pin
# - gpio_resetb drives the AD936x RESETB pin
# For first bring-up the hybrid BD drives:
# - gpio_resetb high, matching RESETB active-low semantics
# - gpio_en_agc low as a conservative fixed bootstrap setting
set_property -dict {PACKAGE_PIN P20 IOSTANDARD LVCMOS25} [get_ports gpio_en_agc]
set_property -dict {PACKAGE_PIN R19 IOSTANDARD LVCMOS25} [get_ports gpio_resetb]

# PS SPI0 EMIO path to the physical AD936x SPI pins from the shipped Pluto
# top-level. The AD936x datasheet names the chip-select input SPI_ENB and the
# board routes it through the SPI CS line:
# - low during a transaction enables the SPI bus
# - high when idle disables it
# Keep the vendor pull-up on chip select so the part idles deselected.
set_property -dict {PACKAGE_PIN R17 IOSTANDARD LVCMOS25 PULLTYPE PULLUP} [get_ports spi_csn]
set_property -dict {PACKAGE_PIN V18 IOSTANDARD LVCMOS25} [get_ports spi_clk]
set_property -dict {PACKAGE_PIN P16 IOSTANDARD LVCMOS25} [get_ports spi_mosi]
set_property -dict {PACKAGE_PIN V17 IOSTANDARD LVCMOS25} [get_ports spi_miso]

# Vendor RX LVDS clock.
#
# The Pluto AD9361 DATA_CLK presented here is about 61.44 MHz in the current
# 2R2T capture mode. We still keep margin in implementation signoff, but the
# previous 4.000 ns constraint was effectively asking the rx-domain ingress to
# meet 250 MHz timing. That is far beyond the real interface rate and was
# turning harmless rx_clk-domain changes into false build failures.
#
# Use 8.000 ns (125 MHz) as a conservative signoff target: still roughly 2x
# the real external clock, but no longer so aggressive that normal DSP ingress
# logic cannot route.
create_clock -period 8.000 -name rx_clk_in_p_clk [get_ports rx_clk_in_p]

# CDC exceptions for the sample FIFO pointer synchronizers are applied from
# `plane_watcher_post_impl_overrides.tcl`, where the routed netlist hierarchy
# is available reliably.
# The earlier toggle+hold CDC constraints (`cdc_toggle_src`, `cdc_power_hold`)
# were removed when the sample FIFO replaced that crossing scheme.

# Debug counter snapshots: rx_clk-domain counters captured by the snapshot
# process on S_AXI_ACLK. These are software-triggered diagnostic captures —
# values are stable for millions of cycles between snapshots, so the crossing
# is safe by construction.
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */sample_power_max_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */sample_power_max_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */raw_power_max_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */raw_power_max_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */raw_iq_75pct_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */raw_iq_75pct_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */raw_iq_87p5pct_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */raw_iq_87p5pct_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */raw_iq_near_rail_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */raw_iq_near_rail_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */raw_power_sat_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */raw_power_sat_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */raw_power_thr_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */raw_power_thresh_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */edge_thresh_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */edge_thresh_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */power_thresh_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */power_thresh_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */sample_fifo_overflow_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */sample_fifo_overflow_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */rx_valid_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */rx_valid_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */sample_valid_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */sample_valid_count_snap_reg*/D}]
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && NAME =~ */rx_clk_count_reg*/Q}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */rx_clk_count_snap_reg*/D}]

# RX-domain soft reset: toggle crosses from S_AXI_ACLK → rx_clk
set_false_path -from [get_pins  -hierarchical -filter {REF_PIN_NAME == Q && (NAME =~ */soft_reset_toggle_reg*/Q || NAME =~ */soft_reset_toggle_i_reg*/Q)}] \
               -to   [get_pins  -hierarchical -filter {REF_PIN_NAME == D && NAME =~ */rx_soft_tog_meta_reg*/D}]
