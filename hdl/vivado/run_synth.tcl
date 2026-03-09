if {[llength $argv] < 1} {
    puts stderr "usage: vivado -mode batch -source run_synth.tcl -tclargs <project.xpr>"
    exit 1
}

set project_file [file normalize [lindex $argv 0]]
open_project $project_file
set_param general.maxThreads 16

update_compile_order -fileset sources_1
reset_run synth_1
launch_runs synth_1 -jobs 16
wait_on_run synth_1

open_run synth_1
set project_dir [get_property directory [current_project]]
report_utilization -file [file join $project_dir synth_utilization.rpt]
report_timing_summary -file [file join $project_dir synth_timing_summary.rpt]

puts "Synthesis completed for $project_file"
close_project
