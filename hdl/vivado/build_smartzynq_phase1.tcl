# Smart ZYNQ SL (XC7Z020-CLG484) bring-up bitstream.
#
# Layered up in stages:
#   * PS7 with DDR/QSPI/SD/UART and an AXI-GPIO for experimentation.
#   * Ethernet: PS GEM0 → PL gmii_to_rgmii → external RTL8211E RGMII.
#   * Log-detector ADC bring-up: Clocking Wizard generates ENCODE_CLK_HZ,
#     adsb_logdet_bringup drives encode out via ODDR and latches channel A
#     data; live sample + free-running counter are exposed to the PS through
#     two AXI GPIO slaves for `devmem` readback.
#
# This build wires the prototype ADL5513/AD8138/AD9203 log-detector frontend
# through the decode pipeline for first-light validation.
#
# See docs/plans/REWRITE.md for the pin map and design rationale.
#
# Invocation:
#   vivado -mode batch -source build_smartzynq_phase1.tcl \
#       -tclargs <project_dir> ?base_addr? ?jobs? ?encode_mhz?

if {[llength $argv] < 1} {
    puts stderr "usage: vivado -mode batch -source build_smartzynq_phase1.tcl -tclargs <project_dir> ?base_addr? ?jobs? ?encode_mhz?"
    exit 1
}

set project_dir [file normalize [lindex $argv 0]]
set base_addr   [expr {[llength $argv] >= 2 ? [lindex $argv 1] : 0x43C00000}]
set jobs        [expr {[llength $argv] >= 3 ? [lindex $argv 2] : 8}]
# Default encode rate matches logdet_pkg.ENCODE_CLK_HZ = 16 MHz. Override on
# the command line to rebuild at a different rate without editing either file.
# Must be kept in sync with the VHDL constant if anything elsewhere consumes it.
set encode_mhz  [expr {[llength $argv] >= 4 ? [lindex $argv 3] : 16.000}]

set part        xc7z020clg484-2
set project_name plane_watcher_phase1
set bd_name     smartzynq_phase1
set top_name    system_top

set script_dir [file normalize [file dirname [info script]]]
set rtl_dir    [file normalize [file join $script_dir .. rtl]]
set constr_dir [file normalize [file join $script_dir constr]]

file mkdir $project_dir
create_project -force $project_name $project_dir -part $part

# --- Plane-watcher RTL sources --------------------------------------------
#
# Full log-detector frontend + decode pipeline. Order doesn't matter; Vivado
# computes compile order automatically.
#
# adsb_logdet_bringup.vhd MUST stay at the default VHDL-93 file type — it is
# the top of a BD module reference, and Vivado rejects VHDL-2008 in that
# role. Everything further down the hierarchy is marked VHDL-2008 to match
# the rest of the codebase's expected language level.
add_files -norecurse [list \
    [file join $rtl_dir adsb_pkg.vhd] \
    [file join $rtl_dir logdet_pkg.vhd] \
    [file join $rtl_dir ad9203_ingress.vhd] \
    [file join $rtl_dir log_to_linear.vhd] \
    [file join $rtl_dir adsb_crc.vhd] \
    [file join $rtl_dir smallest_bsds.vhd] \
    [file join $rtl_dir bit_flipper.vhd] \
    [file join $rtl_dir bsd_calculator.vhd] \
    [file join $rtl_dir message_decoder.vhd] \
    [file join $rtl_dir preamble_detector.vhd] \
    [file join $rtl_dir adsb_edge_detector.vhd] \
    [file join $rtl_dir adsb_decoder.vhd] \
    [file join $rtl_dir message_aggregator.vhd] \
    [file join $rtl_dir async_msg_fifo.vhd] \
    [file join $rtl_dir async_sample_fifo.vhd] \
    [file join $rtl_dir axi_regs.vhd] \
    [file join $rtl_dir timestamp_counter.vhd] \
    [file join $rtl_dir adsb_top.vhd] \
    [file join $rtl_dir adsb_pl_wrapper.vhd] \
    [file join $rtl_dir adsb_logdet_bringup.vhd] \
]
# Decode-pipeline files use VHDL-2008 constructs; the bring-up wrapper must
# stay VHDL-93 for the BD module reference.
foreach f {
    adsb_pkg.vhd logdet_pkg.vhd log_to_linear.vhd
    adsb_crc.vhd smallest_bsds.vhd bit_flipper.vhd bsd_calculator.vhd
    message_decoder.vhd preamble_detector.vhd adsb_edge_detector.vhd
    adsb_decoder.vhd message_aggregator.vhd async_msg_fifo.vhd
    async_sample_fifo.vhd axi_regs.vhd
    timestamp_counter.vhd adsb_top.vhd adsb_pl_wrapper.vhd
} {
    set_property file_type {VHDL 2008} [get_files $f]
}
update_compile_order -fileset sources_1

# --- Block design ----------------------------------------------------------

create_bd_design $bd_name
current_bd_design $bd_name

create_bd_cell -type ip -vlnv xilinx.com:ip:processing_system7    ps7_0
create_bd_cell -type ip -vlnv xilinx.com:ip:proc_sys_reset        rst_0
create_bd_cell -type ip -vlnv xilinx.com:ip:axi_gpio              axi_gpio_0
# Ethernet support: GEM0 speaks GMII out via EMIO; the gmii_to_rgmii IP
# converts to RGMII for the on-board RTL8211E. The inverter handles the
# reset polarity mismatch (PS FCLK_RESET0_N is active-LOW; gmii_to_rgmii
# rx_reset/tx_reset are active-HIGH). xlconstant ties off the unused
# PHY interrupt input into PS GEM0.
create_bd_cell -type ip -vlnv xilinx.com:ip:gmii_to_rgmii          gmii_to_rgmii_0
create_bd_cell -type ip -vlnv xilinx.com:ip:util_vector_logic      reset_inverter
create_bd_cell -type ip -vlnv xilinx.com:ip:xlconstant             enet_int_tieoff
# Constant HIGH driver for the RTL8211E's active-low reset pin (H17 /
# ETH_RST). Ties PHYRSTB deasserted deterministically — the board has
# an external pull-up, but driving the pin explicitly makes bring-up
# behaviour independent of pull-up strength / timing.
create_bd_cell -type ip -vlnv xilinx.com:ip:xlconstant             eth_reset_tieoff
# PPS is fanned into both the decoder fabric and PS EMIO GPIO[17]. The concat
# drives PS GPIO_I[17] with PPS and ties GPIO_I[16:0] low.
create_bd_cell -type ip -vlnv xilinx.com:ip:xlconstant             gps_gpio_zero
create_bd_cell -type ip -vlnv xilinx.com:ip:xlconcat               gps_gpio_i_concat

# PS7 config for Smart ZYNQ SL. Pinning per docs/Smart_ZYNQ_SL_Schematic_Reference.md:
#   - CLG484 package, 33.333 MHz PS_CLK (from X1)
#   - MT41K256M16 DDR3, 16-bit bus, 533 MHz (single chip)
#   - QSPI single SS: MIO 1-6 + feedback clk on MIO 8 (per hellofpga docs)
#   - SD0 on MIO 40-45, no card detect wired (vendor DT sets has-cd=0)
#   - UART0 on EMIO, routed in XDC to L17/M17 where the CH340N USB-UART lives
#     (vendor firmware uses ttyPS0; this keeps console over the on-board USB)
#   - UART1 on EMIO, routed to spare J5/Bank35 pins for the GPS receiver
#   - EMIO GPIO[17], fed from GPS PPS for Linux pps-gpio/chrony
#   - ENET0 + MDIO on EMIO, feeding the gmii_to_rgmii IP → RTL8211E
#   - FCLK_CLK0 = 100 MHz: AXI interconnect clock (keeps AXI domain slack)
#   - FCLK_CLK1 = 200 MHz: feeds gmii_to_rgmii clkin / IDELAYCTRL (hard req
#     for that IP on Zynq-7000; IOPLL/9 = 200 MHz exactly)
set_property -dict [list \
    CONFIG.PCW_PACKAGE_NAME {clg484} \
    CONFIG.PCW_PRESET_BANK0_VOLTAGE {LVCMOS 3.3V} \
    CONFIG.PCW_PRESET_BANK1_VOLTAGE {LVCMOS 1.8V} \
    CONFIG.PCW_CRYSTAL_PERIPHERAL_FREQMHZ {33.333333} \
    CONFIG.PCW_UIPARAM_DDR_PARTNO {MT41K256M16 RE-125} \
    CONFIG.PCW_UIPARAM_DDR_BUS_WIDTH {16 Bit} \
    CONFIG.PCW_UIPARAM_DDR_FREQ_MHZ {533.333333} \
    CONFIG.PCW_USE_M_AXI_GP0 {1} \
    CONFIG.PCW_FPGA0_PERIPHERAL_FREQMHZ {100.0} \
    CONFIG.PCW_FPGA1_PERIPHERAL_FREQMHZ {200.0} \
    CONFIG.PCW_EN_CLK0_PORT {1} \
    CONFIG.PCW_EN_CLK1_PORT {1} \
    CONFIG.PCW_EN_RST0_PORT {1} \
    CONFIG.PCW_QSPI_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_QSPI_GRP_SINGLE_SS_ENABLE {1} \
    CONFIG.PCW_SD0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_SD0_SD0_IO {MIO 40 .. 45} \
    CONFIG.PCW_UART0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_UART0_UART0_IO {EMIO} \
    CONFIG.PCW_UART1_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_UART1_UART1_IO {EMIO} \
    CONFIG.PCW_UART_PERIPHERAL_FREQMHZ {100} \
    CONFIG.PCW_GPIO_MIO_GPIO_ENABLE {1} \
    CONFIG.PCW_GPIO_MIO_GPIO_IO {MIO} \
    CONFIG.PCW_GPIO_EMIO_GPIO_ENABLE {1} \
    CONFIG.PCW_GPIO_EMIO_GPIO_IO {18} \
    CONFIG.PCW_ENET0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_ENET0_ENET0_IO {EMIO} \
    CONFIG.PCW_ENET0_GRP_MDIO_ENABLE {1} \
    CONFIG.PCW_ENET0_GRP_MDIO_IO {EMIO} \
    CONFIG.PCW_ENET0_RESET_ENABLE {0} \
    CONFIG.PCW_USE_FABRIC_INTERRUPT {0} \
] [get_bd_cells ps7_0]

set_property -dict [list \
    CONFIG.C_GPIO_WIDTH {8} \
    CONFIG.C_ALL_OUTPUTS {0} \
    CONFIG.C_IS_DUAL {0} \
] [get_bd_cells axi_gpio_0]

# gmii_to_rgmii: PHY address 8 (matches vendor DTS gmii_to_rgmii_0@8 node).
# Shared logic included in core so the IP owns its own MMCM/BUFG.
# RGMII_TXC_SKEW=0 → "Skew added by PHY": the IP emits TXC aligned with
# TXD, and the RTL8211E's internal delay (strap-configured) provides the
# 2 ns offset. This matches the HelloFPGA reference configuration for
# the Smart ZYNQ SL; using RGMII_TXC_SKEW=2 (MMCM-added skew) would
# double up on the PHY's strap-added skew and break TX timing.
#
# This setting is a property of the IP↔PHY (RGMII) link, not the MAC↔IP
# (GMII) link. DT phy-mode should be "gmii" because that's the interface
# the MAC actually drives.
set_property -dict [list \
    CONFIG.SupportLevel {Include_Shared_Logic_in_Core} \
    CONFIG.C_PHYADDR {8} \
    CONFIG.RGMII_TXC_SKEW {0} \
] [get_bd_cells gmii_to_rgmii_0]

# Inverter for the reset polarity mismatch.
set_property -dict [list \
    CONFIG.C_SIZE {1} \
    CONFIG.C_OPERATION {not} \
    CONFIG.LOGO_FILE {data/sym_notgate.png} \
] [get_bd_cells reset_inverter]

# Tie off GEM0's unused PHY interrupt input.
set_property -dict [list \
    CONFIG.CONST_WIDTH {1} \
    CONFIG.CONST_VAL {0} \
] [get_bd_cells enet_int_tieoff]

# ETH_RST driven HIGH (PHY not in reset). Active-low on the RTL8211E side.
set_property -dict [list \
    CONFIG.CONST_WIDTH {1} \
    CONFIG.CONST_VAL {1} \
] [get_bd_cells eth_reset_tieoff]

# Tie unused PS EMIO GPIO inputs low; GPIO[17] is replaced with PPS below.
set_property -dict [list \
    CONFIG.CONST_WIDTH {1} \
    CONFIG.CONST_VAL {0} \
] [get_bd_cells gps_gpio_zero]

set_property CONFIG.NUM_PORTS {18} [get_bd_cells gps_gpio_i_concat]

# Apply the standard PS7 automation: hooks up FCLK_CLK0, FCLK_RESET0_N,
# AXI interconnect between M_AXI_GP0 and axi_gpio_0's S_AXI, and proc_sys_reset.
apply_bd_automation -rule xilinx.com:bd_rule:processing_system7 \
    -config {make_external "FIXED_IO, DDR" apply_board_preset "0" Master "Disable" Slave "Disable"} \
    [get_bd_cells ps7_0]

apply_bd_automation -rule xilinx.com:bd_rule:axi4 \
    -config {Master "/ps7_0/M_AXI_GP0" Clk "Auto"} \
    [get_bd_intf_pins axi_gpio_0/S_AXI]

# --- Ethernet wiring ------------------------------------------------------

# PS GEM0 GMII/MDIO interfaces to the gmii_to_rgmii converter.
# Interface pin names on the IP: GMII (GEM-side GMII slave), MDIO_GEM
# (GEM-side MDIO slave), MDIO_PHY (PHY-side master), RGMII (PHY-side master).
# IMPORTANT: the GMII interface bundles only the DATA signals (txd/rxd/ctl);
# TX_CLK and RX_CLK are SEPARATE PS7 pins that must be wired explicitly
# below. Missing these causes PS GEM to have no TX reference clock, link
# comes up at 1 Gbps but TX frames never shift out.
connect_bd_intf_net [get_bd_intf_pins ps7_0/GMII_ETHERNET_0] \
                    [get_bd_intf_pins gmii_to_rgmii_0/GMII]
connect_bd_intf_net [get_bd_intf_pins ps7_0/MDIO_ETHERNET_0] \
                    [get_bd_intf_pins gmii_to_rgmii_0/MDIO_GEM]

# Speed-adaptive GMII clocks from the converter IP to PS GEM0. These step
# between 125 / 25 / 2.5 MHz as the link speed changes, which is exactly
# what the MAC needs for GMII operation at 1000 / 100 / 10 Mbps.
connect_bd_net [get_bd_pins gmii_to_rgmii_0/gmii_tx_clk] \
               [get_bd_pins ps7_0/ENET0_GMII_TX_CLK]
connect_bd_net [get_bd_pins gmii_to_rgmii_0/gmii_rx_clk] \
               [get_bd_pins ps7_0/ENET0_GMII_RX_CLK]

# 200 MHz reference clock for the IP's internal MMCM/IDELAYCTRL.
connect_bd_net [get_bd_pins ps7_0/FCLK_CLK1] \
               [get_bd_pins gmii_to_rgmii_0/clkin]

# Reset: invert active-low FCLK_RESET0_N, drive both rx_reset and tx_reset.
connect_bd_net [get_bd_pins ps7_0/FCLK_RESET0_N] \
               [get_bd_pins reset_inverter/Op1]
connect_bd_net [get_bd_pins reset_inverter/Res] \
               [get_bd_pins gmii_to_rgmii_0/rx_reset] \
               [get_bd_pins gmii_to_rgmii_0/tx_reset]

# Tie off GEM0's PHY interrupt input (RTL8211E's INT line is not wired to PL).
connect_bd_net [get_bd_pins enet_int_tieoff/dout] \
               [get_bd_pins ps7_0/ENET0_EXT_INTIN]

# Expose PS UART0 at the BD boundary so XDC can pin it to L17/M17. Explicit
# name keeps the external sub-signals predictable: UART_0_txd, UART_0_rxd.
make_bd_intf_pins_external -name UART_0 [get_bd_intf_pins ps7_0/UART_0]

# GPS receiver UART over PS UART1 EMIO. XDC pins this to spare J5/Bank35 pins:
# GPS_UART_txd is PS → GPS RX, GPS_UART_rxd is GPS TX → PS.
make_bd_intf_pins_external -name GPS_UART [get_bd_intf_pins ps7_0/UART_1]

# Expose the ethernet-side interfaces: RGMII bus to the PHY, MDIO to the PHY.
make_bd_intf_pins_external -name RGMII     [get_bd_intf_pins gmii_to_rgmii_0/RGMII]
make_bd_intf_pins_external -name MDIO_PHY  [get_bd_intf_pins gmii_to_rgmii_0/MDIO_PHY]

# Expose the PHY reset as a top-level output named ETH_RST. Pinned to H17
# in XDC. Driven by the constant-1 cell above.
create_bd_port -dir O ETH_RST
connect_bd_net [get_bd_pins eth_reset_tieoff/dout] [get_bd_ports ETH_RST]

# --- ADC bring-up ---------------------------------------------------------
#
# Clock tree and data path for the log-detector frontend first-light test:
#
#   FCLK_CLK0 (100 MHz) → clk_wiz_adc → adc_clk ($encode_mhz MHz)
#   adc_clk → adsb_logdet_bringup_0 → ODDR → adc_encode → J6 U22
#   J6 data → adsb_logdet_bringup_0 → 2-flop CDC → AXI GPIO → PS
#
# Two AXI GPIO slaves expose the captured data to Linux:
#   axi_gpio_sample — 32-bit live sample word   (base + 0x1000)
#   axi_gpio_count  — 32-bit free-running count (base + 0x2000)

create_bd_cell -type ip -vlnv xilinx.com:ip:clk_wiz          clk_wiz_adc
create_bd_cell -type ip -vlnv xilinx.com:ip:proc_sys_reset   rst_adc
create_bd_cell -type ip -vlnv xilinx.com:ip:axi_gpio         axi_gpio_sample
create_bd_cell -type ip -vlnv xilinx.com:ip:axi_gpio         axi_gpio_count

# Module-reference cell backed by hdl/rtl/adsb_logdet_bringup.vhd.
create_bd_cell -type module -reference adsb_logdet_bringup adsb_logdet_bringup_0

# Clocking Wizard: 100 MHz → $encode_mhz MHz, locked output used by the
# adc-domain proc_sys_reset. No reset input; FCLK_CLK0 is already glitch-free.
set_property -dict [list \
    CONFIG.PRIM_IN_FREQ {100.000} \
    CONFIG.CLKOUT1_REQUESTED_OUT_FREQ $encode_mhz \
    CONFIG.USE_RESET {false} \
    CONFIG.USE_LOCKED {true} \
] [get_bd_cells clk_wiz_adc]

# Both new GPIO slaves: 32-bit input-only, single channel.
foreach gpio {axi_gpio_sample axi_gpio_count} {
    set_property -dict [list \
        CONFIG.C_GPIO_WIDTH {32} \
        CONFIG.C_ALL_INPUTS {1} \
        CONFIG.C_IS_DUAL {0} \
    ] [get_bd_cells $gpio]
}

# ADC-domain reset: gated on FCLK_RESET0_N AND clk_wiz_adc locked, so the
# ADC datapath stays in reset until the MMCM is stable.
connect_bd_net [get_bd_pins ps7_0/FCLK_CLK0]      [get_bd_pins clk_wiz_adc/clk_in1]
connect_bd_net [get_bd_pins clk_wiz_adc/clk_out1] [get_bd_pins rst_adc/slowest_sync_clk]
connect_bd_net [get_bd_pins ps7_0/FCLK_RESET0_N]  [get_bd_pins rst_adc/ext_reset_in]
connect_bd_net [get_bd_pins clk_wiz_adc/locked]   [get_bd_pins rst_adc/dcm_locked]

# adsb_logdet_bringup: ADC domain from clk_wiz_adc, AXI domain from FCLK_CLK0.
connect_bd_net [get_bd_pins clk_wiz_adc/clk_out1]       [get_bd_pins adsb_logdet_bringup_0/adc_clk]
connect_bd_net [get_bd_pins rst_adc/peripheral_aresetn] [get_bd_pins adsb_logdet_bringup_0/adc_rstn]
connect_bd_net [get_bd_pins ps7_0/FCLK_CLK0]            [get_bd_pins adsb_logdet_bringup_0/S_AXI_ACLK]
connect_bd_net [get_bd_pins rst_0/peripheral_aresetn]   [get_bd_pins adsb_logdet_bringup_0/S_AXI_ARESETN]

# Feed the synchronised live sample / counter into the two AXI GPIO input ports.
connect_bd_net [get_bd_pins adsb_logdet_bringup_0/live_sample_axi] \
               [get_bd_pins axi_gpio_sample/gpio_io_i]
connect_bd_net [get_bd_pins adsb_logdet_bringup_0/sample_count_axi] \
               [get_bd_pins axi_gpio_count/gpio_io_i]

# AXI interconnect for the new slaves.
apply_bd_automation -rule xilinx.com:bd_rule:axi4 \
    -config {Master "/ps7_0/M_AXI_GP0" Clk "Auto"} \
    [get_bd_intf_pins axi_gpio_sample/S_AXI]
apply_bd_automation -rule xilinx.com:bd_rule:axi4 \
    -config {Master "/ps7_0/M_AXI_GP0" Clk "Auto"} \
    [get_bd_intf_pins axi_gpio_count/S_AXI]

# Decode pipeline AXI-Lite slave. Vivado is supposed to auto-infer the
# S_AXI interface from the S_AXI_* port naming on adsb_logdet_bringup_0,
# but the module-reference flow is finicky — sometimes the pin is named
# S_AXI, sometimes it needs to be discovered by filter. Dump what's there
# so we can see, then pick the first AXI slave interface on the cell.

puts "=== adsb_logdet_bringup_0 interface pins ==="
foreach intf [get_bd_intf_pins -of_objects [get_bd_cells adsb_logdet_bringup_0]] {
    puts "  $intf (mode=[get_property MODE $intf], vlnv=[get_property VLNV $intf])"
}
puts "============================================"

set pl_axi_intf_list [get_bd_intf_pins -of_objects [get_bd_cells adsb_logdet_bringup_0] \
                        -filter {MODE == Slave && VLNV =~ xilinx.com:interface:aximm*}]
if {[llength $pl_axi_intf_list] == 0} {
    puts stderr "ERROR: no AXI slave interface found on adsb_logdet_bringup_0"
    exit 1
}
set pl_axi_intf [lindex $pl_axi_intf_list 0]
puts "Wiring AXI slave interface: $pl_axi_intf"

apply_bd_automation -rule xilinx.com:bd_rule:axi4 \
    -config {Master "/ps7_0/M_AXI_GP0" Clk "Auto"} \
    $pl_axi_intf

# External AD9203 pins. XDC constraints live in constr/smartzynq_adc_io.xdc.
create_bd_port -dir O adc_encode
connect_bd_net [get_bd_ports adc_encode] [get_bd_pins adsb_logdet_bringup_0/adc_encode]

create_bd_port -dir I -from 9 -to 0 adc_data_a
connect_bd_net [get_bd_ports adc_data_a] [get_bd_pins adsb_logdet_bringup_0/adc_data_a]

create_bd_port -dir I adc_otr_a
connect_bd_net [get_bd_ports adc_otr_a] [get_bd_pins adsb_logdet_bringup_0/adc_otr_a]

# GPS PPS: direct to the decoder timestamp counter and to PS GPIO[17] so Linux
# can expose /dev/pps0 through the pps-gpio driver.
create_bd_port -dir I gps_pps
connect_bd_net [get_bd_ports gps_pps] \
               [get_bd_pins adsb_logdet_bringup_0/pps_in] \
               [get_bd_pins gps_gpio_i_concat/In17]
for {set i 0} {$i < 17} {incr i} {
    connect_bd_net [get_bd_pins gps_gpio_zero/dout] [get_bd_pins gps_gpio_i_concat/In$i]
}
connect_bd_net [get_bd_pins gps_gpio_i_concat/dout] [get_bd_pins ps7_0/GPIO_I]

# --- Address map ---------------------------------------------------------
#
# axi_gpio_0       : $base_addr + 0x0000  (8-bit experimentation GPIO)
# axi_gpio_sample  : $base_addr + 0x1000  (32-bit ADC live sample)
# axi_gpio_count   : $base_addr + 0x2000  (32-bit sample counter)

assign_bd_address [get_bd_addr_segs {axi_gpio_0/S_AXI/Reg}]
set_property range  4K       [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_0_Reg"]
set_property offset $base_addr [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_0_Reg"]

assign_bd_address [get_bd_addr_segs {axi_gpio_sample/S_AXI/Reg}]
set_property range  4K [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_sample_Reg"]
set_property offset [expr {$base_addr + 0x1000}] [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_sample_Reg"]

assign_bd_address [get_bd_addr_segs {axi_gpio_count/S_AXI/Reg}]
set_property range  4K [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_count_Reg"]
set_property offset [expr {$base_addr + 0x2000}] [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_count_Reg"]

# adsb_logdet_bringup (decode pipeline AXI-Lite): base + 0x3000, 256-byte window.
# Reference segments by the interface pin discovered above — segment names
# depend on Vivado's auto-naming and aren't stable across versions.
set pl_axi_slave_seg  [get_bd_addr_segs -of_objects $pl_axi_intf]
set pl_axi_master_seg [get_bd_addr_segs -of_objects [get_bd_addr_spaces ps7_0/Data] \
                         -filter "NAME =~ *adsb_logdet_bringup*"]
assign_bd_address $pl_axi_slave_seg
set_property range  4K $pl_axi_master_seg
set_property offset [expr {$base_addr + 0x3000}] $pl_axi_master_seg

validate_bd_design
save_bd_design

# Wrap BD into a top-level HDL module and make it the synthesis top.
make_wrapper -files [get_files "$bd_name.bd"] -top
add_files -norecurse [glob $project_dir/$project_name.gen/sources_1/bd/$bd_name/hdl/${bd_name}_wrapper.v]
set_property top ${bd_name}_wrapper [current_fileset]

# Board I/O constraints. UART + ethernet in the phase-1 file; AD9203 pinning
# (clock, data, OTR) in the adc_io file.
add_files -fileset constrs_1 -norecurse [list \
    [file join $constr_dir smartzynq_phase1_io.xdc] \
    [file join $constr_dir smartzynq_adc_io.xdc] \
]

# --- Build -----------------------------------------------------------------

launch_runs synth_1 -jobs $jobs
wait_on_run synth_1
if {[get_property PROGRESS [get_runs synth_1]] ne "100%"} {
    puts stderr "ERROR: synthesis did not complete"
    exit 1
}

launch_runs impl_1 -to_step write_bitstream -jobs $jobs
wait_on_run impl_1
if {[get_property PROGRESS [get_runs impl_1]] ne "100%"} {
    puts stderr "ERROR: implementation/bitstream did not complete"
    exit 1
}

# --- Export XSA (the handoff to Petalinux) --------------------------------

open_run impl_1
set xsa_path [file join $project_dir ${project_name}.xsa]
write_hw_platform -fixed -include_bit -force -file $xsa_path
puts "XSA written to: $xsa_path"
close_design
