-- =============================================================================
-- adsb_pl_wrapper.vhd -- Vivado-facing wrapper for the ADS-B PL core
-- =============================================================================
--
-- Presents the existing `adsb_top` entity using only std_logic/std_logic_vector
-- ports so the design can be pulled into Vivado/IP Integrator without relying
-- on signed top-level ports.
--
-- This wrapper does not solve board integration by itself. It is the module
-- boundary that a Zynq block design can connect to while the exact sample
-- source, PPS routing, and AXI base address are being finalised.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity adsb_pl_wrapper is
    generic (
        NUM_DECODERS        : positive := 8;
        FIFO_DEPTH          : positive := 64;
        ENABLE_DEEP_DEBUG   : boolean  := false;
        C_S_AXI_DATA_WIDTH  : integer  := 32;
        C_S_AXI_ADDR_WIDTH  : integer  := 8
    );
    port (
        -- Sample-domain interface
        sample_clk    : in  std_logic;
        sample_reset  : in  std_logic;
        sample_power  : in  std_logic_vector(INPUT_POWER_WIDTH-1 downto 0);
        sample_valid  : in  std_logic;
        pps_in        : in  std_logic;
        irq           : out std_logic;

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

architecture rtl of adsb_pl_wrapper is
    signal sample_power_signed : signed(INPUT_POWER_WIDTH-1 downto 0);
begin

    sample_power_signed <= signed(sample_power);

    U_core : entity work.adsb_top
        generic map (
            NUM_DECODERS       => NUM_DECODERS,
            FIFO_DEPTH         => FIFO_DEPTH,
            ENABLE_DEEP_DEBUG  => ENABLE_DEEP_DEBUG,
            C_S_AXI_DATA_WIDTH => C_S_AXI_DATA_WIDTH,
            C_S_AXI_ADDR_WIDTH => C_S_AXI_ADDR_WIDTH
        )
        port map (
            clock         => sample_clk,
            reset         => sample_reset,
            in_power      => sample_power_signed,
            in_valid      => sample_valid,
            pps_in        => pps_in,
            raw_power_max      => (others => '0'),
            raw_iq_75pct_count => (others => '0'),
            raw_iq_87p5pct_count => (others => '0'),
            raw_iq_near_rail_count => (others => '0'),
            raw_power_sat_count => (others => '0'),
            raw_power_thresh_count => (others => '0'),
            sample_power_max   => (others => '0'),
            edge_thresh_count  => (others => '0'),
            power_thresh_count => (others => '0'),
            sample_fifo_overflow_count => (others => '0'),
            debug_edge_count   => (others => '0'),
            debug_som_count    => (others => '0'),
            debug_msg_count    => (others => '0'),
            debug_edge_shape_count => (others => '0'),
            debug_edge_qual_count  => (others => '0'),
            debug_preamble_pass_count => (others => '0'),
            debug_preamble_detect_count => (others => '0'),
            debug_preamble_abs_count => (others => '0'),
            debug_preamble_quiet_count => (others => '0'),
            debug_preamble_quiet_a_fail_count => (others => '0'),
            debug_preamble_quiet_b_fail_count => (others => '0'),
            debug_preamble_quiet_c_fail_count => (others => '0'),
            debug_preamble_quiet_d_fail_count => (others => '0'),
            debug_preamble_snr_count => (others => '0'),
            debug_preamble_holdoff_count => (others => '0'),
            debug_preamble_peak_age => (others => '0'),
            agg_valid_count    => (others => '0'),
            fifo_wr_count      => (others => '0'),
            rx_valid_count     => (others => '0'),
            sample_valid_count => (others => '0'),
            rx_clk_count       => (others => '0'),
            soft_reset_toggle_out => open,
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
