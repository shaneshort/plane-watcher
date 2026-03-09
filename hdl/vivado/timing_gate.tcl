proc plane_watcher_require_timing_clean {project_dir timing_report_path} {
    open_run impl_1
    report_utilization -file [file join $project_dir impl_utilization.rpt]
    report_timing_summary -file $timing_report_path

    set impl_run [get_runs impl_1]
    set wns [get_property STATS.WNS $impl_run]
    set tns [get_property STATS.TNS $impl_run]

    if {$wns eq "" || $tns eq ""} {
        puts stderr "ERROR: failed to read implementation timing stats from impl_1"
        close_project
        exit 2
    }

    puts [format "Timing check after route: WNS=%s ns, TNS=%s ns" $wns $tns]

    if {$wns < 0.0 || $tns < 0.0} {
        puts stderr "ERROR: timing constraints are not met after route. Aborting before bitstream generation."
        close_project
        exit 2
    }
}
