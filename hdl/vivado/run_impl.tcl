if {[llength $argv] < 1} {
    puts stderr "usage: vivado -mode batch -source run_impl.tcl -tclargs <project.xpr>"
    exit 1
}

set project_file [file normalize [lindex $argv 0]]
set script_dir [file dirname [file normalize [info script]]]
open_project $project_file
set_param general.maxThreads 16
source [file join $script_dir timing_gate.tcl]

update_compile_order -fileset sources_1
reset_run synth_1
reset_run impl_1
launch_runs impl_1 -to_step route_design -jobs 16
wait_on_run impl_1

set project_dir [get_property directory [current_project]]
plane_watcher_require_timing_clean $project_dir [file join $project_dir impl_timing_summary.rpt]

launch_runs impl_1 -to_step write_bitstream -jobs 16
wait_on_run impl_1

puts "Implementation completed for $project_file"
close_project
