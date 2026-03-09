proc plane_watcher_add_sources {repo_root} {
    set rtl_dir [file join $repo_root hdl rtl]

    set rtl_sources [list \
        adsb_pkg.vhd \
        timestamp_counter.vhd \
        adsb_crc.vhd \
        smallest_bsds.vhd \
        bit_flipper.vhd \
        bsd_calculator.vhd \
        adsb_edge_detector.vhd \
        preamble_detector.vhd \
        message_decoder.vhd \
        message_aggregator.vhd \
        adsb_decoder.vhd \
        async_sample_fifo.vhd \
        async_msg_fifo.vhd \
        iq_to_power.vhd \
        power_downsampler.vhd \
        vendor_rx_ingress.vhd \
        axi_regs.vhd \
        adsb_top.vhd \
        adsb_pl_wrapper.vhd \
        adsb_vendor_wrapper.vhd \
    ]

    foreach src $rtl_sources {
        set path [file join $rtl_dir $src]
        add_files -norecurse $path
        if {$src eq "adsb_pl_wrapper.vhd" || $src eq "adsb_vendor_wrapper.vhd"} {
            set_property file_type {VHDL} [get_files $path]
        } else {
            set_property file_type {VHDL 2008} [get_files $path]
        }
    }
}
