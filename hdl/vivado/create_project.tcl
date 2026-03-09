if {[llength $argv] >= 1} {
    set project_dir [file normalize [lindex $argv 0]]
} else {
    set project_dir [file normalize [file join [pwd] build vivado]]
}

if {[llength $argv] >= 2} {
    set part_name [lindex $argv 1]
} else {
    set part_name xc7z020clg400-2
}

if {[llength $argv] >= 3} {
    set vendor_hdl_root [file normalize [lindex $argv 2]]
} else {
    set vendor_hdl_root ""
}

set script_dir [file dirname [file normalize [info script]]]
set repo_root [file normalize [file join $script_dir .. ..]]

create_project plane_watcher $project_dir -part $part_name -force
set_property target_language VHDL [current_project]
set_property default_lib xil_defaultlib [current_project]

if {$vendor_hdl_root ne ""} {
    set ip_repo_paths [list \
        [file join $vendor_hdl_root hdl library axi_ad9361] \
        [file join $vendor_hdl_root hdl library axi_dmac] \
        [file join $vendor_hdl_root hdl library util_pack util_cpack2] \
        [file join $vendor_hdl_root hdl library util_pack util_upack2] \
        [file join $vendor_hdl_root hdl library util_cdc] \
        [file join $vendor_hdl_root hdl library util_axis_fifo] \
    ]
    set_property ip_repo_paths $ip_repo_paths [current_project]
    update_ip_catalog
    puts "Configured vendor IP repositories from $vendor_hdl_root"
}

source [file join $script_dir srcs.tcl]
plane_watcher_add_sources $repo_root

set_property top adsb_vendor_wrapper [current_fileset]
update_compile_order -fileset sources_1

puts "Created Vivado project at $project_dir for part $part_name"
puts "Top module: adsb_vendor_wrapper"
