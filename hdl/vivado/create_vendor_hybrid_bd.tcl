if {[llength $argv] < 2} {
    puts stderr "usage: vivado -mode batch -source create_vendor_hybrid_bd.tcl -tclargs <project.xpr> <vendor_hdl_root> ?base_addr?"
    exit 1
}

set project_file [file normalize [lindex $argv 0]]
set vendor_hdl_root [file normalize [lindex $argv 1]]
if {[llength $argv] >= 3} {
    set base_addr [lindex $argv 2]
} else {
    set base_addr 0x43D00000
}

open_project $project_file

set script_dir [file dirname [file normalize [info script]]]
set design_name zynq_adsb_vendor
set ad_hdl_dir [file join $vendor_hdl_root hdl]

set bd_file [get_files -quiet */bd/$design_name/$design_name.bd]
if {[llength $bd_file] > 0} {
    remove_files $bd_file
}

source [file join $vendor_hdl_root hdl projects scripts adi_board.tcl]
source [file join $vendor_hdl_root hdl projects common xilinx adi_fir_filter_bd.tcl]

create_bd_design $design_name
current_bd_design $design_name

create_bd_cell -type ip -vlnv xilinx.com:ip:processing_system7 processing_system7_0
create_bd_cell -type ip -vlnv xilinx.com:ip:proc_sys_reset proc_sys_reset_0
create_bd_cell -type ip -vlnv xilinx.com:ip:proc_sys_reset proc_sys_reset_rx
create_bd_cell -type ip -vlnv xilinx.com:ip:axi_interconnect axi_interconnect_0
create_bd_cell -type ip -vlnv xilinx.com:ip:xlconcat xlconcat_0
create_bd_cell -type ip -vlnv xilinx.com:ip:xlslice gpio_up_enable_slice
create_bd_cell -type ip -vlnv xilinx.com:ip:xlslice gpio_up_txnrx_slice
create_bd_cell -type module -reference adsb_vendor_wrapper adsb_vendor_wrapper_0

ad_ip_instance axi_ad9361 axi_ad9361
ad_ip_parameter axi_ad9361 CONFIG.ID 0
ad_ip_parameter axi_ad9361 CONFIG.CMOS_OR_LVDS_N 0
ad_ip_parameter axi_ad9361 CONFIG.MODE_1R1T 0
ad_ip_parameter axi_ad9361 CONFIG.ADC_INIT_DELAY 30

ad_add_decimation_filter "rx_fir_decimator" 8 2 1 {61.44} {61.44} \
    "$vendor_hdl_root/hdl/library/util_fir_int/coefile_int.coe"

set_property -dict [list \
    CONFIG.PCW_PRESET_BANK0_VOLTAGE {LVCMOS 3.3V} \
    CONFIG.PCW_PRESET_BANK1_VOLTAGE {LVCMOS 1.8V} \
    CONFIG.PCW_PACKAGE_NAME {clg400} \
    CONFIG.PCW_USE_M_AXI_GP0 {1} \
    CONFIG.PCW_USE_S_AXI_HP1 {1} \
    CONFIG.PCW_USE_S_AXI_HP2 {1} \
    CONFIG.PCW_EN_CLK1_PORT {1} \
    CONFIG.PCW_EN_RST1_PORT {1} \
    CONFIG.PCW_FPGA0_PERIPHERAL_FREQMHZ {100.0} \
    CONFIG.PCW_FPGA1_PERIPHERAL_FREQMHZ {200.0} \
    CONFIG.PCW_GPIO_MIO_GPIO_ENABLE {1} \
    CONFIG.PCW_GPIO_MIO_GPIO_IO {MIO} \
    CONFIG.PCW_GPIO_EMIO_GPIO_ENABLE {1} \
    CONFIG.PCW_GPIO_EMIO_GPIO_IO {18} \
    CONFIG.PCW_ENET0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_ENET0_ENET0_IO {MIO 16 .. 27} \
    CONFIG.PCW_ENET0_GRP_MDIO_ENABLE {1} \
    CONFIG.PCW_ENET0_GRP_MDIO_IO {MIO 52 .. 53} \
    CONFIG.PCW_SD0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_SDIO_PERIPHERAL_FREQMHZ {50} \
    CONFIG.PCW_UART1_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_UART1_UART1_IO {MIO 8 .. 9} \
    CONFIG.PCW_QSPI_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_QSPI_GRP_SINGLE_SS_ENABLE {1} \
    CONFIG.PCW_SPI0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_SPI0_SPI0_IO {EMIO} \
    CONFIG.PCW_SPI1_PERIPHERAL_ENABLE {0} \
    CONFIG.PCW_I2C0_PERIPHERAL_ENABLE {0} \
    CONFIG.PCW_I2C1_PERIPHERAL_ENABLE {0} \
    CONFIG.PCW_TTC0_PERIPHERAL_ENABLE {0} \
    CONFIG.PCW_USB0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_USB0_RESET_ENABLE {1} \
    CONFIG.PCW_USB0_RESET_IO {MIO 46} \
    CONFIG.PCW_USE_FABRIC_INTERRUPT {1} \
    CONFIG.PCW_IRQ_F2P_INTR {1} \
    CONFIG.PCW_IRQ_F2P_MODE {REVERSE} \
    CONFIG.PCW_MIO_0_PULLUP {enabled} \
    CONFIG.PCW_MIO_9_PULLUP {enabled} \
    CONFIG.PCW_MIO_10_PULLUP {enabled} \
    CONFIG.PCW_MIO_11_PULLUP {enabled} \
    CONFIG.PCW_MIO_48_PULLUP {enabled} \
    CONFIG.PCW_MIO_49_PULLUP {disabled} \
    CONFIG.PCW_MIO_53_PULLUP {enabled} \
    CONFIG.PCW_UIPARAM_DDR_PARTNO {MT41K256M16 RE-125} \
    CONFIG.PCW_UIPARAM_DDR_BUS_WIDTH {32 Bit} \
    CONFIG.PCW_UIPARAM_DDR_USE_INTERNAL_VREF {0} \
    CONFIG.PCW_UIPARAM_DDR_TRAIN_WRITE_LEVEL {1} \
    CONFIG.PCW_UIPARAM_DDR_TRAIN_READ_GATE {1} \
    CONFIG.PCW_UIPARAM_DDR_TRAIN_DATA_EYE {1} \
    CONFIG.PCW_UIPARAM_DDR_DQS_TO_CLK_DELAY_0 {0.048} \
    CONFIG.PCW_UIPARAM_DDR_DQS_TO_CLK_DELAY_1 {0.050} \
    CONFIG.PCW_UIPARAM_DDR_BOARD_DELAY0 {0.241} \
    CONFIG.PCW_UIPARAM_DDR_BOARD_DELAY1 {0.240} \
] [get_bd_cells processing_system7_0]

set_property -dict [list CONFIG.NUM_MI {2} CONFIG.NUM_SI {1}] [get_bd_cells axi_interconnect_0]
set_property CONFIG.NUM_PORTS {1} [get_bd_cells xlconcat_0]
set_property -dict [list CONFIG.DIN_FROM {15} CONFIG.DIN_TO {15} CONFIG.DIN_WIDTH {18} CONFIG.DOUT_WIDTH {1}] [get_bd_cells gpio_up_enable_slice]
set_property -dict [list CONFIG.DIN_FROM {16} CONFIG.DIN_TO {16} CONFIG.DIN_WIDTH {18} CONFIG.DOUT_WIDTH {1}] [get_bd_cells gpio_up_txnrx_slice]

make_bd_intf_pins_external [get_bd_intf_pins processing_system7_0/DDR]
make_bd_intf_pins_external [get_bd_intf_pins processing_system7_0/FIXED_IO]

# AD936x physical interface ports from vendor design.
#
# Bootstrap control-path note:
# - This board flow intentionally does not recreate the shipped Pluto PS GPIO
#   sideband path for AD936x control. Instead it hard-drives the minimum set of
#   board pins needed to bring the radio up in a stable RX-capable state.
# - "enable" and "txnrx" remain the real physical AD936x ENSM control pins
#   driven by axi_ad9361.
# - "gpio_resetb" is the real AD936x RESETB pin. RESETB is active low per the
#   datasheet, so it is driven high here to hold the transceiver out of reset.
# - "gpio_en_agc" is the real AD936x EN_AGC pin. It is driven low here as a
#   conservative fixed bootstrap setting.
# - "up_enable" and "up_txnrx" are internal axi_ad9361 control-side inputs.
#   When TDD is inactive, axi_ad9361 forwards them to the physical enable/txnrx
#   outputs.
# - `up_enable` is hard-driven high so the interface stays enabled during
#   bring-up and vendor tuning.
# - `up_txnrx` is restored from PS GPIO EMIO bit 16 so the vendor tune flow can
#   switch between RX/TX during interface calibration.
# - `gpio_resetb` remains hard-driven high and `gpio_en_agc` remains hard-driven
#   low for bring-up simplicity.
create_bd_port -dir I rx_clk_in_p
create_bd_port -dir I rx_clk_in_n
create_bd_port -dir I rx_frame_in_p
create_bd_port -dir I rx_frame_in_n
create_bd_port -dir I -from 5 -to 0 rx_data_in_p
create_bd_port -dir I -from 5 -to 0 rx_data_in_n
create_bd_port -dir O tx_clk_out_p
create_bd_port -dir O tx_clk_out_n
create_bd_port -dir O tx_frame_out_p
create_bd_port -dir O tx_frame_out_n
create_bd_port -dir O -from 5 -to 0 tx_data_out_p
create_bd_port -dir O -from 5 -to 0 tx_data_out_n
create_bd_port -dir O enable
create_bd_port -dir O txnrx
create_bd_port -dir O gpio_resetb
create_bd_port -dir O gpio_en_agc
create_bd_port -dir O spi_csn
create_bd_port -dir O spi_clk
create_bd_port -dir O spi_mosi
create_bd_port -dir I spi_miso

ad_connect rx_clk_in_p axi_ad9361/rx_clk_in_p
ad_connect rx_clk_in_n axi_ad9361/rx_clk_in_n
ad_connect rx_frame_in_p axi_ad9361/rx_frame_in_p
ad_connect rx_frame_in_n axi_ad9361/rx_frame_in_n
ad_connect rx_data_in_p axi_ad9361/rx_data_in_p
ad_connect rx_data_in_n axi_ad9361/rx_data_in_n
ad_connect tx_clk_out_p axi_ad9361/tx_clk_out_p
ad_connect tx_clk_out_n axi_ad9361/tx_clk_out_n
ad_connect tx_frame_out_p axi_ad9361/tx_frame_out_p
ad_connect tx_frame_out_n axi_ad9361/tx_frame_out_n
ad_connect tx_data_out_p axi_ad9361/tx_data_out_p
ad_connect tx_data_out_n axi_ad9361/tx_data_out_n
ad_connect enable axi_ad9361/enable
ad_connect txnrx axi_ad9361/txnrx
ad_connect VCC gpio_resetb
ad_connect GND gpio_en_agc
ad_connect VCC axi_ad9361/up_enable
ad_connect axi_ad9361/tdd_sync GND
connect_bd_net [get_bd_pins processing_system7_0/GPIO_O] [get_bd_pins gpio_up_txnrx_slice/Din]
connect_bd_net [get_bd_pins gpio_up_txnrx_slice/Dout] [get_bd_pins axi_ad9361/up_txnrx]

ad_connect processing_system7_0/FCLK_CLK1 axi_ad9361/delay_clk
ad_connect axi_ad9361/l_clk axi_ad9361/clk

ad_connect axi_ad9361/l_clk rx_fir_decimator/aclk
ad_connect axi_ad9361/adc_valid_i0 rx_fir_decimator/valid_in_0
ad_connect axi_ad9361/adc_enable_i0 rx_fir_decimator/enable_in_0
ad_connect axi_ad9361/adc_data_i0 rx_fir_decimator/data_in_0
ad_connect axi_ad9361/adc_valid_q0 rx_fir_decimator/valid_in_1
ad_connect axi_ad9361/adc_enable_q0 rx_fir_decimator/enable_in_1
ad_connect axi_ad9361/adc_data_q0 rx_fir_decimator/data_in_1
ad_connect VCC rx_fir_decimator/active

# RX clock domain reset: dedicated proc_sys_reset synchronised to l_clk.
# Do not use axi_ad9361/rst here; that reset can pulse during calibration
# and ENSM transitions, which would spuriously reset the custom RX path.
connect_bd_net [get_bd_pins axi_ad9361/l_clk] [get_bd_pins proc_sys_reset_rx/slowest_sync_clk]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_RESET0_N] [get_bd_pins proc_sys_reset_rx/ext_reset_in]

# Hook the decoder wrapper to the vendor RX path.
ad_connect axi_ad9361/l_clk adsb_vendor_wrapper_0/rx_clk
connect_bd_net [get_bd_pins proc_sys_reset_rx/peripheral_reset] [get_bd_pins adsb_vendor_wrapper_0/rx_reset]
ad_connect rx_fir_decimator/data_out_0 adsb_vendor_wrapper_0/rx_i
ad_connect rx_fir_decimator/data_out_1 adsb_vendor_wrapper_0/rx_q
ad_connect rx_fir_decimator/valid_out_0 adsb_vendor_wrapper_0/rx_valid
# This board revision does not expose a real PPS input yet. Keep the wrapper's
# PPS path defined but tied inactive until JP5 or another board-level PPS route
# is intentionally integrated and constrained.
ad_connect GND adsb_vendor_wrapper_0/pps_in

# Shipped Pluto PS SPI0 EMIO path to the physical AD936x SPI pins.
# The AD936x datasheet names chip select SPI_ENB:
# - low during a transaction enables the SPI bus
# - high when idle deselects the part
# This matches the shipped Pluto PS SPI0 -> spi_csn wiring.
connect_bd_net [get_bd_pins processing_system7_0/SPI0_SS_O] [get_bd_ports spi_csn]
connect_bd_net [get_bd_pins processing_system7_0/SPI0_SCLK_O] [get_bd_ports spi_clk]
connect_bd_net [get_bd_pins processing_system7_0/SPI0_MOSI_O] [get_bd_ports spi_mosi]
connect_bd_net [get_bd_ports spi_miso] [get_bd_pins processing_system7_0/SPI0_MISO_I]
connect_bd_net [get_bd_pins GND_1/dout] [get_bd_pins processing_system7_0/SPI0_SCLK_I]
connect_bd_net [get_bd_pins VCC_1/dout] [get_bd_pins processing_system7_0/SPI0_SS_I]
connect_bd_net [get_bd_pins GND_1/dout] [get_bd_pins processing_system7_0/SPI0_MOSI_I]

connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins adsb_vendor_wrapper_0/S_AXI_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/S00_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/M00_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/M01_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins proc_sys_reset_0/slowest_sync_clk]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins processing_system7_0/M_AXI_GP0_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins processing_system7_0/S_AXI_HP1_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins processing_system7_0/S_AXI_HP2_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_ad9361/s_axi_aclk]

connect_bd_net [get_bd_pins processing_system7_0/FCLK_RESET0_N] [get_bd_pins proc_sys_reset_0/ext_reset_in]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/S00_ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/M00_ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/M01_ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/peripheral_aresetn] [get_bd_pins adsb_vendor_wrapper_0/S_AXI_ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/peripheral_aresetn] [get_bd_pins axi_ad9361/s_axi_aresetn]

connect_bd_intf_net [get_bd_intf_pins processing_system7_0/M_AXI_GP0] [get_bd_intf_pins axi_interconnect_0/S00_AXI]
connect_bd_intf_net [get_bd_intf_pins axi_interconnect_0/M00_AXI] [get_bd_intf_pins adsb_vendor_wrapper_0/S_AXI]
connect_bd_intf_net [get_bd_intf_pins axi_interconnect_0/M01_AXI] [get_bd_intf_pins axi_ad9361/s_axi]

connect_bd_net [get_bd_pins adsb_vendor_wrapper_0/irq] [get_bd_pins xlconcat_0/In0]
connect_bd_net [get_bd_pins xlconcat_0/dout] [get_bd_pins processing_system7_0/IRQ_F2P]

assign_bd_address
set adsb_segments [get_bd_addr_segs -quiet -filter {NAME =~ "*adsb_vendor_wrapper_0*"}]
if {[llength $adsb_segments] > 0} {
    set adsb_seg [lindex $adsb_segments 0]
    set_property offset $base_addr $adsb_seg
    puts "ADS-B segment: offset=[get_property offset $adsb_seg] range=[get_property range $adsb_seg]"
}

validate_bd_design
save_bd_design

puts "Created hybrid vendor RX block design $design_name"
puts "Vendor HDL root: $vendor_hdl_root"
puts "Requested AXI base address: $base_addr"
puts "Note: PS SPI0 EMIO is wired to the physical AD936x SPI pins."
puts "Note: AD936x resetb is hard-driven high and en_agc low for bootstrap bring-up."
puts "Note: axi_ad9361 up_enable is hard-driven high; up_txnrx is restored from PS GPIO EMIO bit 16 to allow the vendor tune flow to switch RX/TX."
puts "Note: PPS is tied inactive in this board-specific hybrid BD until a real board/header route is selected."

close_project
