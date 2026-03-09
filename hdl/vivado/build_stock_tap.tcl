# =============================================================================
# build_stock_tap.tcl -- Single-session full build using stock Pluto BD + tap
# =============================================================================
# Usage: vivado -mode batch -source build_stock_tap.tcl \
#          -tclargs <vendor_hdl_root> [base_addr] [jobs] [build_id] [deep_debug]
# =============================================================================

if {[llength $argv] < 1} {
    puts stderr "usage: vivado -mode batch -source build_stock_tap.tcl -tclargs <vendor_hdl_root> ?base_addr? ?jobs? ?build_id? ?deep_debug?"
    exit 1
}

set vendor_hdl_root [file normalize [lindex $argv 0]]
set base_addr 0x43D00000
set jobs 16
set build_id 0
set deep_debug false
if {[llength $argv] >= 2} { set base_addr [lindex $argv 1] }
if {[llength $argv] >= 3} { set jobs [lindex $argv 2] }
if {[llength $argv] >= 4} { set build_id [lindex $argv 3] }
if {[llength $argv] >= 5} {
    if {[lindex $argv 4] in {1 true TRUE yes YES on ON}} {
        set deep_debug true
    }
}
set_param general.maxThreads $jobs

set script_dir [file dirname [file normalize [info script]]]
set repo_root [file normalize [file join $script_dir .. ..]]
set project_dir [file join $script_dir build plane_watcher]
set part_name xc7z020clg400-2
set ad_hdl_dir [file join $vendor_hdl_root hdl]
set pluto_dir [file join $ad_hdl_dir projects pluto]
set post_impl_overrides [file join $script_dir constr plane_watcher_post_impl_overrides.tcl]
source [file join $script_dir timing_gate.tcl]

set sys_zynq 1
set IGNORE_VERSION_CHECK 1

proc pw_connect_with_fanout {src_pin dst_pin} {
    if {![catch {ad_connect $src_pin $dst_pin} result]} {
        return
    }

    set src_obj [get_bd_pins -quiet $src_pin]
    if {[llength $src_obj] == 0} {
        set src_obj [get_bd_ports -quiet $src_pin]
    }
    if {[llength $src_obj] == 0} {
        error "unable to resolve source object for '$src_pin': $result"
    }

    set dst_obj [get_bd_pins -quiet $dst_pin]
    if {[llength $dst_obj] == 0} {
        set dst_obj [get_bd_ports -quiet $dst_pin]
    }
    if {[llength $dst_obj] == 0} {
        error "unable to resolve destination object for '$dst_pin': $result"
    }

    set src_nets [get_bd_nets -quiet -of_objects $src_obj]
    if {[llength $src_nets] == 0} {
        error "ad_connect failed and source '$src_pin' has no existing net: $result"
    }

    connect_bd_net [lindex $src_nets 0] $dst_obj
}

proc pw_connect_if_needed {src_pin dst_pin} {
    set dst_obj [get_bd_pins -quiet $dst_pin]
    if {[llength $dst_obj] == 0} {
        return
    }
    set existing [get_bd_nets -quiet -of_objects $dst_obj]
    if {[llength $existing] == 0} {
        connect_bd_net [get_bd_pins $src_pin] $dst_obj
    }
}

puts "=== Step 1/4: Creating project ==="
file delete -force $project_dir
create_project plane_watcher $project_dir -part $part_name -force
set_property target_language Verilog [current_project]
set_property default_lib xil_defaultlib [current_project]
set_property ip_repo_paths [list [file join $ad_hdl_dir library]] [current_project]
update_ip_catalog

source [file join $script_dir srcs.tcl]
plane_watcher_add_sources $repo_root

add_files -norecurse [file join $script_dir system_top.v]
# The stock Pluto constraints define rx_clk at 4.000 ns. For this project the
# same AD9361 DATA_CLK is only ~61.44 MHz, and we sign off with a conservative
# 8.000 ns target instead of the vendor's much harsher overconstraint.
set local_system_constr [file join $project_dir system_constr_pw.xdc]
set in_f [open [file join $pluto_dir system_constr.xdc] r]
set out_f [open $local_system_constr w]
while {[gets $in_f line] >= 0} {
    if {[string match "create_clock -period 4.000 -name rx_clk *" $line]} {
        puts $out_f {create_clock -period 8.000 -name rx_clk [get_ports rx_clk_in_p]}
    } else {
        puts $out_f $line
    }
}
close $in_f
close $out_f
add_files -norecurse $local_system_constr
add_files -fileset constrs_1 -norecurse [file join $script_dir constr plane_watcher_stock_tap_io.xdc]
add_files -norecurse [file join $ad_hdl_dir library common ad_iobuf.v]

puts "=== Step 2/4: Creating stock Pluto block design ==="
source [file join $vendor_hdl_root hdl projects scripts adi_board.tcl]
source [file join $vendor_hdl_root hdl projects common xilinx adi_fir_filter_bd.tcl]

create_bd_design system
current_bd_design system
source [file join $pluto_dir system_bd.tcl]

create_bd_cell -type module -reference adsb_vendor_wrapper adsb_vendor_wrapper_0
set_property -dict [list CONFIG.BUILD_ID $build_id CONFIG.ENABLE_DEEP_DEBUG $deep_debug] [get_bd_cells adsb_vendor_wrapper_0]
create_bd_cell -type ip -vlnv xilinx.com:ip:proc_sys_reset proc_sys_reset_rx

ad_connect axi_ad9361/l_clk proc_sys_reset_rx/slowest_sync_clk
ad_connect sys_rstgen/peripheral_aresetn proc_sys_reset_rx/ext_reset_in
ad_connect axi_ad9361/l_clk adsb_vendor_wrapper_0/rx_clk
connect_bd_net [get_bd_pins proc_sys_reset_rx/peripheral_reset] [get_bd_pins adsb_vendor_wrapper_0/rx_reset]

pw_connect_with_fanout axi_ad9361/adc_data_i0 adsb_vendor_wrapper_0/rx_i
pw_connect_with_fanout axi_ad9361/adc_data_q0 adsb_vendor_wrapper_0/rx_q
pw_connect_with_fanout axi_ad9361/adc_valid_i0 adsb_vendor_wrapper_0/rx_valid

# PPS input from GPS module — directly connected to the decoder wrapper and
# also routed through EMIO GPIO[17] (Linux GPIO 71) for the pps-gpio driver.
create_bd_port -dir I pps_in
ad_connect pps_in adsb_vendor_wrapper_0/pps_in

ad_cpu_interconnect $base_addr adsb_vendor_wrapper_0
pw_connect_if_needed sys_cpu_clk adsb_vendor_wrapper_0/S_AXI_ACLK
pw_connect_if_needed sys_cpu_resetn adsb_vendor_wrapper_0/S_AXI_ARESETN
ad_cpu_interrupt ps-10 mb-10 adsb_vendor_wrapper_0/irq

# GPS F9P UART via PS UART0 EMIO. The stock Pluto BD only enables UART1
# (console on MIO 8..9); add UART0 on EMIO and route TX/RX to board pins.
set_property -dict [list \
    CONFIG.PCW_UART0_PERIPHERAL_ENABLE {1} \
    CONFIG.PCW_UART0_UART0_IO {EMIO} \
] [get_bd_cells sys_ps7]

create_bd_port -dir O uart0_tx
create_bd_port -dir I uart0_rx
ad_connect sys_ps7/UART0_TX uart0_tx
ad_connect uart0_rx sys_ps7/UART0_RX

validate_bd_design
save_bd_design

puts "=== Step 3/4: Generating BD wrapper ==="
set bd_file [lindex [get_files -quiet */bd/system/system.bd] 0]
generate_target all $bd_file

# The generated AXI SPI IP XDC requests IOB packing for generic internal
# registers such as *IO*_I_REG. In this design the relevant register is not
# directly attached to an I/O cell, so Vivado emits a CRITICAL WARNING and
# ignores the constraint. Keep the IP's CDC waivers but drop only those stale
# IOB packing directives.
set spi_xdc [lindex [get_files -quiet */bd/system/ip/system_axi_spi_0/system_axi_spi_0.xdc] 0]
if {$spi_xdc ne ""} {
    set spi_xdc_patch [file join $project_dir system_axi_spi_0_pw.xdc]
    set in_f [open $spi_xdc r]
    set out_f [open $spi_xdc_patch w]
    while {[gets $in_f line] >= 0} {
        if {[string match {set_property IOB true *} $line]} {
            continue
        }
        puts $out_f $line
    }
    close $in_f
    close $out_f
    set_property is_enabled false [get_files $spi_xdc]
    add_files -fileset constrs_1 -norecurse $spi_xdc_patch
}

set wrapper_files [make_wrapper -files [list $bd_file] -top]
add_files -norecurse $wrapper_files
set_property is_enabled false [get_files -quiet *system_sys_ps7_0.xdc]
set_property top system_top [current_fileset]
update_compile_order -fileset sources_1

# Apply the debug CDC false-path overrides during implementation so routed
# timing and the end-of-build diagnostic report use the same exception set.
set_property STEPS.OPT_DESIGN.TCL.PRE $post_impl_overrides [get_runs impl_1]

puts "=== Step 4/4: Running synthesis + implementation (jobs=$jobs) ==="
launch_runs impl_1 -to_step route_design -jobs $jobs
wait_on_run impl_1

plane_watcher_require_timing_clean $project_dir [file join $project_dir impl_timing_summary_post_override.rpt]
source $post_impl_overrides
source [file join $ad_hdl_dir library axi_ad9361 axi_ad9361_delay.tcl]

puts "=== Timing met. Generating bitstream ==="
launch_runs impl_1 -to_step write_bitstream -jobs $jobs
wait_on_run impl_1

puts "=========================================="
puts "Build complete."
puts "Bitstream: [file join $project_dir plane_watcher.runs impl_1 system_top.bit]"
puts "=========================================="
close_project
