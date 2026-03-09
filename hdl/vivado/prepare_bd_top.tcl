if {[llength $argv] < 2} {
    puts stderr "usage: vivado -mode batch -source prepare_bd_top.tcl -tclargs <project.xpr> <bd_name>"
    exit 1
}

set project_file [file normalize [lindex $argv 0]]
set bd_name [lindex $argv 1]

open_project $project_file

set bd_file [get_files -quiet */bd/$bd_name/$bd_name.bd]
if {[llength $bd_file] == 0} {
    puts stderr "block design $bd_name not found in project"
    exit 1
}

set bd_file [lindex $bd_file 0]
open_bd_design $bd_file
generate_target all $bd_file

set wrapper_files [make_wrapper -files [list $bd_file] -top]
add_files -norecurse $wrapper_files

set wrapper_top ${bd_name}_wrapper
set_property top $wrapper_top [current_fileset]
update_compile_order -fileset sources_1
save_bd_design

puts "Prepared BD wrapper top $wrapper_top for $project_file"
close_project
