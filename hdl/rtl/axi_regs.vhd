-- =============================================================================
-- axi_regs.vhd — AXI4-Lite Slave Register Interface
-- =============================================================================
--
-- Provides a memory-mapped register interface for the Linux PS to read
-- decoded ADS-B messages and timestamps from the PL decode pipeline.
--
-- Register Map (byte addresses, 16-slot window: 0x00–0x3C):
--   0x00  MSG_DATA_0  (R)   Message bits [31:0]
--   0x04  MSG_DATA_1  (R)   Message bits [63:32]
--   0x08  MSG_DATA_2  (R)   Message bits [95:64]
--   0x0C  MSG_DATA_3  (R)   Message bits [111:96] (upper 16 bits zero)
--   0x10  TOA_LO      (R)   Timestamp bits [31:0]
--   0x14  TOA_HI      (R)   Timestamp bits [63:32]
--   0x18  RPL         (R)   Signal level [23:0] — READING POPS THE FIFO
--   0x1C  STATUS      (R)   [0] = not empty, [1] = full, [2] = overflow (sticky)
--                            [14:8] = FIFO fill count
--   0x20  PPS_COUNT   (R)   PPS pulse count [31:0]
--   0x24  PPS_CTR_LO  (R)   Counter at last PPS [31:0]
--   0x28  PPS_CTR_HI  (R)   Counter at last PPS [63:32]
--   0x2C  CONTROL     (RW)  [0] = soft reset (auto-clear), [1] = enable,
--                            [2] = debug snapshot request (write-1 pulses)
--   0x30  VERSION     (R)   Hardware version (0x00010000 = v1.0.0)
--   0x34  DBG_INDEX   (RW)  Debug counter selector [5:0]
--   0x38  DBG_DATA    (R)   Reads the debug counter selected by DBG_INDEX
--   0x3C  CONFIG      (RW)  [2:0] = quiet_score_shift, [5:3] = snr_ratio_shift
--
-- Debug counter indices (write to DBG_INDEX, read from DBG_DATA):
--    0  POWER_MAX          Maximum sample_power seen after downsampling [23:0]
--    1  EDGE_THR_CT        downsampled samples above edge threshold
--    2  POWER_THR_CT       downsampled samples above main power threshold
--    3  EDGE_CT            Asserted edge-detector outputs
--    4  SOM_CT             Preamble/SOM assignments
--    5  MSG_CT             Decoded-message valid pulses
--    6  EDGE_SHAPE_CT      Edge-shape candidates
--    7  EDGE_QUAL_CT       Edge-threshold-qualified windows
--    8  PRE_PASS_CT        Preamble quality-gate passes
--    9  PRE_DET_CT         Preamble detections after peak logic
--   17  PRE_ABS_CT         All four pulse-sum threshold checks passed
--   18  PRE_QUIET_CT       Quiet-zone checks passed after pulse threshold
--   19  PRE_SNR_CT         Aggregate SNR check passed after quiet checks
--   20  PRE_HOLDOFF_CT     Qualified preambles suppressed by holdoff
--   31  PRE_QA_FAIL_CT     Quiet-zone A failures after pulse threshold
--   32  PRE_QB_FAIL_CT     Quiet-zone B failures after pulse threshold
--   33  PRE_QC_FAIL_CT     Quiet-zone C failures after pulse threshold
--   34  PRE_QD_FAIL_CT     Quiet-zone D failures after pulse threshold
--   36  PRE_NOFREE_CT      Preambles dropped because all decoders were busy
--   37  PRE_BUSY_DROP_CT   Pending decoder claim lost because target became busy
--   38  DEC_BUSY_MAX       Peak number of simultaneously busy decoders
--   39  AGG_DROP_CT        Messages dropped because aggregator holding slot was full
--   40  DF4_CT             Forwarded DF4 outputs
--   41  DF5_CT             Forwarded DF5 outputs
--   42  DF11_CT            Valid DF11 outputs
--   43  DF17_CT            Valid DF17 outputs
--   44  DF18_CT            Valid DF18 outputs
--   45  CAND_DF4_CT        Assembled pre-CRC candidates classified as DF4
--   46  CAND_DF5_CT        Assembled pre-CRC candidates classified as DF5
--   47  CAND_DF11_CT       Assembled pre-CRC candidates classified as DF11
--   48  RAW_POWER_MAX      Maximum scalar power seen before downsampling [23:0]
--   49  RAW_POWER_THR_CT   pre-downsample samples above main power threshold
--   53  RAW_IQ_75PCT_CT    raw I/Q samples with either lane above 75% full scale
--   54  RAW_IQ_87P5PCT_CT  raw I/Q samples with either lane above 87.5% full scale
--   50  RAW_IQ_NEARRAIL_CT raw I/Q samples with either lane near full scale
--   51  RAW_POWER_SAT_CT   pre-downsample scalar power samples near ceiling
--   52  SAMPLE_FIFO_OVF_CT samples dropped because the RX→core sample FIFO was full
--   10  AGG_VALID_CT       Aggregator output-valid pulses
--   11  FIFO_WR_CT         FIFO write strobes
--   12  RX_VALID_CT        Raw wrapper rx_valid count
--   13  SMP_VALID_CT       Wrapper sample_valid count
--   14  CORE_CLK_CT        Decoder-core clock cycle count
--   15  CORE_INVAL_CT      Decoder-core in_valid count
--   16  CORE_STATE         [0]=combined_reset [1]=decoder_enable [2]=in_valid
--
-- Auto-pop protocol:
--   1. Linux checks STATUS bit 0 (not_empty)
--   2. Reads MSG_DATA_0..3, TOA_LO/HI at leisure
--   3. Reads RPL — this atomically pops the FIFO, advancing to next message
--   4. Repeat from step 1
--
-- For Zynq deployment: This module runs on S_AXI_ACLK. In simulation,
-- connect to the same 16 MHz clock. For hardware, connect to FCLK_CLK0
-- (100 MHz) with the upstream async_msg_fifo providing the CDC bridge.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity axi_regs is
    generic (
        ENABLE_DEEP_DEBUG   : boolean := false;
        BUILD_ID           : integer := 0;
        C_S_AXI_DATA_WIDTH : integer := 32;
        C_S_AXI_ADDR_WIDTH : integer := 12
    );
    port (
        -- =====================================================================
        -- FIFO read interface (connected to async_msg_fifo read side)
        -- =====================================================================
        fifo_rd_data  : in  std_logic_vector(199 downto 0);   -- Packed: [111:0]=msg, [175:112]=toa, [199:176]=rpl
        fifo_empty    : in  std_logic;
        fifo_full     : in  std_logic;
        fifo_count    : in  unsigned(6 downto 0);
        fifo_overflow : in  std_logic;
        fifo_rd_en    : out std_logic;                         -- Pop strobe

        -- =====================================================================
        -- PPS / timestamp inputs (directly from timestamp_counter)
        -- =====================================================================
        pps_count      : in  unsigned(31 downto 0);
        counter_at_pps : in  unsigned(COUNTER_WIDTH-1 downto 0);

        -- =====================================================================
        -- Bring-up debug inputs (sample-clock domain, snapshotted upstream)
        -- =====================================================================
        raw_power_max      : in  unsigned(INPUT_POWER_WIDTH-1 downto 0);
        raw_iq_75pct_count : in unsigned(31 downto 0);
        raw_iq_87p5pct_count : in unsigned(31 downto 0);
        raw_iq_near_rail_count : in unsigned(31 downto 0);
        raw_power_sat_count : in unsigned(31 downto 0);
        sample_power_max   : in  unsigned(INPUT_POWER_WIDTH-1 downto 0);
        edge_thresh_count  : in  unsigned(31 downto 0);
        power_thresh_count : in  unsigned(31 downto 0);
        sample_fifo_overflow_count : in unsigned(31 downto 0);
        raw_power_thresh_count : in unsigned(31 downto 0);
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
        debug_preamble_no_free_count : in unsigned(31 downto 0);
        debug_preamble_busy_drop_count : in unsigned(31 downto 0);
        debug_decoder_busy_max : in unsigned(31 downto 0);
        debug_smallest_done_count : in unsigned(31 downto 0);
        debug_invalid_df_count : in unsigned(31 downto 0);
        debug_crc_attempt_count : in unsigned(31 downto 0);
        debug_crc_pass_count : in unsigned(31 downto 0);
        debug_crc_exhausted_count : in unsigned(31 downto 0);
        debug_df4_count : in unsigned(31 downto 0);
        debug_df5_count : in unsigned(31 downto 0);
        debug_df11_count : in unsigned(31 downto 0);
        debug_cand_df4_count : in unsigned(31 downto 0);
        debug_cand_df5_count : in unsigned(31 downto 0);
        debug_cand_df11_count : in unsigned(31 downto 0);
        debug_df17_count : in unsigned(31 downto 0);
        debug_df18_count : in unsigned(31 downto 0);
        debug_crc0_w0 : in unsigned(31 downto 0);
        debug_crc0_w1 : in unsigned(31 downto 0);
        debug_crc0_w2 : in unsigned(31 downto 0);
        debug_crc0_w3 : in unsigned(31 downto 0);
        agg_valid_count    : in unsigned(31 downto 0);
        agg_drop_count     : in unsigned(31 downto 0);
        fifo_wr_count      : in unsigned(31 downto 0);
        rx_valid_count     : in unsigned(31 downto 0);
        sample_valid_count : in unsigned(31 downto 0);
        core_clk_count     : in unsigned(31 downto 0);
        core_in_valid_count : in unsigned(31 downto 0);
        core_state         : in std_logic_vector(31 downto 0);
        rx_clk_count       : in unsigned(31 downto 0);
        debug_snapshot_req : out std_logic;
        quiet_score_shift_cfg : out unsigned(2 downto 0);
        snr_ratio_shift_cfg   : out unsigned(2 downto 0);
        holdoff_cfg           : out unsigned(11 downto 0);

        -- =====================================================================
        -- Decoder control outputs
        -- =====================================================================
        soft_reset     : out std_logic;                        -- Pulse to reset decode pipeline (AXI domain)
        soft_reset_toggle : out std_logic;                     -- Toggle for CDC pulse synchronizer
        decoder_enable : out std_logic;                        -- Enable/disable decoders (AXI domain level)

        -- =====================================================================
        -- Interrupt
        -- =====================================================================
        irq            : out std_logic;                        -- Level: high when FIFO not empty

        -- =====================================================================
        -- AXI4-Lite Slave Interface
        -- =====================================================================
        S_AXI_ACLK    : in  std_logic;
        S_AXI_ARESETN : in  std_logic;                        -- Active-low reset (AXI convention)

        -- Write address channel
        S_AXI_AWADDR  : in  std_logic_vector(C_S_AXI_ADDR_WIDTH-1 downto 0);
        S_AXI_AWPROT  : in  std_logic_vector(2 downto 0);
        S_AXI_AWVALID : in  std_logic;
        S_AXI_AWREADY : out std_logic;

        -- Write data channel
        S_AXI_WDATA   : in  std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0);
        S_AXI_WSTRB   : in  std_logic_vector(C_S_AXI_DATA_WIDTH/8-1 downto 0);
        S_AXI_WVALID  : in  std_logic;
        S_AXI_WREADY  : out std_logic;

        -- Write response channel
        S_AXI_BRESP   : out std_logic_vector(1 downto 0);
        S_AXI_BVALID  : out std_logic;
        S_AXI_BREADY  : in  std_logic;

        -- Read address channel
        S_AXI_ARADDR  : in  std_logic_vector(C_S_AXI_ADDR_WIDTH-1 downto 0);
        S_AXI_ARPROT  : in  std_logic_vector(2 downto 0);
        S_AXI_ARVALID : in  std_logic;
        S_AXI_ARREADY : out std_logic;

        -- Read data channel
        S_AXI_RDATA   : out std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0);
        S_AXI_RRESP   : out std_logic_vector(1 downto 0);
        S_AXI_RVALID  : out std_logic;
        S_AXI_RREADY  : in  std_logic
    );
end entity;

architecture arch of axi_regs is

    function version_hi(deep_debug_enabled : boolean) return std_logic_vector is
    begin
        if deep_debug_enabled then
            return x"8001";
        end if;
        return x"0001";
    end function;

    -- =========================================================================
    -- Constants
    -- =========================================================================
    constant VERSION_REG : std_logic_vector(31 downto 0) :=
        version_hi(ENABLE_DEEP_DEBUG) &
        std_logic_vector(to_unsigned(BUILD_ID mod 65536, 16));  -- bit31=deep-debug, v1.0.build

    -- Register index (byte address >> 2) — only 16 slots (0x00–0x3C) are
    -- reliably addressable through the Zynq AXI interconnect.
    constant IDX_MSG_DATA_0 : natural := 0;   -- 0x00
    constant IDX_MSG_DATA_1 : natural := 1;   -- 0x04
    constant IDX_MSG_DATA_2 : natural := 2;   -- 0x08
    constant IDX_MSG_DATA_3 : natural := 3;   -- 0x0C
    constant IDX_TOA_LO     : natural := 4;   -- 0x10
    constant IDX_TOA_HI     : natural := 5;   -- 0x14
    constant IDX_RPL        : natural := 6;   -- 0x18
    constant IDX_STATUS     : natural := 7;   -- 0x1C
    constant IDX_PPS_COUNT  : natural := 8;   -- 0x20
    constant IDX_PPS_CTR_LO : natural := 9;   -- 0x24
    constant IDX_PPS_CTR_HI : natural := 10;  -- 0x28
    constant IDX_CONTROL    : natural := 11;  -- 0x2C
    constant IDX_VERSION    : natural := 12;  -- 0x30
    constant IDX_DBG_INDEX  : natural := 13;  -- 0x34
    constant IDX_DBG_DATA   : natural := 14;  -- 0x38
    constant IDX_CONFIG     : natural := 15;  -- 0x3C

    -- Debug counter indices (written to DBG_INDEX, selects DBG_DATA mux)
    constant DBG_POWER_MAX    : natural := 0;
    constant DBG_EDGE_THR_CT  : natural := 1;
    constant DBG_POWER_THR_CT : natural := 2;
    constant DBG_EDGE_CT      : natural := 3;
    constant DBG_SOM_CT       : natural := 4;
    constant DBG_MSG_CT       : natural := 5;
    constant DBG_EDGE_SHAPE_CT : natural := 6;
    constant DBG_EDGE_QUAL_CT  : natural := 7;
    constant DBG_PRE_PASS_CT   : natural := 8;
    constant DBG_PRE_DET_CT    : natural := 9;
    constant DBG_AGG_VALID_CT  : natural := 10;
    constant DBG_FIFO_WR_CT    : natural := 11;
    constant DBG_RX_VALID_CT   : natural := 12;
    constant DBG_SMP_VALID_CT  : natural := 13;
    constant DBG_CORE_CLK_CT   : natural := 14;
    constant DBG_CORE_INVAL_CT : natural := 15;
    constant DBG_CORE_STATE    : natural := 16;
    constant DBG_PRE_ABS_CT    : natural := 17;
    constant DBG_PRE_QUIET_CT  : natural := 18;
    constant DBG_PRE_SNR_CT    : natural := 19;
    constant DBG_PRE_HOLDOFF_CT : natural := 20;
    constant DBG_RX_CLK_CT      : natural := 21;
    constant DBG_SMALLEST_DONE_CT : natural := 22;
    constant DBG_INVALID_DF_CT    : natural := 23;
    constant DBG_CRC_ATTEMPT_CT   : natural := 24;
    constant DBG_CRC_PASS_CT      : natural := 25;
    constant DBG_CRC_EXHAUST_CT   : natural := 26;
    constant DBG_CRC0_W0          : natural := 27;
    constant DBG_CRC0_W1          : natural := 28;
    constant DBG_CRC0_W2          : natural := 29;
    constant DBG_CRC0_W3          : natural := 30;
    constant DBG_PRE_QA_FAIL_CT   : natural := 31;
    constant DBG_PRE_QB_FAIL_CT   : natural := 32;
    constant DBG_PRE_QC_FAIL_CT   : natural := 33;
    constant DBG_PRE_QD_FAIL_CT   : natural := 34;
    constant DBG_PRE_PEAK_AGE     : natural := 35;
    constant DBG_PRE_NOFREE_CT    : natural := 36;
    constant DBG_PRE_BUSY_DROP_CT : natural := 37;
    constant DBG_DEC_BUSY_MAX     : natural := 38;
    constant DBG_AGG_DROP_CT      : natural := 39;
    constant DBG_DF4_CT           : natural := 40;
    constant DBG_DF5_CT           : natural := 41;
    constant DBG_DF11_CT          : natural := 42;
    constant DBG_DF17_CT          : natural := 43;
    constant DBG_DF18_CT          : natural := 44;
    constant DBG_CAND_DF4_CT      : natural := 45;
    constant DBG_CAND_DF5_CT      : natural := 46;
    constant DBG_CAND_DF11_CT     : natural := 47;
    constant DBG_RAW_POWER_MAX    : natural := 48;
    constant DBG_RAW_POWER_THR_CT : natural := 49;
    constant DBG_RAW_IQ_NEARRAIL_CT : natural := 50;
    constant DBG_RAW_POWER_SAT_CT : natural := 51;
    constant DBG_SAMPLE_FIFO_OVF_CT : natural := 52;
    constant DBG_RAW_IQ_75PCT_CT  : natural := 53;
    constant DBG_RAW_IQ_87P5PCT_CT : natural := 54;

    -- =========================================================================
    -- Unpack FIFO data
    -- =========================================================================
    signal fifo_msg : std_logic_vector(111 downto 0);
    signal fifo_toa : std_logic_vector(63 downto 0);
    signal fifo_rpl : std_logic_vector(23 downto 0);

    -- =========================================================================
    -- AXI internal signals
    -- =========================================================================
    signal axi_awready : std_logic := '0';
    signal axi_wready  : std_logic := '0';
    signal axi_bvalid  : std_logic := '0';
    signal axi_arready : std_logic := '0';
    signal axi_rvalid  : std_logic := '0';
    signal axi_rdata   : std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0) := (others => '0');
    signal axi_rdata_stage : std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0) := (others => '0');
    signal read_pending : std_logic := '0';

    -- Latched addresses
    signal aw_addr_latched : std_logic_vector(C_S_AXI_ADDR_WIDTH-1 downto 0) := (others => '0');
    signal ar_addr_latched : std_logic_vector(C_S_AXI_ADDR_WIDTH-1 downto 0) := (others => '0');

    -- Control register
    signal control_reg : std_logic_vector(31 downto 0) := x"00000002";  -- Enable=1 by default
    signal config_reg  : std_logic_vector(31 downto 0) := (others => '0');

    -- Debug index register
    signal dbg_index_reg : unsigned(5 downto 0) := (others => '0');

    -- Pre-computed debug data (updated every cycle from dbg_index_reg)
    signal dbg_data_reg : std_logic_vector(31 downto 0) := (others => '0');

    -- FIFO pop request
    signal fifo_pop : std_logic := '0';
    signal snapshot_req_toggle : std_logic := '0';
    signal soft_reset_toggle_i : std_logic := '0';

    -- Internal reset (active high, from AXI active-low)
    signal rst : std_logic;

begin

    rst <= not S_AXI_ARESETN;

    -- Unpack FIFO read data
    fifo_msg <= fifo_rd_data(111 downto 0);
    fifo_toa <= fifo_rd_data(175 downto 112);
    fifo_rpl <= fifo_rd_data(199 downto 176);

    -- Drive AXI outputs
    S_AXI_AWREADY <= axi_awready;
    S_AXI_WREADY  <= axi_wready;
    S_AXI_BRESP   <= "00";           -- OKAY response always
    S_AXI_BVALID  <= axi_bvalid;
    S_AXI_ARREADY <= axi_arready;
    S_AXI_RDATA   <= axi_rdata;
    S_AXI_RRESP   <= "00";           -- OKAY response always
    S_AXI_RVALID  <= axi_rvalid;

    -- FIFO pop
    fifo_rd_en <= fifo_pop;

    -- Control outputs
    soft_reset     <= control_reg(0);
    soft_reset_toggle <= soft_reset_toggle_i;
    decoder_enable <= control_reg(1);
    debug_snapshot_req <= snapshot_req_toggle;
    quiet_score_shift_cfg <= unsigned(config_reg(2 downto 0));
    snr_ratio_shift_cfg   <= unsigned(config_reg(5 downto 3));
    holdoff_cfg           <= unsigned(config_reg(17 downto 6));

    -- Interrupt: level-sensitive, high when messages available
    irq <= not fifo_empty;

    -- =========================================================================
    -- Write Address Channel
    -- =========================================================================
    aw_handshake : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if rst = '1' then
                axi_awready <= '0';
                aw_addr_latched <= (others => '0');
            else
                if axi_awready = '0' and S_AXI_AWVALID = '1' and S_AXI_WVALID = '1' then
                    axi_awready <= '1';
                    aw_addr_latched <= S_AXI_AWADDR;
                else
                    axi_awready <= '0';
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Write Data Channel
    -- =========================================================================
    w_handshake : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if rst = '1' then
                axi_wready <= '0';
            else
                if axi_wready = '0' and S_AXI_AWVALID = '1' and S_AXI_WVALID = '1' then
                    axi_wready <= '1';
                else
                    axi_wready <= '0';
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Write Response Channel
    -- =========================================================================
    b_handshake : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if rst = '1' then
                axi_bvalid <= '0';
            else
                if axi_awready = '1' and S_AXI_AWVALID = '1' and
                   axi_wready = '1' and S_AXI_WVALID = '1' and
                   axi_bvalid = '0' then
                    axi_bvalid <= '1';
                elsif S_AXI_BREADY = '1' and axi_bvalid = '1' then
                    axi_bvalid <= '0';
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Write Data to Registers
    -- CONTROL (0x2C) and DBG_INDEX (0x34, 6-bit selector) are writable.
    -- =========================================================================
    write_regs : process(S_AXI_ACLK)
        variable addr_idx : natural;
    begin
        if rising_edge(S_AXI_ACLK) then
            if rst = '1' then
                control_reg <= x"00000002";  -- Enable=1, soft_reset=0
                config_reg <= (others => '0');
                config_reg(2 downto 0) <= std_logic_vector(to_unsigned(QUIET_SCORE_SHIFT, 3));
                config_reg(5 downto 3) <= std_logic_vector(to_unsigned(SNR_RATIO_SHIFT, 3));
                config_reg(17 downto 6) <= std_logic_vector(to_unsigned(PREAMBLE_HOLDOFF_DEFAULT, 12));
                soft_reset_toggle_i <= '0';
                dbg_index_reg <= (others => '0');
            else
                -- Auto-clear pulse-style control bits after one cycle
                control_reg(0) <= '0';
                control_reg(2) <= '0';

                if axi_awready = '1' and S_AXI_AWVALID = '1' and
                   axi_wready = '1' and S_AXI_WVALID = '1' then
                    addr_idx := to_integer(unsigned(aw_addr_latched(C_S_AXI_ADDR_WIDTH-1 downto 2)));
                    if addr_idx = IDX_CONTROL then
                        -- Byte-lane write strobes
                        for i in 0 to 3 loop
                            if S_AXI_WSTRB(i) = '1' then
                                control_reg(i*8+7 downto i*8) <= S_AXI_WDATA(i*8+7 downto i*8);
                            end if;
                        end loop;
                        -- Toggle-based CDC for soft_reset pulse
                        if S_AXI_WSTRB(0) = '1' and S_AXI_WDATA(0) = '1' then
                            soft_reset_toggle_i <= not soft_reset_toggle_i;
                        end if;
                        if S_AXI_WSTRB(0) = '1' and S_AXI_WDATA(2) = '1' then
                            snapshot_req_toggle <= not snapshot_req_toggle;
                        end if;
                    elsif addr_idx = IDX_DBG_INDEX then
                        if S_AXI_WSTRB(0) = '1' then
                            dbg_index_reg <= unsigned(S_AXI_WDATA(5 downto 0));
                        end if;
                    elsif addr_idx = IDX_CONFIG then
                        for i in 0 to 3 loop
                            if S_AXI_WSTRB(i) = '1' then
                                config_reg(i*8+7 downto i*8) <= S_AXI_WDATA(i*8+7 downto i*8);
                            end if;
                        end loop;
                    end if;
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Debug data pre-select — runs every cycle so the result is registered
    -- before the AXI read mux ever sees it. This breaks the two-level mux
    -- (address decode → debug index decode) into two pipeline stages.
    -- =========================================================================
    dbg_preselect : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            case to_integer(dbg_index_reg) is
                when DBG_POWER_MAX      => dbg_data_reg <= std_logic_vector(resize(sample_power_max, 32));
                when DBG_EDGE_THR_CT    => dbg_data_reg <= std_logic_vector(edge_thresh_count);
                when DBG_POWER_THR_CT   => dbg_data_reg <= std_logic_vector(power_thresh_count);
                when DBG_EDGE_CT        => dbg_data_reg <= std_logic_vector(debug_edge_count);
                when DBG_SOM_CT         => dbg_data_reg <= std_logic_vector(debug_som_count);
                when DBG_MSG_CT         => dbg_data_reg <= std_logic_vector(debug_msg_count);
                when DBG_EDGE_SHAPE_CT  => dbg_data_reg <= std_logic_vector(debug_edge_shape_count);
                when DBG_EDGE_QUAL_CT   => dbg_data_reg <= std_logic_vector(debug_edge_qual_count);
                when DBG_PRE_PASS_CT    => dbg_data_reg <= std_logic_vector(debug_preamble_pass_count);
                when DBG_PRE_DET_CT     => dbg_data_reg <= std_logic_vector(debug_preamble_detect_count);
                when DBG_AGG_VALID_CT   => dbg_data_reg <= std_logic_vector(agg_valid_count);
                when DBG_FIFO_WR_CT     => dbg_data_reg <= std_logic_vector(fifo_wr_count);
                when DBG_RX_VALID_CT    => dbg_data_reg <= std_logic_vector(rx_valid_count);
                when DBG_SMP_VALID_CT   => dbg_data_reg <= std_logic_vector(sample_valid_count);
                when DBG_CORE_CLK_CT    => dbg_data_reg <= std_logic_vector(core_clk_count);
                when DBG_CORE_INVAL_CT  => dbg_data_reg <= std_logic_vector(core_in_valid_count);
                when DBG_CORE_STATE     => dbg_data_reg <= core_state;
                when DBG_PRE_ABS_CT     => dbg_data_reg <= std_logic_vector(debug_preamble_abs_count);
                when DBG_PRE_QUIET_CT   => dbg_data_reg <= std_logic_vector(debug_preamble_quiet_count);
                when DBG_PRE_SNR_CT     => dbg_data_reg <= std_logic_vector(debug_preamble_snr_count);
                when DBG_PRE_HOLDOFF_CT => dbg_data_reg <= std_logic_vector(debug_preamble_holdoff_count);
                when DBG_RX_CLK_CT      => dbg_data_reg <= std_logic_vector(rx_clk_count);
                when DBG_SMALLEST_DONE_CT => dbg_data_reg <= std_logic_vector(debug_smallest_done_count);
                when DBG_INVALID_DF_CT    => dbg_data_reg <= std_logic_vector(debug_invalid_df_count);
                when DBG_CRC_ATTEMPT_CT   => dbg_data_reg <= std_logic_vector(debug_crc_attempt_count);
                when DBG_CRC_PASS_CT      => dbg_data_reg <= std_logic_vector(debug_crc_pass_count);
                when DBG_CRC_EXHAUST_CT   => dbg_data_reg <= std_logic_vector(debug_crc_exhausted_count);
                when DBG_CRC0_W0          => dbg_data_reg <= std_logic_vector(debug_crc0_w0);
                when DBG_CRC0_W1          => dbg_data_reg <= std_logic_vector(debug_crc0_w1);
                when DBG_CRC0_W2          => dbg_data_reg <= std_logic_vector(debug_crc0_w2);
                when DBG_CRC0_W3          => dbg_data_reg <= std_logic_vector(debug_crc0_w3);
                when DBG_PRE_QA_FAIL_CT   => dbg_data_reg <= std_logic_vector(debug_preamble_quiet_a_fail_count);
                when DBG_PRE_QB_FAIL_CT   => dbg_data_reg <= std_logic_vector(debug_preamble_quiet_b_fail_count);
                when DBG_PRE_QC_FAIL_CT   => dbg_data_reg <= std_logic_vector(debug_preamble_quiet_c_fail_count);
                when DBG_PRE_QD_FAIL_CT   => dbg_data_reg <= std_logic_vector(debug_preamble_quiet_d_fail_count);
                when DBG_PRE_PEAK_AGE     => dbg_data_reg <= std_logic_vector(debug_preamble_peak_age);
                when DBG_PRE_NOFREE_CT    => dbg_data_reg <= std_logic_vector(debug_preamble_no_free_count);
                when DBG_PRE_BUSY_DROP_CT => dbg_data_reg <= std_logic_vector(debug_preamble_busy_drop_count);
                when DBG_DEC_BUSY_MAX     => dbg_data_reg <= std_logic_vector(debug_decoder_busy_max);
                when DBG_AGG_DROP_CT      => dbg_data_reg <= std_logic_vector(agg_drop_count);
                when DBG_DF4_CT           => dbg_data_reg <= std_logic_vector(debug_df4_count);
                when DBG_DF5_CT           => dbg_data_reg <= std_logic_vector(debug_df5_count);
                when DBG_DF11_CT          => dbg_data_reg <= std_logic_vector(debug_df11_count);
                when DBG_DF17_CT          => dbg_data_reg <= std_logic_vector(debug_df17_count);
                when DBG_DF18_CT          => dbg_data_reg <= std_logic_vector(debug_df18_count);
                when DBG_CAND_DF4_CT      => dbg_data_reg <= std_logic_vector(debug_cand_df4_count);
                when DBG_CAND_DF5_CT      => dbg_data_reg <= std_logic_vector(debug_cand_df5_count);
                when DBG_CAND_DF11_CT     => dbg_data_reg <= std_logic_vector(debug_cand_df11_count);
                when DBG_RAW_POWER_MAX    => dbg_data_reg <= std_logic_vector(resize(raw_power_max, 32));
                when DBG_RAW_POWER_THR_CT => dbg_data_reg <= std_logic_vector(raw_power_thresh_count);
                when DBG_RAW_IQ_NEARRAIL_CT => dbg_data_reg <= std_logic_vector(raw_iq_near_rail_count);
                when DBG_RAW_POWER_SAT_CT => dbg_data_reg <= std_logic_vector(raw_power_sat_count);
                when DBG_SAMPLE_FIFO_OVF_CT => dbg_data_reg <= std_logic_vector(sample_fifo_overflow_count);
                when DBG_RAW_IQ_75PCT_CT  => dbg_data_reg <= std_logic_vector(raw_iq_75pct_count);
                when DBG_RAW_IQ_87P5PCT_CT => dbg_data_reg <= std_logic_vector(raw_iq_87p5pct_count);
                when others             => dbg_data_reg <= (others => '0');
            end case;
        end if;
    end process;

    -- =========================================================================
    -- Read Address Channel
    -- =========================================================================
    ar_handshake : process(S_AXI_ACLK)
    begin
        if rising_edge(S_AXI_ACLK) then
            if rst = '1' then
                axi_arready <= '0';
                ar_addr_latched <= (others => '0');
            else
                if axi_arready = '0' and S_AXI_ARVALID = '1' then
                    axi_arready <= '1';
                    ar_addr_latched <= S_AXI_ARADDR;
                else
                    axi_arready <= '0';
                end if;
            end if;
        end if;
    end process;

    -- =========================================================================
    -- Read Data Channel
    -- Present register data one cycle after address acceptance.
    -- Auto-pop FIFO when RPL register read completes.
    -- =========================================================================
    r_handshake : process(S_AXI_ACLK)
        variable addr_idx : natural;
        variable status_word : std_logic_vector(31 downto 0);
        variable read_data_next : std_logic_vector(C_S_AXI_DATA_WIDTH-1 downto 0);
        variable pop_next : std_logic;
    begin
        if rising_edge(S_AXI_ACLK) then
            if rst = '1' then
                axi_rvalid <= '0';
                axi_rdata  <= (others => '0');
                axi_rdata_stage <= (others => '0');
                fifo_pop   <= '0';
                read_pending <= '0';
            else
                fifo_pop <= '0';  -- Default: no pop

                if axi_arready = '1' and S_AXI_ARVALID = '1' and axi_rvalid = '0' and read_pending = '0' then
                    -- Address accepted — compute read data into a staging register.
                    addr_idx := to_integer(unsigned(ar_addr_latched(C_S_AXI_ADDR_WIDTH-1 downto 2)));
                    read_data_next := (others => '0');
                    pop_next := '0';

                    case addr_idx is
                        when IDX_MSG_DATA_0 =>
                            read_data_next := fifo_msg(31 downto 0);

                        when IDX_MSG_DATA_1 =>
                            read_data_next := fifo_msg(63 downto 32);

                        when IDX_MSG_DATA_2 =>
                            read_data_next := fifo_msg(95 downto 64);

                        when IDX_MSG_DATA_3 =>
                            read_data_next(15 downto 0) := fifo_msg(111 downto 96);

                        when IDX_TOA_LO =>
                            read_data_next := fifo_toa(31 downto 0);

                        when IDX_TOA_HI =>
                            read_data_next := fifo_toa(63 downto 32);

                        when IDX_RPL =>
                            read_data_next(23 downto 0) := fifo_rpl;
                            -- Auto-pop: advance FIFO on RPL read
                            if fifo_empty = '0' then
                                pop_next := '1';
                            end if;

                        when IDX_STATUS =>
                            status_word := (others => '0');
                            status_word(0) := not fifo_empty;
                            status_word(1) := fifo_full;
                            status_word(2) := fifo_overflow;
                            status_word(14 downto 8) := std_logic_vector(fifo_count);
                            read_data_next := status_word;

                        when IDX_PPS_COUNT =>
                            read_data_next := std_logic_vector(pps_count);

                        when IDX_PPS_CTR_LO =>
                            read_data_next := std_logic_vector(counter_at_pps(31 downto 0));

                        when IDX_PPS_CTR_HI =>
                            read_data_next := std_logic_vector(counter_at_pps(63 downto 32));

                        when IDX_CONTROL =>
                            read_data_next := control_reg;

                        when IDX_VERSION =>
                            read_data_next := VERSION_REG;

                        when IDX_DBG_INDEX =>
                            read_data_next(5 downto 0) := std_logic_vector(dbg_index_reg);

                        when IDX_DBG_DATA =>
                            read_data_next := dbg_data_reg;

                        when IDX_CONFIG =>
                            read_data_next := config_reg;

                        when others =>
                            null;
                    end case;

                    axi_rdata_stage <= read_data_next;
                    fifo_pop <= pop_next;
                    read_pending <= '1';

                elsif read_pending = '1' and axi_rvalid = '0' then
                    -- Present the registered read data one cycle later.
                    axi_rdata <= axi_rdata_stage;
                    axi_rvalid <= '1';
                    read_pending <= '0';

                elsif axi_rvalid = '1' and S_AXI_RREADY = '1' then
                    -- Read data accepted by master
                    axi_rvalid <= '0';
                end if;
            end if;
        end if;
    end process;

end architecture;
