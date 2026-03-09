# Post-implementation timing overrides for the Pluto tap build.
#
# These are applied after open_run impl_1 so they see the routed netlist
# hierarchy directly. Clock creation/overrides must happen before routing; the
# only post-impl work here is attaching false paths to debug/status CDCs that
# are awkward to match reliably from project XDC files.
#
# Important: for the stock-tap flow, this file is part of the signoff timing
# path, not just a convenience copy of the XDC rules. If you add a new
# rx-domain debug counter that is snapshotted into S_AXI_ACLK, update this
# file as well as the front-end XDC or the post-override timing report will
# still time that CDC.

proc pw_false_path_if_present {from_filter to_filter} {
    set from_cells [get_cells -quiet -hierarchical -filter $from_filter]
    set to_cells   [get_cells -quiet -hierarchical -filter $to_filter]
    if {[llength $from_cells] > 0 && [llength $to_cells] > 0} {
        set_false_path -from $from_cells -to $to_cells
    }
}

pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */U_sample_fifo/wr_ptr_gray_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */U_sample_fifo/wr_ptr_gray_sync1_r_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */U_sample_fifo/rd_ptr_gray_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */U_sample_fifo/rd_ptr_gray_sync1_w_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */sample_power_max_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */sample_power_max_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */raw_power_max_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */raw_power_max_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */raw_iq_75pct_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */raw_iq_75pct_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */raw_iq_87p5pct_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */raw_iq_87p5pct_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */raw_iq_near_rail_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */raw_iq_near_rail_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */raw_power_sat_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */raw_power_sat_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */raw_power_thr_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */raw_power_thresh_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */edge_thresh_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */edge_thresh_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */power_thresh_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */power_thresh_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */sample_fifo_overflow_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */sample_fifo_overflow_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */rx_valid_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */rx_valid_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */sample_valid_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */sample_valid_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && NAME =~ */rx_clk_count_reg*} \
                         {IS_SEQUENTIAL && NAME =~ */rx_clk_count_snap_reg*}
pw_false_path_if_present {IS_SEQUENTIAL && (NAME =~ */soft_reset_toggle_reg* || NAME =~ */soft_reset_toggle_i_reg*)} \
                         {IS_SEQUENTIAL && NAME =~ */rx_soft_tog_meta_reg*}
