# Phase 1 bring-up for the Smart ZYNQ SL (XC7Z020-CLG484).
#
# Goal: minimal PL fabric providing the peripherals Linux needs to come up on
# this board — PS7 with DDR/QSPI/SD/UART, one AXI-GPIO for experimentation,
# and ethernet (PS GEM0 → PL gmii_to_rgmii → external RTL8211E RGMII).
#
# No plane_watcher IP, no MMCM (beyond what gmii_to_rgmii needs), no
# daughterboard pins. See docs/plans/2026-04-16-smart-zynq-sl-port-plan.md.
#
# Invocation:
#   vivado -mode batch -source build_smartzynq_phase1.tcl -tclargs <project_dir> ?base_addr? ?jobs?

if {[llength $argv] < 1} {
    puts stderr "usage: vivado -mode batch -source build_smartzynq_phase1.tcl -tclargs <project_dir> ?base_addr? ?jobs?"
    exit 1
}

set project_dir [file normalize [lindex $argv 0]]
set base_addr   [expr {[llength $argv] >= 2 ? [lindex $argv 1] : 0x43C00000}]
set jobs        [expr {[llength $argv] >= 3 ? [lindex $argv 2] : 8}]

set part        xc7z020clg484-2
set project_name plane_watcher_phase1
set bd_name     smartzynq_phase1
set top_name    system_top

file mkdir $project_dir
create_project -force $project_name $project_dir -part $part

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

# PS7 config for Smart ZYNQ SL. Pinning per docs/Smart_ZYNQ_SL_Schematic_Reference.md:
#   - CLG484 package, 33.333 MHz PS_CLK (from X1)
#   - MT41K256M16 DDR3, 16-bit bus, 533 MHz (single chip)
#   - QSPI single SS: MIO 1-6 + feedback clk on MIO 8 (per hellofpga docs)
#   - SD0 on MIO 40-45, no card detect wired (vendor DT sets has-cd=0)
#   - UART0 on EMIO, routed in XDC to L17/M17 where the CH340N USB-UART lives
#     (vendor firmware uses ttyPS0; this keeps console over the on-board USB)
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
    CONFIG.PCW_UART_PERIPHERAL_FREQMHZ {100} \
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

# Expose the ethernet-side interfaces: RGMII bus to the PHY, MDIO to the PHY.
make_bd_intf_pins_external -name RGMII     [get_bd_intf_pins gmii_to_rgmii_0/RGMII]
make_bd_intf_pins_external -name MDIO_PHY  [get_bd_intf_pins gmii_to_rgmii_0/MDIO_PHY]

# Expose the PHY reset as a top-level output named ETH_RST. Pinned to H17
# in XDC. Driven by the constant-1 cell above.
create_bd_port -dir O ETH_RST
connect_bd_net [get_bd_pins eth_reset_tieoff/dout] [get_bd_ports ETH_RST]

# Pin the AXI-GPIO at the requested base.
assign_bd_address [get_bd_addr_segs {axi_gpio_0/S_AXI/Reg}]
set_property range  4K       [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_0_Reg"]
set_property offset $base_addr [get_bd_addr_segs "ps7_0/Data/SEG_axi_gpio_0_Reg"]

validate_bd_design
save_bd_design

# Wrap BD into a top-level HDL module and make it the synthesis top.
make_wrapper -files [get_files "$bd_name.bd"] -top
add_files -norecurse [glob $project_dir/$project_name.gen/sources_1/bd/$bd_name/hdl/${bd_name}_wrapper.v]
set_property top ${bd_name}_wrapper [current_fileset]

# Board I/O constraints (UART pinning, etc.).
add_files -fileset constrs_1 -norecurse \
    [file normalize [file join [file dirname [info script]] constr smartzynq_phase1_io.xdc]]

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
