-- =============================================================================
-- adsb_logdet_bringup.vhd -- AD9203 ingress + log-to-linear LUT + decode chain
-- =============================================================================
--
-- Wires the log-detector frontend end-to-end on the Smart ZYNQ SL:
--
--   AD9203  ->  ad9203_ingress  ->  log_to_linear  ->  async_sample_fifo  ->  adsb_pl_wrapper
--                 (adc_clk,         (1-cycle ROM,    (wr: adc_clk,          (sample_clk = S_AXI_ACLK
--                  16 MHz)           adc_clk)         rd: S_AXI_ACLK)        = 100 MHz)
--
-- The decode pipeline runs on the 100 MHz S_AXI_ACLK with sample_valid
-- strobing at the 16 MSPS effective rate. This matches the architecture
-- that the pipeline was exercised against on the Pluto hardware (the
-- Fishball build) — giving the pipeline ~6× headroom between sample
-- arrivals — and brings timestamp resolution up to 10 ns (vs 62.5 ns if
-- we clocked the pipeline directly on adc_clk).
--
-- The encode clock is driven out through an ODDR at adc_clk rate.
--
-- Two diagnostic taps are kept from earlier bring-up: a 32-bit live sample
-- word and a free-running sample counter, synchronised to the AXI clock
-- domain and exposed as BD ports so the PS can read them through AXI GPIO
-- slaves. These don't interact with the decode pipeline — they're for
-- checking the raw ADC path.
--
-- pps_in is routed from a GPS module header pin into adsb_pl_wrapper so the
-- 100 MHz timestamp counter can latch UTC second boundaries. pl_irq
-- (decode-pipeline IRQ) is exposed as an output but left unconnected in the BD
-- because PS fabric interrupts are disabled in PS7 config.
--
-- VHDL-93: this entity is the top of a Vivado BD module reference, which
-- rejects VHDL-2008 top files. Dependencies further down the hierarchy
-- (adsb_pl_wrapper, adsb_top, …) are free to use VHDL-2008.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library unisim;
    use unisim.vcomponents.all;

library work;
    use work.adsb_pkg.all;
    use work.logdet_pkg.all;

entity adsb_logdet_bringup is
    port (
        -- ADC capture clock domain. Clocks the ingress + LUT and drives
        -- ENCODE out through ODDR.
        adc_clk       : in  std_logic;
        adc_rstn      : in  std_logic;

        -- External ADC pins (see hdl/vivado/constr/smartzynq_adc_io.xdc).
        adc_encode    : out std_logic;
        adc_data_a    : in  std_logic_vector(LOGDET_ADC_WIDTH-1 downto 0);
        adc_otr_a     : in  std_logic;

        -- GPS PPS input. Also routed to PS EMIO GPIO in the block design for
        -- Linux pps-gpio/chrony, but consumed directly here for hardware TOA.
        pps_in        : in  std_logic;

        -- AXI clock domain. Used by the AXI-Lite slave into the decode
        -- pipeline AND by the CDC for the diagnostic sample/counter taps.
        S_AXI_ACLK    : in  std_logic;
        S_AXI_ARESETN : in  std_logic;

        -- Diagnostic readback taps (fed into BD AXI GPIO slaves).
        live_sample_axi  : out std_logic_vector(31 downto 0);
        sample_count_axi : out std_logic_vector(31 downto 0);

        -- AXI-Lite slave: adsb_pl_wrapper's message FIFO + debug counters
        -- + config registers. 8-bit address → 256-byte window → 64 regs.
        S_AXI_AWADDR  : in  std_logic_vector(7 downto 0);
        S_AXI_AWPROT  : in  std_logic_vector(2 downto 0);
        S_AXI_AWVALID : in  std_logic;
        S_AXI_AWREADY : out std_logic;
        S_AXI_WDATA   : in  std_logic_vector(31 downto 0);
        S_AXI_WSTRB   : in  std_logic_vector(3 downto 0);
        S_AXI_WVALID  : in  std_logic;
        S_AXI_WREADY  : out std_logic;
        S_AXI_BRESP   : out std_logic_vector(1 downto 0);
        S_AXI_BVALID  : out std_logic;
        S_AXI_BREADY  : in  std_logic;
        S_AXI_ARADDR  : in  std_logic_vector(7 downto 0);
        S_AXI_ARPROT  : in  std_logic_vector(2 downto 0);
        S_AXI_ARVALID : in  std_logic;
        S_AXI_ARREADY : out std_logic;
        S_AXI_RDATA   : out std_logic_vector(31 downto 0);
        S_AXI_RRESP   : out std_logic_vector(1 downto 0);
        S_AXI_RVALID  : out std_logic;
        S_AXI_RREADY  : in  std_logic;

        -- Decode-pipeline IRQ output. Not connected to the PS in the BD.
        pl_irq        : out std_logic
    );
end entity;

architecture rtl of adsb_logdet_bringup is

    signal adc_reset : std_logic;  -- active high, synchronous to adc_clk
    signal axi_reset : std_logic;  -- active high, synchronous to S_AXI_ACLK

    -- ADC capture stage outputs.
    signal ingress_data_a : unsigned(LOGDET_ADC_WIDTH-1 downto 0);
    signal ingress_otr_a  : std_logic;
    signal ingress_valid  : std_logic;
    signal lut_code       : unsigned(LOGDET_ADC_WIDTH-1 downto 0);

    -- LUT outputs (in the adc_clk domain).
    signal lut_power : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal lut_valid : std_logic;

    -- Async FIFO outputs (in the S_AXI_ACLK domain) feeding the decode pipeline.
    signal sfifo_empty    : std_logic;
    signal sfifo_rd_en    : std_logic;
    signal sfifo_rd_data  : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal sfifo_overflow : std_logic;
    signal sample_capture_trigger_toggle : std_logic;
    signal soft_reset_toggle_sample : std_logic;
    signal capture_tog_meta  : std_logic := '0';
    signal capture_tog_sync  : std_logic := '0';
    signal capture_tog_prev  : std_logic := '0';
    signal reset_tog_meta    : std_logic := '0';
    signal reset_tog_sync    : std_logic := '0';
    signal reset_tog_prev    : std_logic := '0';
    signal raw_capture_word  : std_logic_vector(31 downto 0) := (others => '0');
    type raw_capture_t is array (0 to 63) of std_logic_vector(LOGDET_ADC_WIDTH downto 0);
    signal raw_capture_buf : raw_capture_t := (others => (others => '0'));
    signal raw_capture_frozen : std_logic := '0';
    signal raw_capture_index : unsigned(7 downto 0);
    signal adc_code_min_adc    : unsigned(LOGDET_ADC_WIDTH-1 downto 0) := (others => '1');
    signal adc_code_max_adc    : unsigned(LOGDET_ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_bit_or_adc      : unsigned(LOGDET_ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_bit_and_adc     : unsigned(LOGDET_ADC_WIDTH-1 downto 0) := (others => '1');
    signal adc_bit_toggle_adc  : unsigned(LOGDET_ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_prev_code_adc   : unsigned(LOGDET_ADC_WIDTH-1 downto 0) := (others => '0');
    signal adc_otr_count_adc   : unsigned(31 downto 0) := (others => '0');

    -- Diagnostic counter and packed sample word in the ADC domain.
    signal sample_count_adc : unsigned(31 downto 0) := (others => '0');
    signal live_sample_adc  : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_code_min_word    : std_logic_vector(31 downto 0);
    signal adc_code_max_word    : std_logic_vector(31 downto 0);
    signal adc_bit_or_word      : std_logic_vector(31 downto 0);
    signal adc_bit_and_word     : std_logic_vector(31 downto 0);
    signal adc_bit_toggle_word  : std_logic_vector(31 downto 0);
    signal adc_otr_count_word   : std_logic_vector(31 downto 0);

    -- 2-flop synchronisers into the AXI domain.
    signal live_sync1, live_sync2   : std_logic_vector(31 downto 0) := (others => '0');
    signal count_sync1, count_sync2 : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_min_sync1, adc_min_sync2 : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_max_sync1, adc_max_sync2 : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_or_sync1, adc_or_sync2 : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_and_sync1, adc_and_sync2 : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_toggle_sync1, adc_toggle_sync2 : std_logic_vector(31 downto 0) := (others => '0');
    signal adc_otr_sync1, adc_otr_sync2 : std_logic_vector(31 downto 0) := (others => '0');

    attribute ASYNC_REG : string;
    attribute ASYNC_REG of live_sync1  : signal is "TRUE";
    attribute ASYNC_REG of live_sync2  : signal is "TRUE";
    attribute ASYNC_REG of count_sync1 : signal is "TRUE";
    attribute ASYNC_REG of count_sync2 : signal is "TRUE";
    attribute ASYNC_REG of adc_min_sync1 : signal is "TRUE";
    attribute ASYNC_REG of adc_min_sync2 : signal is "TRUE";
    attribute ASYNC_REG of adc_max_sync1 : signal is "TRUE";
    attribute ASYNC_REG of adc_max_sync2 : signal is "TRUE";
    attribute ASYNC_REG of adc_or_sync1 : signal is "TRUE";
    attribute ASYNC_REG of adc_or_sync2 : signal is "TRUE";
    attribute ASYNC_REG of adc_and_sync1 : signal is "TRUE";
    attribute ASYNC_REG of adc_and_sync2 : signal is "TRUE";
    attribute ASYNC_REG of adc_toggle_sync1 : signal is "TRUE";
    attribute ASYNC_REG of adc_toggle_sync2 : signal is "TRUE";
    attribute ASYNC_REG of adc_otr_sync1 : signal is "TRUE";
    attribute ASYNC_REG of adc_otr_sync2 : signal is "TRUE";
    attribute ASYNC_REG of capture_tog_meta : signal is "TRUE";
    attribute ASYNC_REG of capture_tog_sync : signal is "TRUE";
    attribute ASYNC_REG of reset_tog_meta : signal is "TRUE";
    attribute ASYNC_REG of reset_tog_sync : signal is "TRUE";

begin

    adc_reset <= not adc_rstn;
    axi_reset <= not S_AXI_ARESETN;

    -- -------------------------------------------------------------------------
    -- ENCODE output via ODDR — 50% duty-cycle mirror of adc_clk routed through
    -- the dedicated clock-out path.
    -- -------------------------------------------------------------------------
    adc_encode_oddr : ODDR
        generic map (
            DDR_CLK_EDGE => "SAME_EDGE",
            INIT         => '0',
            SRTYPE       => "ASYNC"
        )
        port map (
            Q  => adc_encode,
            C  => adc_clk,
            CE => '1',
            D1 => '1',
            D2 => '0',
            R  => '0',
            S  => '0'
        );

    -- -------------------------------------------------------------------------
    -- Parallel-CMOS latch for the AD9203.
    -- -------------------------------------------------------------------------
    ingress : entity work.ad9203_ingress
        generic map (
            ADC_WIDTH => LOGDET_ADC_WIDTH
        )
        port map (
            clock     => adc_clk,
            reset     => adc_reset,
            adc_data  => adc_data_a,
            adc_otr   => adc_otr_a,
            out_data  => ingress_data_a,
            out_otr   => ingress_otr_a,
            out_valid => ingress_valid
        );

    -- Feed raw 10-bit ADC codes into the LUT. Do not pre-scale here;
    -- calibration belongs in log_to_linear.vhd after prototype bench capture.
    lut_code <= ingress_data_a;

    -- -------------------------------------------------------------------------
    -- Antilog LUT: ADL5513 log-compressed ADC code -> linear power scalar.
    -- One clock cycle of latency; out_valid follows in_valid pipelined.
    -- -------------------------------------------------------------------------
    log_lut : entity work.log_to_linear
        generic map (
            ADC_WIDTH    => LOGDET_ADC_WIDTH,
            OUTPUT_WIDTH => INPUT_POWER_WIDTH
        )
        port map (
            clock     => adc_clk,
            reset     => adc_reset,
            in_code   => lut_code,
            in_valid  => ingress_valid,
            out_power => lut_power,
            out_valid => lut_valid
        );

    -- -------------------------------------------------------------------------
    -- Cross-clock FIFO between the ADC-rate LUT output and the 100 MHz
    -- decode pipeline. Write side burns one cycle per adc_clk sample; read
    -- side is continuously drained whenever the FIFO is non-empty, at the
    -- faster S_AXI_ACLK rate. Average fill stays near zero (read rate is
    -- ~6× the write rate), so DEPTH=64 has massive headroom.
    -- -------------------------------------------------------------------------
    sfifo_rd_en <= not sfifo_empty;

    sample_fifo : entity work.async_sample_fifo
        generic map (
            WIDTH => INPUT_POWER_WIDTH,
            DEPTH => 64
        )
        port map (
            wr_clock => adc_clk,
            wr_reset => adc_reset,
            wr_data  => lut_power,
            wr_en    => lut_valid,
            full     => open,
            overflow => sfifo_overflow,

            rd_clock => S_AXI_ACLK,
            rd_reset => axi_reset,
            rd_data  => sfifo_rd_data,
            rd_en    => sfifo_rd_en,
            empty    => sfifo_empty
        );

    -- -------------------------------------------------------------------------
    -- Full decode pipeline. Both sample_clk AND S_AXI_ACLK run at 100 MHz
    -- here; the sample FIFO provides the CDC from the 16 MHz ADC domain.
    -- sample_valid pulses each cycle the FIFO delivered a new word, i.e. at
    -- the 16 MSPS effective rate averaged over time.
    -- -------------------------------------------------------------------------
    pl_wrapper : entity work.adsb_pl_wrapper
        port map (
            sample_clk    => S_AXI_ACLK,
            sample_reset  => axi_reset,
            sample_power  => std_logic_vector(sfifo_rd_data),
            sample_valid  => sfifo_rd_en,
            raw_capture_data => raw_capture_word,
            adc_code_min  => adc_min_sync2,
            adc_code_max  => adc_max_sync2,
            adc_bit_or    => adc_or_sync2,
            adc_bit_and   => adc_and_sync2,
            adc_bit_toggle => adc_toggle_sync2,
            adc_otr_count => adc_otr_sync2,
            pps_in        => pps_in,
            irq           => pl_irq,
            soft_reset_toggle_out => soft_reset_toggle_sample,
            sample_capture_trigger_toggle_out => sample_capture_trigger_toggle,
            raw_capture_index_out => raw_capture_index,

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

    raw_capture_proc : process(adc_clk)
        variable idx : integer range 0 to 63;
    begin
        if rising_edge(adc_clk) then
            if adc_reset = '1' then
                raw_capture_buf <= (others => (others => '0'));
                raw_capture_frozen <= '0';
                capture_tog_meta <= '0';
                capture_tog_sync <= '0';
                capture_tog_prev <= '0';
                reset_tog_meta <= '0';
                reset_tog_sync <= '0';
                reset_tog_prev <= '0';
            else
                capture_tog_meta <= sample_capture_trigger_toggle;
                capture_tog_sync <= capture_tog_meta;
                capture_tog_prev <= capture_tog_sync;
                reset_tog_meta <= soft_reset_toggle_sample;
                reset_tog_sync <= reset_tog_meta;
                reset_tog_prev <= reset_tog_sync;

                if reset_tog_sync /= reset_tog_prev then
                    raw_capture_buf <= (others => (others => '0'));
                    raw_capture_frozen <= '0';
                elsif capture_tog_sync /= capture_tog_prev then
                    raw_capture_frozen <= '1';
                end if;

                if ingress_valid = '1' and raw_capture_frozen = '0' then
                    raw_capture_buf(0 to 62) <= raw_capture_buf(1 to 63);
                    raw_capture_buf(63) <= ingress_otr_a & std_logic_vector(ingress_data_a);
                end if;
            end if;

            idx := to_integer(raw_capture_index(5 downto 0));
            raw_capture_word <= (others => '0');
            raw_capture_word(31) <= raw_capture_frozen;
            raw_capture_word(LOGDET_ADC_WIDTH downto 0) <= raw_capture_buf(idx);
        end if;
    end process;

    adc_health_proc : process(adc_clk)
    begin
        if rising_edge(adc_clk) then
            if adc_reset = '1' then
                adc_code_min_adc <= (others => '1');
                adc_code_max_adc <= (others => '0');
                adc_bit_or_adc <= (others => '0');
                adc_bit_and_adc <= (others => '1');
                adc_bit_toggle_adc <= (others => '0');
                adc_prev_code_adc <= (others => '0');
                adc_otr_count_adc <= (others => '0');
            elsif reset_tog_sync /= reset_tog_prev then
                adc_code_min_adc <= (others => '1');
                adc_code_max_adc <= (others => '0');
                adc_bit_or_adc <= (others => '0');
                adc_bit_and_adc <= (others => '1');
                adc_bit_toggle_adc <= (others => '0');
                adc_prev_code_adc <= ingress_data_a;
                adc_otr_count_adc <= (others => '0');
            elsif ingress_valid = '1' then
                if ingress_data_a < adc_code_min_adc then
                    adc_code_min_adc <= ingress_data_a;
                end if;
                if ingress_data_a > adc_code_max_adc then
                    adc_code_max_adc <= ingress_data_a;
                end if;
                adc_bit_or_adc <= adc_bit_or_adc or ingress_data_a;
                adc_bit_and_adc <= adc_bit_and_adc and ingress_data_a;
                adc_bit_toggle_adc <= adc_bit_toggle_adc or (adc_prev_code_adc xor ingress_data_a);
                adc_prev_code_adc <= ingress_data_a;
                if ingress_otr_a = '1' then
                    adc_otr_count_adc <= adc_otr_count_adc + 1;
                end if;
            end if;
        end if;
    end process;

    -- -------------------------------------------------------------------------
    -- Diagnostic taps: ADC-domain sample counter + latest-sample register,
    -- 2-flop synchronised into the AXI clock domain for GPIO readback.
    -- -------------------------------------------------------------------------
    sample_count_proc : process(adc_clk)
    begin
        if rising_edge(adc_clk) then
            if adc_reset = '1' then
                sample_count_adc <= (others => '0');
            elsif ingress_valid = '1' then
                sample_count_adc <= sample_count_adc + 1;
            end if;
        end if;
    end process;

    live_sample_adc(31 downto LOGDET_ADC_WIDTH + 1) <= (others => '0');
    live_sample_adc(LOGDET_ADC_WIDTH)               <= ingress_otr_a;
    live_sample_adc(LOGDET_ADC_WIDTH-1 downto 0)    <= std_logic_vector(ingress_data_a);
    adc_code_min_word   <= std_logic_vector(resize(adc_code_min_adc, 32));
    adc_code_max_word   <= std_logic_vector(resize(adc_code_max_adc, 32));
    adc_bit_or_word     <= std_logic_vector(resize(adc_bit_or_adc, 32));
    adc_bit_and_word    <= std_logic_vector(resize(adc_bit_and_adc, 32));
    adc_bit_toggle_word <= std_logic_vector(resize(adc_bit_toggle_adc, 32));
    adc_otr_count_word  <= std_logic_vector(adc_otr_count_adc);

    cdc_proc : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if S_AXI_ARESETN = '0' then
                live_sync1  <= (others => '0');
                live_sync2  <= (others => '0');
                count_sync1 <= (others => '0');
                count_sync2 <= (others => '0');
                adc_min_sync1 <= (others => '0');
                adc_min_sync2 <= (others => '0');
                adc_max_sync1 <= (others => '0');
                adc_max_sync2 <= (others => '0');
                adc_or_sync1 <= (others => '0');
                adc_or_sync2 <= (others => '0');
                adc_and_sync1 <= (others => '0');
                adc_and_sync2 <= (others => '0');
                adc_toggle_sync1 <= (others => '0');
                adc_toggle_sync2 <= (others => '0');
                adc_otr_sync1 <= (others => '0');
                adc_otr_sync2 <= (others => '0');
            else
                live_sync1  <= live_sample_adc;
                live_sync2  <= live_sync1;
                count_sync1 <= std_logic_vector(sample_count_adc);
                count_sync2 <= count_sync1;
                adc_min_sync1 <= adc_code_min_word;
                adc_min_sync2 <= adc_min_sync1;
                adc_max_sync1 <= adc_code_max_word;
                adc_max_sync2 <= adc_max_sync1;
                adc_or_sync1 <= adc_bit_or_word;
                adc_or_sync2 <= adc_or_sync1;
                adc_and_sync1 <= adc_bit_and_word;
                adc_and_sync2 <= adc_and_sync1;
                adc_toggle_sync1 <= adc_bit_toggle_word;
                adc_toggle_sync2 <= adc_toggle_sync1;
                adc_otr_sync1 <= adc_otr_count_word;
                adc_otr_sync2 <= adc_otr_sync1;
            end if;
        end if;
    end process;

    live_sample_axi  <= live_sync2;
    sample_count_axi <= count_sync2;

end architecture;
