-- =============================================================================
-- adsb_top.vhd — Top-Level ADS-B Receiver (PL Side)
-- =============================================================================
--
-- Wires together the complete ADS-B decode pipeline with AXI register
-- interface for Zynq PS communication.
--
-- Data flow:
--   in_power → adsb_decoder → message_aggregator → async_msg_fifo → axi_regs → AXI
--
-- The message_aggregator serializes decoded messages from 8 parallel decoders
-- into a single stream. The FIFO buffers messages for the AXI register
-- interface, which the Linux PS reads via memory-mapped registers.
--
-- Clock domains (simulation):
--   Everything runs on a single local decode clock for simulation.
--
-- Clock domains (Zynq hardware / integration):
--   - clock: local decode clock (16 MHz in sim, S_AXI_ACLK in the current vendor wrapper)
--   - S_AXI_ACLK: 100-150 MHz (FCLK_CLK0 from Zynq PS)
--   - async_msg_fifo bridges the local decode clock to the AXI clock
--
-- The message_aggregator's packet timeout framing (zero-padding) is filtered
-- out before the FIFO — only real decoded messages are stored.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity adsb_top is
    generic (
        NUM_DECODERS        : positive := 8;
        FIFO_DEPTH          : positive := 64;
        ENABLE_DEEP_DEBUG   : boolean  := false;
        BUILD_ID            : integer  := 0;
        C_S_AXI_DATA_WIDTH  : integer  := 32;
        C_S_AXI_ADDR_WIDTH  : integer  := 8
    );
    port (
        -- =====================================================================
        -- Clocks and Resets
        -- =====================================================================
        clock         : in  std_logic;                                -- Local decode clock
        reset         : in  std_logic;                                -- Active-high reset

        -- =====================================================================
        -- RF Input (I²+Q² power from ADC frontend)
        -- =====================================================================
        in_power      : in  signed(INPUT_POWER_WIDTH-1 downto 0);
        in_valid      : in  std_logic;

        -- =====================================================================
        -- PPS Input (from GPSDO, active-high pulse)
        -- =====================================================================
        pps_in        : in  std_logic;

        -- =====================================================================
        -- Bring-up debug inputs (local decode clock domain)
        -- =====================================================================
        raw_power_max      : in  unsigned(INPUT_POWER_WIDTH-1 downto 0);
        raw_iq_75pct_count : in unsigned(31 downto 0);
        raw_iq_87p5pct_count : in unsigned(31 downto 0);
        raw_iq_near_rail_count : in unsigned(31 downto 0);
        raw_power_sat_count : in unsigned(31 downto 0);
        raw_power_thresh_count : in unsigned(31 downto 0);
        sample_power_max   : in  unsigned(INPUT_POWER_WIDTH-1 downto 0);
        edge_thresh_count  : in  unsigned(31 downto 0);
        power_thresh_count : in  unsigned(31 downto 0);
        sample_fifo_overflow_count : in unsigned(31 downto 0);
        debug_edge_count   : in  unsigned(31 downto 0);
        debug_som_count    : in  unsigned(31 downto 0);
        debug_msg_count    : in  unsigned(31 downto 0);
        debug_edge_shape_count : in unsigned(31 downto 0);
        debug_edge_qual_count  : in unsigned(31 downto 0);
        debug_preamble_pass_count : in unsigned(31 downto 0);
        debug_preamble_detect_count : in unsigned(31 downto 0);
        debug_preamble_abs_count : in unsigned(31 downto 0);
        debug_preamble_quiet_count : in unsigned(31 downto 0);
        debug_preamble_quiet_a_fail_count : in unsigned(31 downto 0);
        debug_preamble_quiet_b_fail_count : in unsigned(31 downto 0);
        debug_preamble_quiet_c_fail_count : in unsigned(31 downto 0);
        debug_preamble_quiet_d_fail_count : in unsigned(31 downto 0);
        debug_preamble_snr_count : in unsigned(31 downto 0);
        debug_preamble_holdoff_count : in unsigned(31 downto 0);
        debug_preamble_peak_age : in unsigned(31 downto 0);
        agg_valid_count    : in  unsigned(31 downto 0);
        fifo_wr_count      : in  unsigned(31 downto 0);
        rx_valid_count     : in  unsigned(31 downto 0);
        sample_valid_count : in  unsigned(31 downto 0);
        rx_clk_count       : in  unsigned(31 downto 0);

        -- =====================================================================
        -- Soft reset toggle (from AXI regs, exposed for rx-domain CDC)
        -- =====================================================================
        soft_reset_toggle_out : out std_logic;

        -- =====================================================================
        -- Interrupt Output
        -- =====================================================================
        irq           : out std_logic;                                -- High when messages available

        -- =====================================================================
        -- AXI4-Lite Slave Interface
        -- =====================================================================
        S_AXI_ACLK    : in  std_logic;
        S_AXI_ARESETN : in  std_logic;

        S_AXI_AWADDR  : in  std_logic_vector(C_S_AXI_ADDR_WIDTH-1 downto 0);
        S_AXI_AWPROT  : in  std_logic_vector(2 downto 0);
        S_AXI_AWVALID : in  std_logic;
        S_AXI_AWREADY : out std_logic;

        S_AXI_WDATA   : in  std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0);
        S_AXI_WSTRB   : in  std_logic_vector(C_S_AXI_DATA_WIDTH/8-1 downto 0);
        S_AXI_WVALID  : in  std_logic;
        S_AXI_WREADY  : out std_logic;

        S_AXI_BRESP   : out std_logic_vector(1 downto 0);
        S_AXI_BVALID  : out std_logic;
        S_AXI_BREADY  : in  std_logic;

        S_AXI_ARADDR  : in  std_logic_vector(C_S_AXI_ADDR_WIDTH-1 downto 0);
        S_AXI_ARPROT  : in  std_logic_vector(2 downto 0);
        S_AXI_ARVALID : in  std_logic;
        S_AXI_ARREADY : out std_logic;

        S_AXI_RDATA   : out std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0);
        S_AXI_RRESP   : out std_logic_vector(1 downto 0);
        S_AXI_RVALID  : out std_logic;
        S_AXI_RREADY  : in  std_logic
    );
end entity;

architecture arch of adsb_top is

    -- FIFO data width: 112 (message) + 64 (TOA) + 24 (RPL) = 200 bits
    constant FIFO_WIDTH : positive := 112 + COUNTER_WIDTH + INPUT_POWER_WIDTH;
    constant AUTO_SNAPSHOT_CYCLES : natural := 10_000_000;  -- 100 ms at 100 MHz

    -- =========================================================================
    -- Timestamp counter signals
    -- =========================================================================
    signal counter_value  : unsigned(COUNTER_WIDTH-1 downto 0);
    signal counter_at_pps : unsigned(COUNTER_WIDTH-1 downto 0);
    signal pps_count      : unsigned(31 downto 0);
    signal pps_new        : std_logic;

    -- =========================================================================
    -- Decoder signals
    -- =========================================================================
    signal init           : std_logic := '0';
    signal debug_rpl      : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal dec_messages   : messages_t(NUM_DECODERS-1 downto 0);
    signal dec_valid      : std_logic_vector(NUM_DECODERS-1 downto 0);
    signal dec_toas       : toas_t(NUM_DECODERS-1 downto 0);
    signal dec_rpls       : rpls_t(NUM_DECODERS-1 downto 0);
    signal debug_edge_count_i : unsigned(31 downto 0);
    signal debug_som_count_i  : unsigned(31 downto 0);
    signal debug_msg_count_i  : unsigned(31 downto 0);
    signal debug_edge_shape_count_i : unsigned(31 downto 0);
    signal debug_edge_qual_count_i  : unsigned(31 downto 0);
    signal debug_preamble_pass_count_i : unsigned(31 downto 0);
    signal debug_preamble_detect_count_i : unsigned(31 downto 0);
    signal debug_preamble_abs_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_a_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_b_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_c_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_quiet_d_fail_count_i : unsigned(31 downto 0);
    signal debug_preamble_snr_count_i : unsigned(31 downto 0);
    signal debug_preamble_holdoff_count_i : unsigned(31 downto 0);
    signal debug_preamble_peak_age_i : unsigned(31 downto 0);
    signal debug_preamble_no_free_count_i : unsigned(31 downto 0);
    signal debug_preamble_busy_drop_count_i : unsigned(31 downto 0);
    signal debug_decoder_busy_max_i : unsigned(31 downto 0);
    signal debug_smallest_done_count_i : unsigned(31 downto 0);
    signal debug_invalid_df_count_i : unsigned(31 downto 0);
    signal debug_crc_attempt_count_i : unsigned(31 downto 0);
    signal debug_crc_pass_count_i : unsigned(31 downto 0);
    signal debug_crc_exhausted_count_i : unsigned(31 downto 0);
    signal debug_df4_count_i : unsigned(31 downto 0);
    signal debug_df5_count_i : unsigned(31 downto 0);
    signal debug_df11_count_i : unsigned(31 downto 0);
    signal debug_cand_df4_count_i : unsigned(31 downto 0);
    signal debug_cand_df5_count_i : unsigned(31 downto 0);
    signal debug_cand_df11_count_i : unsigned(31 downto 0);
    signal debug_df17_count_i : unsigned(31 downto 0);
    signal debug_df18_count_i : unsigned(31 downto 0);
    signal debug_crc0_w0_i : std_logic_vector(31 downto 0);
    signal debug_crc0_w1_i : std_logic_vector(31 downto 0);
    signal debug_crc0_w2_i : std_logic_vector(31 downto 0);
    signal debug_crc0_w3_i : std_logic_vector(31 downto 0);

    -- =========================================================================
    -- Aggregator signals
    -- =========================================================================
    signal agg_message    : std_logic_vector(127 downto 0);
    signal agg_toa        : unsigned(COUNTER_WIDTH-1 downto 0);
    signal agg_rpl        : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal agg_valid      : std_logic;

    -- =========================================================================
    -- FIFO signals
    -- =========================================================================
    signal fifo_wr_data   : std_logic_vector(FIFO_WIDTH-1 downto 0);
    signal fifo_wr_en     : std_logic;
    signal fifo_full      : std_logic;
    signal fifo_rd_data   : std_logic_vector(FIFO_WIDTH-1 downto 0);
    signal fifo_rd_en     : std_logic;
    signal fifo_empty     : std_logic;
    signal fifo_count     : unsigned(6 downto 0);
    signal fifo_overflow  : std_logic;
    signal agg_valid_count_i : unsigned(31 downto 0) := (others => '0');
    signal agg_drop_count_i  : unsigned(31 downto 0) := (others => '0');
    signal fifo_wr_count_i   : unsigned(31 downto 0) := (others => '0');
    signal core_clk_count_i  : unsigned(31 downto 0) := (others => '0');
    signal core_in_valid_count_i : unsigned(31 downto 0) := (others => '0');
    signal core_state_i      : std_logic_vector(31 downto 0) := (others => '0');

    -- =========================================================================
    -- Control signals (from AXI registers, AXI clock domain)
    -- =========================================================================
    signal soft_reset     : std_logic;   -- AXI domain pulse
    signal decoder_enable : std_logic;   -- AXI domain level

    -- CDC: soft_reset pulse → toggle in AXI domain, edge-detect in clock domain
    signal soft_reset_toggle     : std_logic;  -- AXI domain toggle (from axi_regs)
    signal soft_reset_tog_meta   : std_logic := '0';
    signal soft_reset_tog_sync   : std_logic := '0';
    signal soft_reset_tog_prev   : std_logic := '0';
    signal soft_reset_synced     : std_logic := '0';  -- single-cycle pulse in clock domain

    -- CDC: decoder_enable level → 2-FF sync into clock domain
    signal decoder_enable_meta   : std_logic := '0';
    signal decoder_enable_synced : std_logic := '0';
    signal quiet_score_shift_cfg_axi : unsigned(2 downto 0);
    signal snr_ratio_shift_cfg_axi   : unsigned(2 downto 0);
    signal quiet_score_shift_meta    : unsigned(2 downto 0) := to_unsigned(QUIET_SCORE_SHIFT, 3);
    signal quiet_score_shift_synced  : unsigned(2 downto 0) := to_unsigned(QUIET_SCORE_SHIFT, 3);
    signal snr_ratio_shift_meta      : unsigned(2 downto 0) := to_unsigned(SNR_RATIO_SHIFT, 3);
    signal snr_ratio_shift_synced    : unsigned(2 downto 0) := to_unsigned(SNR_RATIO_SHIFT, 3);
    signal holdoff_cfg_axi           : unsigned(11 downto 0);
    signal holdoff_meta              : unsigned(11 downto 0) := to_unsigned(PREAMBLE_HOLDOFF_DEFAULT, 12);
    signal holdoff_synced            : unsigned(11 downto 0) := to_unsigned(PREAMBLE_HOLDOFF_DEFAULT, 12);

    signal combined_reset : std_logic;

    -- CDC: snapshot request toggle → edge-detect in clock domain
    signal debug_snapshot_req : std_logic;
    signal snapshot_req_meta : std_logic := '0';
    signal snapshot_req_sync : std_logic := '0';
    signal snapshot_req_prev : std_logic := '0';
    attribute ASYNC_REG : string;
    attribute ASYNC_REG of soft_reset_tog_meta : signal is "TRUE";
    attribute ASYNC_REG of soft_reset_tog_sync : signal is "TRUE";
    attribute ASYNC_REG of decoder_enable_meta : signal is "TRUE";
    attribute ASYNC_REG of decoder_enable_synced : signal is "TRUE";
    attribute ASYNC_REG of quiet_score_shift_meta : signal is "TRUE";
    attribute ASYNC_REG of quiet_score_shift_synced : signal is "TRUE";
    attribute ASYNC_REG of snr_ratio_shift_meta : signal is "TRUE";
    attribute ASYNC_REG of snr_ratio_shift_synced : signal is "TRUE";
    attribute ASYNC_REG of snapshot_req_meta : signal is "TRUE";
    attribute ASYNC_REG of snapshot_req_sync : signal is "TRUE";
    signal auto_snapshot_count : natural range 0 to AUTO_SNAPSHOT_CYCLES-1 := 0;
    signal raw_power_max_snap      : unsigned(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal raw_iq_75pct_count_snap : unsigned(31 downto 0) := (others => '0');
    signal raw_iq_87p5pct_count_snap : unsigned(31 downto 0) := (others => '0');
    signal raw_iq_near_rail_count_snap : unsigned(31 downto 0) := (others => '0');
    signal raw_power_sat_count_snap : unsigned(31 downto 0) := (others => '0');
    signal raw_power_thresh_count_snap : unsigned(31 downto 0) := (others => '0');
    signal sample_power_max_snap   : unsigned(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal edge_thresh_count_snap  : unsigned(31 downto 0) := (others => '0');
    signal power_thresh_count_snap : unsigned(31 downto 0) := (others => '0');
    signal sample_fifo_overflow_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_edge_count_snap   : unsigned(31 downto 0) := (others => '0');
    signal debug_som_count_snap    : unsigned(31 downto 0) := (others => '0');
    signal debug_msg_count_snap    : unsigned(31 downto 0) := (others => '0');
    signal debug_edge_shape_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_edge_qual_count_snap  : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_pass_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_detect_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_abs_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_quiet_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_quiet_a_fail_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_quiet_b_fail_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_quiet_c_fail_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_quiet_d_fail_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_snr_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_holdoff_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_peak_age_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_no_free_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_preamble_busy_drop_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_decoder_busy_max_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_smallest_done_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_invalid_df_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_crc_attempt_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_crc_pass_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_crc_exhausted_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_df4_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_df5_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_df11_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_cand_df4_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_cand_df5_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_cand_df11_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_df17_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_df18_count_snap : unsigned(31 downto 0) := (others => '0');
    signal debug_crc0_w0_snap : std_logic_vector(31 downto 0) := (others => '0');
    signal debug_crc0_w1_snap : std_logic_vector(31 downto 0) := (others => '0');
    signal debug_crc0_w2_snap : std_logic_vector(31 downto 0) := (others => '0');
    signal debug_crc0_w3_snap : std_logic_vector(31 downto 0) := (others => '0');
    signal agg_valid_count_snap    : unsigned(31 downto 0) := (others => '0');
    signal agg_drop_count_snap     : unsigned(31 downto 0) := (others => '0');
    signal fifo_wr_count_snap      : unsigned(31 downto 0) := (others => '0');
    signal rx_valid_count_snap     : unsigned(31 downto 0) := (others => '0');
    signal sample_valid_count_snap : unsigned(31 downto 0) := (others => '0');
    signal rx_clk_count_snap       : unsigned(31 downto 0) := (others => '0');
    signal core_clk_count_snap     : unsigned(31 downto 0) := (others => '0');
    signal core_in_valid_count_snap : unsigned(31 downto 0) := (others => '0');
    signal core_state_snap         : std_logic_vector(31 downto 0) := (others => '0');

begin

    -- =========================================================================
    -- CDC synchronizers: AXI domain → local decode clock domain
    -- =========================================================================
    cdc_sync : process(clock)
    begin
        if rising_edge(clock) then
            -- soft_reset: toggle-based pulse synchronizer
            -- (AXI-side pulse is shorter than one l_clk cycle, so we use
            --  a toggle in AXI domain and edge-detect here)
            soft_reset_tog_meta <= soft_reset_toggle;
            soft_reset_tog_sync <= soft_reset_tog_meta;
            soft_reset_tog_prev <= soft_reset_tog_sync;
            if soft_reset_tog_sync /= soft_reset_tog_prev then
                soft_reset_synced <= '1';
            else
                soft_reset_synced <= '0';
            end if;

            -- decoder_enable: simple 2-FF level synchronizer
            decoder_enable_meta   <= decoder_enable;
            decoder_enable_synced <= decoder_enable_meta;
            quiet_score_shift_meta   <= quiet_score_shift_cfg_axi;
            quiet_score_shift_synced <= quiet_score_shift_meta;
            snr_ratio_shift_meta     <= snr_ratio_shift_cfg_axi;
            snr_ratio_shift_synced   <= snr_ratio_shift_meta;
            holdoff_meta             <= holdoff_cfg_axi;
            holdoff_synced           <= holdoff_meta;
        end if;
    end process;

    -- Combine external reset with synchronized software reset
    combined_reset <= reset or soft_reset_synced;
    soft_reset_toggle_out <= soft_reset_toggle;

    -- Core state: fully registered, all 32 bits explicitly driven
    core_state_reg : process(clock)
    begin
        if rising_edge(clock) then
            core_state_i <= (others => '0');
            core_state_i(0) <= combined_reset;
            core_state_i(1) <= decoder_enable_synced;
            core_state_i(2) <= in_valid;
        end if;
    end process;

    -- =========================================================================
    -- Timestamp Counter
    -- Free-running 64-bit counter at the local decode clock. Captures the
    -- counter value on PPS rising edge for UTC reconstruction in Linux.
    -- =========================================================================
    U_timestamp : entity work.timestamp_counter
        generic map (WIDTH => COUNTER_WIDTH)
        port map (
            clock          => clock,
            reset          => combined_reset,
            pps_in         => pps_in,
            counter_value  => counter_value,
            counter_at_pps => counter_at_pps,
            pps_count      => pps_count,
            pps_new        => pps_new
        );

    -- =========================================================================
    -- ADS-B Decode Pipeline (8 parallel decoders)
    -- =========================================================================
    U_decoder : entity work.adsb_decoder
        generic map (
            NUM_DECODERS => NUM_DECODERS,
            ENABLE_DEEP_DEBUG => ENABLE_DEEP_DEBUG
        )
        port map (
            clock         => clock,
            reset         => combined_reset,
            init          => init,
            in_power      => in_power,
            in_valid      => in_valid,
            counter_value => counter_value,
            quiet_score_shift_cfg => quiet_score_shift_synced,
            snr_ratio_shift_cfg   => snr_ratio_shift_synced,
            holdoff_cfg           => holdoff_synced,
            debug_rpl     => debug_rpl,
            debug_edge_count => debug_edge_count_i,
            debug_som_count  => debug_som_count_i,
            debug_msg_count  => debug_msg_count_i,
            debug_edge_shape_count => debug_edge_shape_count_i,
            debug_edge_qual_count  => debug_edge_qual_count_i,
            debug_preamble_pass_count => debug_preamble_pass_count_i,
            debug_preamble_detect_count => debug_preamble_detect_count_i,
            debug_preamble_abs_count => debug_preamble_abs_count_i,
            debug_preamble_quiet_count => debug_preamble_quiet_count_i,
            debug_preamble_quiet_a_fail_count => debug_preamble_quiet_a_fail_count_i,
            debug_preamble_quiet_b_fail_count => debug_preamble_quiet_b_fail_count_i,
            debug_preamble_quiet_c_fail_count => debug_preamble_quiet_c_fail_count_i,
            debug_preamble_quiet_d_fail_count => debug_preamble_quiet_d_fail_count_i,
            debug_preamble_snr_count => debug_preamble_snr_count_i,
            debug_preamble_holdoff_count => debug_preamble_holdoff_count_i,
            debug_preamble_peak_age => debug_preamble_peak_age_i,
            debug_preamble_no_free_count => debug_preamble_no_free_count_i,
            debug_preamble_busy_drop_count => debug_preamble_busy_drop_count_i,
            debug_decoder_busy_max => debug_decoder_busy_max_i,
            debug_smallest_done_count => debug_smallest_done_count_i,
            debug_invalid_df_count => debug_invalid_df_count_i,
            debug_crc_attempt_count => debug_crc_attempt_count_i,
            debug_crc_pass_count => debug_crc_pass_count_i,
            debug_crc_exhausted_count => debug_crc_exhausted_count_i,
            debug_df4_count => debug_df4_count_i,
            debug_df5_count => debug_df5_count_i,
            debug_df11_count => debug_df11_count_i,
            debug_cand_df4_count => debug_cand_df4_count_i,
            debug_cand_df5_count => debug_cand_df5_count_i,
            debug_cand_df11_count => debug_cand_df11_count_i,
            debug_df17_count => debug_df17_count_i,
            debug_df18_count => debug_df18_count_i,
            debug_crc0_w0 => debug_crc0_w0_i,
            debug_crc0_w1 => debug_crc0_w1_i,
            debug_crc0_w2 => debug_crc0_w2_i,
            debug_crc0_w3 => debug_crc0_w3_i,
            out_messages  => dec_messages,
            out_valid     => dec_valid,
            out_toas      => dec_toas,
            out_rpls      => dec_rpls
        );

    -- =========================================================================
    -- Message Aggregator
    -- Serializes decoded messages from 8 parallel decoders into a single
    -- stream via holding registers and round-robin scanning.
    -- =========================================================================
    U_aggregator : entity work.message_aggregator
        generic map (
            MSGS_PER_TIMEOUT => 128,
            PACKET_TIMEOUT   => 32000000,       -- Very large: effectively disables timeout padding
            NUM_DECODERS     => NUM_DECODERS
        )
        port map (
            clock       => clock,
            reset       => combined_reset,
            in_messages => dec_messages,
            in_toas     => dec_toas,
            in_rpls     => dec_rpls,
            in_valid    => dec_valid,
            out_message => agg_message,
            out_toa     => agg_toa,
            out_rpl     => agg_rpl,
            out_valid   => agg_valid,
            debug_drop_count => agg_drop_count_i
        );

    -- =========================================================================
    -- FIFO Write: Pack aggregator output and filter out timeout padding
    --
    -- Aggregator tags real messages with 0x0001 in bits [15:0].
    -- Timeout padding has all zeros. We only write real messages to the FIFO.
    --
    -- Packing: [111:0] = message, [175:112] = TOA, [199:176] = RPL
    -- =========================================================================
    fifo_wr_data <= std_logic_vector(agg_rpl) &
                    std_logic_vector(agg_toa) &
                    agg_message(127 downto 16);       -- Strip 16-bit tag, keep 112-bit message

    fifo_wr_en <= agg_valid and agg_message(0) and decoder_enable_synced;  -- Only real messages, only when enabled

    debug_counts : process(clock)
        variable do_snapshot : boolean;
    begin
        if rising_edge(clock) then
            if combined_reset = '1' then
                agg_valid_count_i <= (others => '0');
                fifo_wr_count_i <= (others => '0');
                core_clk_count_i <= (others => '0');
                core_in_valid_count_i <= (others => '0');
                auto_snapshot_count <= 0;
            else
                core_clk_count_i <= core_clk_count_i + 1;
                if agg_valid = '1' then
                    agg_valid_count_i <= agg_valid_count_i + 1;
                end if;
                if fifo_wr_en = '1' then
                    fifo_wr_count_i <= fifo_wr_count_i + 1;
                end if;
                if in_valid = '1' then
                    core_in_valid_count_i <= core_in_valid_count_i + 1;
                end if;

                if auto_snapshot_count = AUTO_SNAPSHOT_CYCLES-1 then
                    auto_snapshot_count <= 0;
                else
                    auto_snapshot_count <= auto_snapshot_count + 1;
                end if;
            end if;

            snapshot_req_meta <= debug_snapshot_req;
            snapshot_req_sync <= snapshot_req_meta;
            do_snapshot := false;

            if snapshot_req_sync /= snapshot_req_prev then
                snapshot_req_prev <= snapshot_req_sync;
                do_snapshot := true;
            elsif auto_snapshot_count = AUTO_SNAPSHOT_CYCLES-1 then
                do_snapshot := true;
            end if;

            if do_snapshot then
                raw_power_max_snap <= raw_power_max;
                raw_iq_75pct_count_snap <= raw_iq_75pct_count;
                raw_iq_87p5pct_count_snap <= raw_iq_87p5pct_count;
                raw_iq_near_rail_count_snap <= raw_iq_near_rail_count;
                raw_power_sat_count_snap <= raw_power_sat_count;
                raw_power_thresh_count_snap <= raw_power_thresh_count;
                sample_power_max_snap <= sample_power_max;
                edge_thresh_count_snap <= edge_thresh_count;
                power_thresh_count_snap <= power_thresh_count;
                sample_fifo_overflow_count_snap <= sample_fifo_overflow_count;
                debug_edge_count_snap <= debug_edge_count_i;
                debug_som_count_snap <= debug_som_count_i;
                debug_msg_count_snap <= debug_msg_count_i;
                debug_edge_shape_count_snap <= debug_edge_shape_count_i;
                debug_edge_qual_count_snap <= debug_edge_qual_count_i;
                debug_preamble_pass_count_snap <= debug_preamble_pass_count_i;
                debug_preamble_detect_count_snap <= debug_preamble_detect_count_i;
                debug_preamble_abs_count_snap <= debug_preamble_abs_count_i;
                debug_preamble_quiet_count_snap <= debug_preamble_quiet_count_i;
                debug_preamble_quiet_a_fail_count_snap <= debug_preamble_quiet_a_fail_count_i;
                debug_preamble_quiet_b_fail_count_snap <= debug_preamble_quiet_b_fail_count_i;
                debug_preamble_quiet_c_fail_count_snap <= debug_preamble_quiet_c_fail_count_i;
                debug_preamble_quiet_d_fail_count_snap <= debug_preamble_quiet_d_fail_count_i;
                debug_preamble_snr_count_snap <= debug_preamble_snr_count_i;
                debug_preamble_holdoff_count_snap <= debug_preamble_holdoff_count_i;
                debug_preamble_peak_age_snap <= debug_preamble_peak_age_i;
                debug_preamble_no_free_count_snap <= debug_preamble_no_free_count_i;
                debug_preamble_busy_drop_count_snap <= debug_preamble_busy_drop_count_i;
                debug_decoder_busy_max_snap <= debug_decoder_busy_max_i;
                debug_smallest_done_count_snap <= debug_smallest_done_count_i;
                debug_invalid_df_count_snap <= debug_invalid_df_count_i;
                debug_crc_attempt_count_snap <= debug_crc_attempt_count_i;
                debug_crc_pass_count_snap <= debug_crc_pass_count_i;
                debug_crc_exhausted_count_snap <= debug_crc_exhausted_count_i;
                debug_df4_count_snap <= debug_df4_count_i;
                debug_df5_count_snap <= debug_df5_count_i;
                debug_df11_count_snap <= debug_df11_count_i;
                debug_cand_df4_count_snap <= debug_cand_df4_count_i;
                debug_cand_df5_count_snap <= debug_cand_df5_count_i;
                debug_cand_df11_count_snap <= debug_cand_df11_count_i;
                debug_df17_count_snap <= debug_df17_count_i;
                debug_df18_count_snap <= debug_df18_count_i;
                debug_crc0_w0_snap <= debug_crc0_w0_i;
                debug_crc0_w1_snap <= debug_crc0_w1_i;
                debug_crc0_w2_snap <= debug_crc0_w2_i;
                debug_crc0_w3_snap <= debug_crc0_w3_i;
                agg_valid_count_snap <= agg_valid_count_i;
                agg_drop_count_snap <= agg_drop_count_i;
                fifo_wr_count_snap <= fifo_wr_count_i;
                rx_valid_count_snap <= rx_valid_count;
                sample_valid_count_snap <= sample_valid_count;
                rx_clk_count_snap <= rx_clk_count;
                core_clk_count_snap <= core_clk_count_i;
                core_in_valid_count_snap <= core_in_valid_count_i;
                core_state_snap <= core_state_i;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Message FIFO
    -- Buffers decoded messages between the decode pipeline and the AXI
    -- register interface across the sample and AXI clock domains.
    -- =========================================================================
    U_fifo : entity work.async_msg_fifo
        generic map (
            WIDTH => FIFO_WIDTH,
            DEPTH => FIFO_DEPTH
        )
        port map (
            reset    => combined_reset,
            wr_clock => clock,
            wr_data  => fifo_wr_data,
            wr_en    => fifo_wr_en,
            full     => fifo_full,
            rd_clock => S_AXI_ACLK,
            rd_data  => fifo_rd_data,
            rd_en    => fifo_rd_en,
            empty    => fifo_empty,
            count    => fifo_count,
            overflow => fifo_overflow
        );

    -- =========================================================================
    -- AXI4-Lite Register Interface
    -- Presents FIFO contents as memory-mapped registers for Linux PS.
    -- Auto-pops FIFO on RPL register read.
    -- =========================================================================
    U_axi_regs : entity work.axi_regs
        generic map (
            ENABLE_DEEP_DEBUG   => ENABLE_DEEP_DEBUG,
            BUILD_ID           => BUILD_ID,
            C_S_AXI_DATA_WIDTH => C_S_AXI_DATA_WIDTH,
            C_S_AXI_ADDR_WIDTH => C_S_AXI_ADDR_WIDTH
        )
        port map (
            -- FIFO read interface
            fifo_rd_data  => fifo_rd_data,
            fifo_empty    => fifo_empty,
            fifo_full     => fifo_full,
            fifo_count    => fifo_count,
            fifo_overflow => fifo_overflow,
            fifo_rd_en    => fifo_rd_en,

            -- PPS / timestamp
            pps_count      => pps_count,
            counter_at_pps => counter_at_pps,

            -- Bring-up debug
            raw_power_max      => raw_power_max_snap,
            raw_iq_75pct_count => raw_iq_75pct_count_snap,
            raw_iq_87p5pct_count => raw_iq_87p5pct_count_snap,
            raw_iq_near_rail_count => raw_iq_near_rail_count_snap,
            raw_power_sat_count => raw_power_sat_count_snap,
            sample_power_max   => sample_power_max_snap,
            raw_power_thresh_count => raw_power_thresh_count_snap,
            edge_thresh_count  => edge_thresh_count_snap,
            power_thresh_count => power_thresh_count_snap,
            sample_fifo_overflow_count => sample_fifo_overflow_count_snap,
            debug_edge_count   => debug_edge_count_snap,
            debug_som_count    => debug_som_count_snap,
            debug_msg_count    => debug_msg_count_snap,
            debug_edge_shape_count => debug_edge_shape_count_snap,
            debug_edge_qual_count  => debug_edge_qual_count_snap,
            debug_preamble_pass_count => debug_preamble_pass_count_snap,
            debug_preamble_detect_count => debug_preamble_detect_count_snap,
            debug_preamble_abs_count => debug_preamble_abs_count_snap,
            debug_preamble_quiet_count => debug_preamble_quiet_count_snap,
            debug_preamble_quiet_a_fail_count => debug_preamble_quiet_a_fail_count_snap,
            debug_preamble_quiet_b_fail_count => debug_preamble_quiet_b_fail_count_snap,
            debug_preamble_quiet_c_fail_count => debug_preamble_quiet_c_fail_count_snap,
            debug_preamble_quiet_d_fail_count => debug_preamble_quiet_d_fail_count_snap,
            debug_preamble_snr_count => debug_preamble_snr_count_snap,
            debug_preamble_holdoff_count => debug_preamble_holdoff_count_snap,
            debug_preamble_peak_age => debug_preamble_peak_age_snap,
            debug_preamble_no_free_count => debug_preamble_no_free_count_snap,
            debug_preamble_busy_drop_count => debug_preamble_busy_drop_count_snap,
            debug_decoder_busy_max => debug_decoder_busy_max_snap,
            debug_smallest_done_count => debug_smallest_done_count_snap,
            debug_invalid_df_count => debug_invalid_df_count_snap,
            debug_crc_attempt_count => debug_crc_attempt_count_snap,
            debug_crc_pass_count => debug_crc_pass_count_snap,
            debug_crc_exhausted_count => debug_crc_exhausted_count_snap,
            debug_df4_count => debug_df4_count_snap,
            debug_df5_count => debug_df5_count_snap,
            debug_df11_count => debug_df11_count_snap,
            debug_cand_df4_count => debug_cand_df4_count_snap,
            debug_cand_df5_count => debug_cand_df5_count_snap,
            debug_cand_df11_count => debug_cand_df11_count_snap,
            debug_df17_count => debug_df17_count_snap,
            debug_df18_count => debug_df18_count_snap,
            debug_crc0_w0 => unsigned(debug_crc0_w0_snap),
            debug_crc0_w1 => unsigned(debug_crc0_w1_snap),
            debug_crc0_w2 => unsigned(debug_crc0_w2_snap),
            debug_crc0_w3 => unsigned(debug_crc0_w3_snap),
            agg_valid_count    => agg_valid_count_snap,
            agg_drop_count     => agg_drop_count_snap,
            fifo_wr_count      => fifo_wr_count_snap,
            rx_valid_count     => rx_valid_count_snap,
            sample_valid_count => sample_valid_count_snap,
            core_clk_count     => core_clk_count_snap,
            core_in_valid_count => core_in_valid_count_snap,
            core_state         => core_state_snap,
            rx_clk_count       => rx_clk_count_snap,
            debug_snapshot_req => debug_snapshot_req,
            quiet_score_shift_cfg => quiet_score_shift_cfg_axi,
            snr_ratio_shift_cfg   => snr_ratio_shift_cfg_axi,
            holdoff_cfg           => holdoff_cfg_axi,

            -- Control
            soft_reset     => soft_reset,
            soft_reset_toggle => soft_reset_toggle,
            decoder_enable => decoder_enable,

            -- Interrupt
            irq            => irq,

            -- AXI4-Lite
            S_AXI_ACLK    => S_AXI_ACLK,
            S_AXI_ARESETN => S_AXI_ARESETN,
            S_AXI_AWADDR  => S_AXI_AWADDR,
            S_AXI_AWPROT  => S_AXI_AWPROT,
            S_AXI_AWVALID => S_AXI_AWVALID,
            S_AXI_AWREADY => S_AXI_AWREADY,
            S_AXI_WDATA   => S_AXI_WDATA,
            S_AXI_WSTRB   => S_AXI_WSTRB,
            S_AXI_WVALID  => S_AXI_WVALID,
            S_AXI_WREADY  => S_AXI_WREADY,
            S_AXI_BRESP   => S_AXI_BRESP,
            S_AXI_BVALID  => S_AXI_BVALID,
            S_AXI_BREADY  => S_AXI_BREADY,
            S_AXI_ARADDR  => S_AXI_ARADDR,
            S_AXI_ARPROT  => S_AXI_ARPROT,
            S_AXI_ARVALID => S_AXI_ARVALID,
            S_AXI_ARREADY => S_AXI_ARREADY,
            S_AXI_RDATA   => S_AXI_RDATA,
            S_AXI_RRESP   => S_AXI_RRESP,
            S_AXI_RVALID  => S_AXI_RVALID,
            S_AXI_RREADY  => S_AXI_RREADY
        );

end architecture;
