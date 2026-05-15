-- =============================================================================
-- adsb_vendor_wrapper.vhd -- Board-facing wrapper for vendor AD936x RX ingress
-- =============================================================================
--
-- Adapts the vendor board's natural RX I/Q stream into the existing decoder
-- pipeline by inserting the vendor_rx_ingress block ahead of adsb_top.
--
-- Clock domains:
--   rx_clk      — AD936x LVDS DATA_CLK (~61.44 MHz from axi_ad9361/l_clk).
--                  In LVDS DDR 2R2T mode, DATA_CLK = ADC sample rate × 2.
--                  The XDC signoff target is 8 ns (125 MHz) for margin.
--                  Drives vendor_rx_ingress (IQ→power, downsampling) and the
--                  front-end debug counters.
--   S_AXI_ACLK  — PS FCLK_CLK0 (100 MHz). Drives the entire decode core
--                  (adsb_top) and AXI register interface.
--
-- A dedicated dual-clock sample FIFO transfers sample_power/sample_valid from
-- rx_clk into S_AXI_ACLK. This preserves ordering even when the fractional
-- downsampler emits back-to-back 30.72 MHz input beats.
--
-- The front-end debug counters (sample_power_max, threshold counts, etc.)
-- remain on rx_clk. They are captured by adsb_top's snapshot mechanism,
-- which already tolerates the informal CDC (values are stable between
-- software-triggered snapshots).
--
-- Note: timestamp_counter inside adsb_top now counts at 100 MHz, not the
-- sample rate. PS-side code must use 100 MHz for TOA/PPS time conversion.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity adsb_vendor_wrapper is
    generic (
        NUM_DECODERS        : positive := 8;
        FIFO_DEPTH          : positive := 64;
        ENABLE_DEEP_DEBUG   : boolean  := false;
        BUILD_ID            : integer  := 0;
        C_S_AXI_DATA_WIDTH  : integer  := 32;
        C_S_AXI_ADDR_WIDTH  : integer  := 8
    );
    port (
        -- Vendor RX-domain interface
        rx_clk       : in  std_logic;
        rx_reset     : in  std_logic;
        rx_i         : in  std_logic_vector(15 downto 0);
        rx_q         : in  std_logic_vector(15 downto 0);
        rx_valid     : in  std_logic;
        pps_in       : in  std_logic;
        irq          : out std_logic;

        -- AXI4-Lite slave interface
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

architecture rtl of adsb_vendor_wrapper is
    constant ADC_WIDTH : positive := 12;
    constant IQ_75PCT_THRESHOLD : integer := 3 * (2 ** (ADC_WIDTH - 3));
    constant IQ_87P5PCT_THRESHOLD : integer := 7 * (2 ** (ADC_WIDTH - 4));
    constant IQ_NEAR_RAIL_THRESHOLD : integer := (2 ** (ADC_WIDTH - 1)) - 8;
    constant RAW_POWER_NEAR_RAIL_THRESHOLD : integer :=
        (2 ** ((ADC_WIDTH * 2) - 3)) - (2 ** ((ADC_WIDTH * 2) - 8));
    constant IQ_75PCT_POS : signed(15 downto 0) := to_signed(IQ_75PCT_THRESHOLD, 16);
    constant IQ_75PCT_NEG : signed(15 downto 0) := to_signed(-IQ_75PCT_THRESHOLD, 16);
    constant IQ_87P5PCT_POS : signed(15 downto 0) := to_signed(IQ_87P5PCT_THRESHOLD, 16);
    constant IQ_87P5PCT_NEG : signed(15 downto 0) := to_signed(-IQ_87P5PCT_THRESHOLD, 16);
    constant IQ_NEAR_RAIL_POS : signed(15 downto 0) := to_signed(IQ_NEAR_RAIL_THRESHOLD, 16);
    constant IQ_NEAR_RAIL_NEG : signed(15 downto 0) := to_signed(-IQ_NEAR_RAIL_THRESHOLD, 16);

    -- RX-domain signals (rx_clk)
    signal rx_i_signed  : signed(15 downto 0);
    signal rx_q_signed  : signed(15 downto 0);
    signal raw_power    : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal raw_valid    : std_logic;
    signal sample_power : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal sample_valid : std_logic;
    signal sample_power_stage : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal sample_valid_stage : std_logic := '0';

    -- Front-end debug counters (rx_clk domain)
    signal raw_power_max      : unsigned(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal raw_iq_75pct_count : unsigned(31 downto 0) := (others => '0');
    signal raw_iq_87p5pct_count : unsigned(31 downto 0) := (others => '0');
    signal raw_iq_near_rail_count : unsigned(31 downto 0) := (others => '0');
    signal raw_power_sat_count : unsigned(31 downto 0) := (others => '0');
    signal raw_power_thr_count : unsigned(31 downto 0) := (others => '0');
    signal sample_power_max   : unsigned(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal edge_thresh_count  : unsigned(31 downto 0) := (others => '0');
    signal power_thresh_count : unsigned(31 downto 0) := (others => '0');
    signal sample_fifo_overflow_count : unsigned(31 downto 0) := (others => '0');
    signal rx_valid_count     : unsigned(31 downto 0) := (others => '0');
    signal sample_valid_count : unsigned(31 downto 0) := (others => '0');
    signal rx_clk_count       : unsigned(31 downto 0) := (others => '0');

    -- Debug outputs from adsb_top (core_clk domain)
    signal debug_edge_count   : unsigned(31 downto 0);
    signal debug_som_count    : unsigned(31 downto 0);
    signal debug_msg_count    : unsigned(31 downto 0);
    signal debug_edge_shape_count : unsigned(31 downto 0);
    signal debug_edge_qual_count  : unsigned(31 downto 0);
    signal debug_preamble_pass_count : unsigned(31 downto 0);
    signal debug_preamble_detect_count : unsigned(31 downto 0);
    signal debug_preamble_abs_count : unsigned(31 downto 0);
    signal debug_preamble_quiet_count : unsigned(31 downto 0);
    signal debug_preamble_quiet_a_fail_count : unsigned(31 downto 0);
    signal debug_preamble_quiet_b_fail_count : unsigned(31 downto 0);
    signal debug_preamble_quiet_c_fail_count : unsigned(31 downto 0);
    signal debug_preamble_quiet_d_fail_count : unsigned(31 downto 0);
    signal debug_preamble_snr_count : unsigned(31 downto 0);
    signal debug_preamble_holdoff_count : unsigned(31 downto 0);
    signal debug_preamble_peak_age : unsigned(31 downto 0);
    signal agg_valid_count    : unsigned(31 downto 0);
    signal fifo_wr_count      : unsigned(31 downto 0);

    -- Sample FIFO: rx_clk → S_AXI_ACLK
    constant SAMPLE_FIFO_DEPTH : positive := 64;
    signal sample_fifo_rd_data : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal sample_fifo_rd_en   : std_logic := '0';
    signal sample_fifo_full    : std_logic := '0';
    signal sample_fifo_empty   : std_logic := '1';
    signal sample_fifo_overflow : std_logic := '0';
    signal sample_fifo_rd_reset : std_logic := '1';
    signal core_sample_power : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal core_sample_valid : std_logic := '0';

    -- Core reset (S_AXI_ACLK domain, active-high)
    signal core_reset : std_logic;

    -- Soft reset toggle from adsb_top/axi_regs (S_AXI_ACLK domain)
    signal soft_reset_toggle : std_logic;

    -- RX-domain soft reset: CDC the toggle into rx_clk and edge-detect
    signal rx_soft_tog_meta : std_logic := '0';
    signal rx_soft_tog_sync : std_logic := '0';
    signal rx_soft_tog_prev : std_logic := '0';
    signal rx_soft_reset    : std_logic := '0';
    signal soft_reset_toggle_prev_axi : std_logic := '0';
    attribute ASYNC_REG : string;
    attribute ASYNC_REG of rx_soft_tog_meta : signal is "TRUE";
    attribute ASYNC_REG of rx_soft_tog_sync : signal is "TRUE";

    -- Combined rx-domain reset: power-on OR software-triggered
    signal rx_reset_combined : std_logic;
begin

    core_reset <= not S_AXI_ARESETN;
    rx_reset_combined <= rx_reset or rx_soft_reset;

    rx_i_signed <= signed(rx_i);
    rx_q_signed <= signed(rx_q);

    -- =========================================================================
    -- RX-domain soft reset: CDC the AXI-domain toggle into rx_clk.
    -- This lets software reset the rx frontend (ingress, CDC, debug counters)
    -- without a power cycle.
    -- =========================================================================
    rx_soft_reset_cdc : process(rx_clk)
    begin
        if rising_edge(rx_clk) then
            rx_soft_tog_meta <= soft_reset_toggle;
            rx_soft_tog_sync <= rx_soft_tog_meta;
            rx_soft_tog_prev <= rx_soft_tog_sync;
            if rx_soft_tog_sync /= rx_soft_tog_prev then
                rx_soft_reset <= '1';
            else
                rx_soft_reset <= '0';
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Front-end debug counters (rx_clk domain)
    -- These remain on rx_clk because they measure RX-side events.
    -- adsb_top's snapshot mechanism captures them for AXI readout.
    -- =========================================================================
    debug_counters : process(rx_clk)
        variable power_unsigned     : unsigned(INPUT_POWER_WIDTH-1 downto 0);
        variable raw_power_unsigned : unsigned(INPUT_POWER_WIDTH-1 downto 0);
    begin
        if rising_edge(rx_clk) then
            if rx_reset_combined = '1' then
                raw_power_max <= (others => '0');
                raw_power_thr_count <= (others => '0');
                raw_iq_75pct_count <= (others => '0');
                raw_iq_87p5pct_count <= (others => '0');
                raw_iq_near_rail_count <= (others => '0');
                raw_power_sat_count <= (others => '0');
                sample_power_max <= (others => '0');
                edge_thresh_count <= (others => '0');
                power_thresh_count <= (others => '0');
                sample_fifo_overflow_count <= (others => '0');
                if ENABLE_DEEP_DEBUG then
                    rx_valid_count <= (others => '0');
                    sample_valid_count <= (others => '0');
                    rx_clk_count <= (others => '0');
                end if;
            else
                if ENABLE_DEEP_DEBUG then
                    rx_clk_count <= rx_clk_count + 1;
                    if rx_valid = '1' then
                        rx_valid_count <= rx_valid_count + 1;
                    end if;
                end if;

                if rx_valid = '1' then
                    if (rx_i_signed >= IQ_75PCT_POS) or
                       (rx_i_signed <= IQ_75PCT_NEG) or
                       (rx_q_signed >= IQ_75PCT_POS) or
                       (rx_q_signed <= IQ_75PCT_NEG) then
                        raw_iq_75pct_count <= raw_iq_75pct_count + 1;
                    end if;
                    if (rx_i_signed >= IQ_87P5PCT_POS) or
                       (rx_i_signed <= IQ_87P5PCT_NEG) or
                       (rx_q_signed >= IQ_87P5PCT_POS) or
                       (rx_q_signed <= IQ_87P5PCT_NEG) then
                        raw_iq_87p5pct_count <= raw_iq_87p5pct_count + 1;
                    end if;
                    if (rx_i_signed >= IQ_NEAR_RAIL_POS) or
                       (rx_i_signed <= IQ_NEAR_RAIL_NEG) or
                       (rx_q_signed >= IQ_NEAR_RAIL_POS) or
                       (rx_q_signed <= IQ_NEAR_RAIL_NEG) then
                        raw_iq_near_rail_count <= raw_iq_near_rail_count + 1;
                    end if;
                end if;

                if raw_valid = '1' then
                    raw_power_unsigned := unsigned(raw_power);

                    if raw_power_unsigned > raw_power_max then
                        raw_power_max <= raw_power_unsigned;
                    end if;

                    if raw_power > POWER_THRESHOLD then
                        raw_power_thr_count <= raw_power_thr_count + 1;
                    end if;

                    if raw_power_unsigned >= to_unsigned(RAW_POWER_NEAR_RAIL_THRESHOLD, INPUT_POWER_WIDTH) then
                        raw_power_sat_count <= raw_power_sat_count + 1;
                    end if;
                end if;

                if sample_valid_stage = '1' then
                    if ENABLE_DEEP_DEBUG then
                        sample_valid_count <= sample_valid_count + 1;
                    end if;
                    power_unsigned := unsigned(sample_power_stage);

                    if power_unsigned > sample_power_max then
                        sample_power_max <= power_unsigned;
                    end if;

                    if sample_power_stage > EDGE_POWER_THRESHOLD then
                        edge_thresh_count <= edge_thresh_count + 1;
                    end if;

                    if sample_power_stage > POWER_THRESHOLD then
                        power_thresh_count <= power_thresh_count + 1;
                    end if;
                end if;

                if sample_valid_stage = '1' and sample_fifo_full = '1' then
                    sample_fifo_overflow_count <= sample_fifo_overflow_count + 1;
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Vendor RX ingress (rx_clk domain)
    -- IQ→power conversion and downsampling to SAMPLE_RATE_HZ.
    -- =========================================================================
    U_ingress : entity work.vendor_rx_ingress
        generic map (
            IQ_WIDTH       => 16,
            INPUT_RATE_HZ  => 30_720_000,
            OUTPUT_RATE_HZ => SAMPLE_RATE_HZ
        )
        port map (
            clock        => rx_clk,
            reset        => rx_reset_combined,
            rx_i         => rx_i_signed,
            rx_q         => rx_q_signed,
            rx_valid     => rx_valid,
            raw_power_dbg => raw_power,
            raw_valid_dbg => raw_valid,
            sample_power => sample_power,
            sample_valid => sample_valid
        );

    -- =========================================================================
    -- Registered ingress handoff (rx_clk domain)
    --
    -- This isolates the downsampler from the wrapper's debug counters and CDC
    -- launch logic. The extra cycle is fixed and preserves data/valid
    -- alignment while reducing immediate fanout on the ingress path.
    -- =========================================================================
    ingress_stage : process(rx_clk)
    begin
        if rising_edge(rx_clk) then
            if rx_reset_combined = '1' then
                sample_power_stage <= (others => '0');
                sample_valid_stage <= '0';
            else
                sample_power_stage <= sample_power;
                sample_valid_stage <= sample_valid;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- CDC: sample data crossing from rx_clk → S_AXI_ACLK
    --
    -- The fractional 30.72 MHz -> 16.00 MHz downsampler can emit on
    -- consecutive input beats, so the earlier hold-data + toggle scheme did
    -- not have deterministic timing margin. A small async FIFO gives the
    -- crossing real ordering and timing guarantees.
    -- =========================================================================
    U_sample_fifo : entity work.async_sample_fifo
        generic map (
            WIDTH => INPUT_POWER_WIDTH,
            DEPTH => SAMPLE_FIFO_DEPTH
        )
        port map (
            wr_clock => rx_clk,
            wr_reset => rx_reset_combined,
            wr_data  => sample_power_stage,
            wr_en    => sample_valid_stage,
            full     => sample_fifo_full,
            overflow => sample_fifo_overflow,
            rd_clock => S_AXI_ACLK,
            rd_reset => sample_fifo_rd_reset,
            rd_data  => sample_fifo_rd_data,
            rd_en    => sample_fifo_rd_en,
            empty    => sample_fifo_empty
        );

    sample_fifo_reset : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if core_reset = '1' then
                sample_fifo_rd_reset <= '1';
                soft_reset_toggle_prev_axi <= soft_reset_toggle;
            else
                sample_fifo_rd_reset <= '0';
                if soft_reset_toggle /= soft_reset_toggle_prev_axi then
                    sample_fifo_rd_reset <= '1';
                end if;
                soft_reset_toggle_prev_axi <= soft_reset_toggle;
            end if;
        end if;
    end process;

    sample_fifo_rd_en <= '1' when (sample_fifo_rd_reset = '0' and sample_fifo_empty = '0') else '0';

    sample_fifo_to_core : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if sample_fifo_rd_reset = '1' then
                core_sample_power <= (others => '0');
                core_sample_valid <= '0';
            else
                core_sample_valid <= '0';
                if sample_fifo_empty = '0' then
                    core_sample_power <= sample_fifo_rd_data;
                    core_sample_valid <= '1';
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Decode core (S_AXI_ACLK domain — 100 MHz)
    --
    -- The entire decode pipeline, timestamp counter, and AXI register
    -- interface all run on the PS clock. This gives ~10 ns timing budget,
    -- which closes easily for the original RTL design.
    --
    -- Note: timestamp_counter now counts at 100 MHz. PS-side TOA/PPS
    -- conversion must use 100 MHz, not the sample rate.
    -- =========================================================================
    U_core : entity work.adsb_top
        generic map (
            NUM_DECODERS       => NUM_DECODERS,
            FIFO_DEPTH         => FIFO_DEPTH,
            ENABLE_DEEP_DEBUG  => ENABLE_DEEP_DEBUG,
            BUILD_ID           => BUILD_ID,
            C_S_AXI_DATA_WIDTH => C_S_AXI_DATA_WIDTH,
            C_S_AXI_ADDR_WIDTH => C_S_AXI_ADDR_WIDTH
        )
        port map (
            clock         => S_AXI_ACLK,
            reset         => core_reset,
            in_power      => core_sample_power,
            in_valid      => core_sample_valid,
            pps_in        => pps_in,
            raw_power_max => raw_power_max,
            raw_iq_75pct_count => raw_iq_75pct_count,
            raw_iq_87p5pct_count => raw_iq_87p5pct_count,
            raw_iq_near_rail_count => raw_iq_near_rail_count,
            raw_power_sat_count => raw_power_sat_count,
            raw_power_thresh_count => raw_power_thr_count,
            sample_power_max   => sample_power_max,
            edge_thresh_count  => edge_thresh_count,
            power_thresh_count => power_thresh_count,
            sample_fifo_overflow_count => sample_fifo_overflow_count,
            debug_edge_count   => debug_edge_count,
            debug_som_count    => debug_som_count,
            debug_msg_count    => debug_msg_count,
            debug_edge_shape_count => debug_edge_shape_count,
            debug_edge_qual_count  => debug_edge_qual_count,
            debug_preamble_pass_count => debug_preamble_pass_count,
            debug_preamble_detect_count => debug_preamble_detect_count,
            debug_preamble_abs_count => debug_preamble_abs_count,
            debug_preamble_quiet_count => debug_preamble_quiet_count,
            debug_preamble_quiet_a_fail_count => debug_preamble_quiet_a_fail_count,
            debug_preamble_quiet_b_fail_count => debug_preamble_quiet_b_fail_count,
            debug_preamble_quiet_c_fail_count => debug_preamble_quiet_c_fail_count,
            debug_preamble_quiet_d_fail_count => debug_preamble_quiet_d_fail_count,
            debug_preamble_snr_count => debug_preamble_snr_count,
            debug_preamble_holdoff_count => debug_preamble_holdoff_count,
            debug_preamble_peak_age => debug_preamble_peak_age,
            agg_valid_count    => agg_valid_count,
            fifo_wr_count      => fifo_wr_count,
            rx_valid_count     => rx_valid_count,
            sample_valid_count => sample_valid_count,
            rx_clk_count       => rx_clk_count,
            raw_capture_data   => (others => '0'),
            adc_code_min       => (others => '0'),
            adc_code_max       => (others => '0'),
            adc_bit_or         => (others => '0'),
            adc_bit_and        => (others => '0'),
            adc_bit_toggle     => (others => '0'),
            adc_otr_count      => (others => '0'),
            soft_reset_toggle_out => soft_reset_toggle,
            sample_capture_trigger_toggle_out => open,
            raw_capture_index_out => open,
            irq           => irq,
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
