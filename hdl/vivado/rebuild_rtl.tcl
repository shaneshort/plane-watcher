# =============================================================================
# rebuild_rtl.tcl — Fast RTL-only re-synthesis and implementation
# =============================================================================
# Usage: vivado -mode batch -source rebuild_rtl.tcl -tclargs [jobs]
#
# Opens the existing project (from a prior full build), re-reads updated VHDL
# sources, and re-runs synthesis + implementation + bitstream. Skips BD
# creation, IP catalog update, and wrapper generation — saving minutes per
# iteration when only RTL files changed.
#
# Prerequisite: a prior full build must exist in build/plane_watcher/.
# =============================================================================

set jobs 16
if {[llength $argv] >= 1} { set jobs [lindex $argv 0] }
set_param general.maxThreads $jobs

set script_dir [file dirname [file normalize [info script]]]
set repo_root [file normalize [file join $script_dir .. ..]]
set project_dir [file join $script_dir build plane_watcher]
set project_file [file join $project_dir plane_watcher.xpr]
source [file join $script_dir timing_gate.tcl]

if {![file exists $project_file]} {
    puts stderr "error: no existing project at $project_file"
    puts stderr "       run a full build first (make vendor-build)"
    exit 1
}

puts "=== Opening existing project ==="
open_project $project_file

# Re-read RTL sources so Vivado picks up any edits
puts "=== Refreshing RTL sources ==="
source [file join $script_dir srcs.tcl]
# Force Vivado to re-check source file timestamps
update_compile_order -fileset sources_1

puts "=== Running synthesis + implementation (jobs=$jobs) ==="
reset_run synth_1
reset_run impl_1
launch_runs impl_1 -to_step route_design -jobs $jobs
wait_on_run impl_1

plane_watcher_require_timing_clean $project_dir [file join $project_dir impl_timing_summary.rpt]

puts "=== Timing met. Generating bitstream ==="
launch_runs impl_1 -to_step write_bitstream -jobs $jobs
wait_on_run impl_1

puts "=========================================="
puts "RTL rebuild complete."
puts "=========================================="
close_project
