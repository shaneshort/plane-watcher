if {[llength $argv] < 1} {
    puts stderr "usage: vivado -mode batch -source create_zynq_bd.tcl -tclargs <project.xpr> ?base_addr?"
    exit 1
}

set project_file [file normalize [lindex $argv 0]]
if {[llength $argv] >= 2} {
    set base_addr [lindex $argv 1]
} else {
    set base_addr 0x43D00000
}

open_project $project_file

set design_name zynq_adsb

set bd_file [get_files -quiet */bd/$design_name/$design_name.bd]
if {[llength $bd_file] > 0} {
    remove_files $bd_file
}

create_bd_design $design_name
current_bd_design $design_name

create_bd_cell -type ip -vlnv xilinx.com:ip:processing_system7 processing_system7_0
create_bd_cell -type ip -vlnv xilinx.com:ip:proc_sys_reset proc_sys_reset_0
create_bd_cell -type ip -vlnv xilinx.com:ip:axi_interconnect axi_interconnect_0
create_bd_cell -type ip -vlnv xilinx.com:ip:xlconcat xlconcat_0
create_bd_cell -type module -reference adsb_vendor_wrapper adsb_vendor_wrapper_0

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

set_property -dict [list \
    CONFIG.NUM_MI {1} \
    CONFIG.NUM_SI {1} \
] [get_bd_cells axi_interconnect_0]

set_property CONFIG.NUM_PORTS {1} [get_bd_cells xlconcat_0]

make_bd_intf_pins_external [get_bd_intf_pins processing_system7_0/DDR]
make_bd_intf_pins_external [get_bd_intf_pins processing_system7_0/FIXED_IO]

create_bd_port -dir I -type clk -freq_hz 61440000 rx_clk
create_bd_port -dir I -type rst rx_reset
create_bd_port -dir I -from 15 -to 0 rx_i
create_bd_port -dir I -from 15 -to 0 rx_q
create_bd_port -dir I rx_valid
create_bd_port -dir I pps_in

connect_bd_net [get_bd_ports rx_clk] [get_bd_pins adsb_vendor_wrapper_0/rx_clk]
connect_bd_net [get_bd_ports rx_reset] [get_bd_pins adsb_vendor_wrapper_0/rx_reset]
connect_bd_net [get_bd_ports rx_i] [get_bd_pins adsb_vendor_wrapper_0/rx_i]
connect_bd_net [get_bd_ports rx_q] [get_bd_pins adsb_vendor_wrapper_0/rx_q]
connect_bd_net [get_bd_ports rx_valid] [get_bd_pins adsb_vendor_wrapper_0/rx_valid]
connect_bd_net [get_bd_ports pps_in] [get_bd_pins adsb_vendor_wrapper_0/pps_in]

connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins adsb_vendor_wrapper_0/S_AXI_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/S00_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins axi_interconnect_0/M00_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins proc_sys_reset_0/slowest_sync_clk]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins processing_system7_0/M_AXI_GP0_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins processing_system7_0/S_AXI_HP1_ACLK]
connect_bd_net [get_bd_pins processing_system7_0/FCLK_CLK0] [get_bd_pins processing_system7_0/S_AXI_HP2_ACLK]

connect_bd_net [get_bd_pins processing_system7_0/FCLK_RESET0_N] [get_bd_pins proc_sys_reset_0/ext_reset_in]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/S00_ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/interconnect_aresetn] [get_bd_pins axi_interconnect_0/M00_ARESETN]
connect_bd_net [get_bd_pins proc_sys_reset_0/peripheral_aresetn] [get_bd_pins adsb_vendor_wrapper_0/S_AXI_ARESETN]

connect_bd_intf_net [get_bd_intf_pins processing_system7_0/M_AXI_GP0] [get_bd_intf_pins axi_interconnect_0/S00_AXI]
connect_bd_intf_net [get_bd_intf_pins axi_interconnect_0/M00_AXI] [get_bd_intf_pins adsb_vendor_wrapper_0/S_AXI]

connect_bd_net [get_bd_pins adsb_vendor_wrapper_0/irq] [get_bd_pins xlconcat_0/In0]
connect_bd_net [get_bd_pins xlconcat_0/dout] [get_bd_pins processing_system7_0/IRQ_F2P]

assign_bd_address
set adsb_segments [get_bd_addr_segs -quiet -filter {NAME =~ "*adsb_vendor_wrapper_0*"}]
if {[llength $adsb_segments] > 0} {
    set_property offset $base_addr [lindex $adsb_segments 0]
}

validate_bd_design
save_bd_design

puts "Created block design $design_name in $project_file"
puts "Requested AXI base address: $base_addr"
puts "RX clock, RX reset, RX I/Q, RX valid, and PPS remain external BD ports."

close_project
