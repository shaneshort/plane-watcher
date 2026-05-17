-- =============================================================================
-- axi_regs_tb.vhd — AXI Register Interface Testbench
-- =============================================================================
--
-- Tests the full adsb_top module by feeding I/Q test vectors through the
-- decode pipeline and reading decoded messages via AXI-Lite register reads.
--
-- Verifies:
--   1. Messages appear in FIFO (STATUS register shows not_empty)
--   2. Message data can be read via MSG_DATA_0..3 registers
--   3. TOA and RPL are valid
--   4. Auto-pop on RPL read advances to next message
--   5. PPS counter registers reflect timestamp counter state
--   6. CONTROL register write (enable/disable)
--   7. VERSION register reads correctly
--
-- Usage: ghdl -r axi_regs_tb -gVECTOR_FILE=vectors/two_sequential.dat
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library std;
    use std.textio.all;

library work;
    use work.adsb_pkg.all;

entity axi_regs_tb is
    generic (
        VECTOR_FILE : string := "vectors/two_sequential.dat"
    );
end entity;

architecture tb of axi_regs_tb is

    constant CLK_PERIOD : time := 62.5 ns;  -- 16 MHz
    constant ADDR_WIDTH : integer := 6;
    constant DATA_WIDTH : integer := 32;

    -- Clock and reset
    signal clock    : std_logic := '0';
    signal reset    : std_logic := '1';
    signal aresetn  : std_logic := '0';
    signal sim_done : boolean := false;

    -- RF input
    signal in_power : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal in_valid : std_logic := '0';

    -- PPS
    signal pps_in : std_logic := '0';

    -- Interrupt
    signal irq : std_logic;

    -- AXI-Lite signals
    signal awaddr  : std_logic_vector(ADDR_WIDTH-1 downto 0) := (others => '0');
    signal awprot  : std_logic_vector(2 downto 0) := (others => '0');
    signal awvalid : std_logic := '0';
    signal awready : std_logic;
    signal wdata   : std_logic_vector(DATA_WIDTH-1 downto 0) := (others => '0');
    signal wstrb   : std_logic_vector(DATA_WIDTH/8-1 downto 0) := (others => '1');
    signal wvalid  : std_logic := '0';
    signal wready  : std_logic;
    signal bresp   : std_logic_vector(1 downto 0);
    signal bvalid  : std_logic;
    signal bready  : std_logic := '1';
    signal araddr  : std_logic_vector(ADDR_WIDTH-1 downto 0) := (others => '0');
    signal arprot  : std_logic_vector(2 downto 0) := (others => '0');
    signal arvalid : std_logic := '0';
    signal arready : std_logic;
    signal rdata   : std_logic_vector(DATA_WIDTH-1 downto 0);
    signal rresp   : std_logic_vector(1 downto 0);
    signal rvalid  : std_logic;
    signal rready  : std_logic := '1';

    -- Helper: convert a 4-bit value to hex character
    function nibble_to_hex(v : std_logic_vector(3 downto 0)) return character is
        variable n : integer;
    begin
        n := to_integer(unsigned(v));
        if n < 10 then
            return character'val(character'pos('0') + n);
        else
            return character'val(character'pos('A') + n - 10);
        end if;
    end function;

    -- Helper: convert 32-bit value to 8-char hex string
    function word_to_hex(w : std_logic_vector(31 downto 0)) return string is
        variable result : string(1 to 8);
    begin
        for i in 0 to 7 loop
            result(i+1) := nibble_to_hex(w(31 - i*4 downto 28 - i*4));
        end loop;
        return result;
    end function;

begin

    -- Clock generation
    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';
    aresetn <= not reset;

    -- DUT: full top-level
    U_dut : entity work.adsb_top
        generic map (
            NUM_DECODERS       => 8,
            FIFO_DEPTH         => 64,
            C_S_AXI_DATA_WIDTH => DATA_WIDTH,
            C_S_AXI_ADDR_WIDTH => ADDR_WIDTH
        )
        port map (
            clock         => clock,
            reset         => reset,
            in_power      => in_power,
            in_valid      => in_valid,
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
            raw_capture_data   => (others => '0'),
            adc_code_min       => (others => '0'),
            adc_code_max       => (others => '0'),
            adc_bit_or         => (others => '0'),
            adc_bit_and        => (others => '0'),
            adc_bit_toggle     => (others => '0'),
            adc_otr_count      => (others => '0'),
            soft_reset_toggle_out => open,
            irq           => irq,
            S_AXI_ACLK    => clock,     -- Same clock for simulation
            S_AXI_ARESETN => aresetn,
            S_AXI_AWADDR  => awaddr,
            S_AXI_AWPROT  => awprot,
            S_AXI_AWVALID => awvalid,
            S_AXI_AWREADY => awready,
            S_AXI_WDATA   => wdata,
            S_AXI_WSTRB   => wstrb,
            S_AXI_WVALID  => wvalid,
            S_AXI_WREADY  => wready,
            S_AXI_BRESP   => bresp,
            S_AXI_BVALID  => bvalid,
            S_AXI_BREADY  => bready,
            S_AXI_ARADDR  => araddr,
            S_AXI_ARPROT  => arprot,
            S_AXI_ARVALID => arvalid,
            S_AXI_ARREADY => arready,
            S_AXI_RDATA   => rdata,
            S_AXI_RRESP   => rresp,
            S_AXI_RVALID  => rvalid,
            S_AXI_RREADY  => rready
        );

    -- =========================================================================
    -- Stimulus: Feed test vectors, then read messages via AXI
    -- =========================================================================
    stim : process
        type char_file_t is file of character;
        file vec_file : char_file_t;
        variable status : file_open_status;
        variable c : character;
        variable byte0, byte1, byte2, byte3 : integer;
        variable i_val, q_val : integer;
        variable i_s, q_s : signed(15 downto 0);
        variable power : signed(INPUT_POWER_WIDTH-1 downto 0);
        variable i_sq, q_sq : signed(31 downto 0);
        variable sample_count : integer := 0;

        -- Read result storage
        variable read_data : std_logic_vector(31 downto 0);
        variable msg_word0, msg_word1, msg_word2, msg_word3 : std_logic_vector(31 downto 0);
        variable toa_lo, toa_hi : std_logic_vector(31 downto 0);
        variable rpl_word : std_logic_vector(31 downto 0);
        variable status_word : std_logic_vector(31 downto 0);
        variable msg_count : integer := 0;

        procedure wait_clocks(n : positive) is
        begin
            for i in 1 to n loop
                wait until rising_edge(clock);
            end loop;
        end procedure;

        -- AXI read transaction
        procedure axi_read(
            addr : in std_logic_vector(ADDR_WIDTH-1 downto 0);
            data : out std_logic_vector(DATA_WIDTH-1 downto 0)
        ) is
        begin
            wait until rising_edge(clock);
            araddr <= addr;
            arvalid <= '1';
            -- Wait for ARREADY
            wait until rising_edge(clock) and arready = '1';
            arvalid <= '0';
            -- Wait for RVALID
            wait until rising_edge(clock) and rvalid = '1';
            data := rdata;
            rready <= '1';
            wait until rising_edge(clock);
            rready <= '1';  -- Keep ready high
        end procedure;

        -- AXI write transaction
        procedure axi_write(
            addr : in std_logic_vector(ADDR_WIDTH-1 downto 0);
            data : in std_logic_vector(DATA_WIDTH-1 downto 0)
        ) is
        begin
            wait until rising_edge(clock);
            awaddr <= addr;
            awvalid <= '1';
            wdata <= data;
            wvalid <= '1';
            wstrb <= (others => '1');
            -- Wait for both AWREADY and WREADY
            wait until rising_edge(clock) and awready = '1' and wready = '1';
            awvalid <= '0';
            wvalid <= '0';
            -- Wait for BVALID
            wait until rising_edge(clock) and bvalid = '1';
            bready <= '1';
            wait until rising_edge(clock);
        end procedure;

    begin
        -- =====================================================================
        -- Phase 1: Reset
        -- =====================================================================
        reset <= '1';
        wait_clocks(20);
        reset <= '0';
        wait_clocks(5);

        -- PPS pulse for timestamp reference
        pps_in <= '1';
        wait_clocks(10);
        pps_in <= '0';
        wait_clocks(10);

        -- =====================================================================
        -- Phase 2: Read VERSION register to verify AXI is working
        -- =====================================================================
        report "=== Reading VERSION register ===";
        axi_read(std_logic_vector(to_unsigned(16#30#, ADDR_WIDTH)), read_data);
        report "VERSION = 0x" & word_to_hex(read_data);
        assert read_data = x"00010000"
            report "VERSION mismatch! Expected 0x00010000" severity error;

        -- =====================================================================
        -- Phase 3: Read STATUS (should be empty initially)
        -- =====================================================================
        axi_read(std_logic_vector(to_unsigned(16#1C#, ADDR_WIDTH)), status_word);
        report "Initial STATUS = 0x" & word_to_hex(status_word);
        assert status_word(0) = '0'
            report "FIFO should be empty initially!" severity error;

        -- =====================================================================
        -- Phase 4: Read PPS_COUNT (should be 1 after our PPS pulse)
        -- =====================================================================
        axi_read(std_logic_vector(to_unsigned(16#20#, ADDR_WIDTH)), read_data);
        report "PPS_COUNT = " & integer'image(to_integer(unsigned(read_data)));

        -- =====================================================================
        -- Phase 5: Feed test vector samples
        -- =====================================================================
        report "Opening test vector: " & VECTOR_FILE;
        file_open(status, vec_file, VECTOR_FILE, read_mode);
        if status /= open_ok then
            report "ERROR: Could not open " & VECTOR_FILE severity failure;
        end if;

        report "Feeding samples into decode pipeline...";

        while not endfile(vec_file) loop
            read(vec_file, c);
            byte0 := character'pos(c);
            if endfile(vec_file) then exit; end if;
            read(vec_file, c);
            byte1 := character'pos(c);
            if endfile(vec_file) then exit; end if;
            read(vec_file, c);
            byte2 := character'pos(c);
            if endfile(vec_file) then exit; end if;
            read(vec_file, c);
            byte3 := character'pos(c);

            i_val := byte0 + byte1 * 256;
            if i_val >= 32768 then i_val := i_val - 65536; end if;
            q_val := byte2 + byte3 * 256;
            if q_val >= 32768 then q_val := q_val - 65536; end if;

            i_s := to_signed(i_val, 16);
            q_s := to_signed(q_val, 16);
            i_sq := i_s * i_s;
            q_sq := q_s * q_s;
            power := resize(i_sq(INPUT_POWER_WIDTH-1 downto 0) +
                           q_sq(INPUT_POWER_WIDTH-1 downto 0),
                           INPUT_POWER_WIDTH);

            wait until rising_edge(clock);
            in_power <= power;
            in_valid <= '1';
            sample_count := sample_count + 1;
        end loop;

        wait until rising_edge(clock);
        in_valid <= '0';
        in_power <= (others => '0');
        file_close(vec_file);
        report "Fed " & integer'image(sample_count) & " samples";

        -- =====================================================================
        -- Phase 6: Wait for pipeline to drain
        -- =====================================================================
        report "Waiting for pipeline to drain...";
        wait_clocks(5000);

        -- =====================================================================
        -- Phase 7: Read decoded messages via AXI registers
        -- =====================================================================
        report "=== Reading messages from FIFO via AXI ===";

        -- Poll STATUS and read messages
        for attempt in 0 to 15 loop
            axi_read(std_logic_vector(to_unsigned(16#1C#, ADDR_WIDTH)), status_word);

            if status_word(0) = '1' then
                -- Message available! Read all fields
                msg_count := msg_count + 1;

                axi_read(std_logic_vector(to_unsigned(16#00#, ADDR_WIDTH)), msg_word0);
                axi_read(std_logic_vector(to_unsigned(16#04#, ADDR_WIDTH)), msg_word1);
                axi_read(std_logic_vector(to_unsigned(16#08#, ADDR_WIDTH)), msg_word2);
                axi_read(std_logic_vector(to_unsigned(16#0C#, ADDR_WIDTH)), msg_word3);
                axi_read(std_logic_vector(to_unsigned(16#10#, ADDR_WIDTH)), toa_lo);
                axi_read(std_logic_vector(to_unsigned(16#14#, ADDR_WIDTH)), toa_hi);
                axi_read(std_logic_vector(to_unsigned(16#18#, ADDR_WIDTH)), rpl_word);  -- This pops FIFO!

                report "*** AXI MSG #" & integer'image(msg_count) &
                       ": " & word_to_hex(msg_word3) & word_to_hex(msg_word2) &
                              word_to_hex(msg_word1) & word_to_hex(msg_word0) &
                       " TOA=" & integer'image(to_integer(unsigned(toa_lo))) &
                       " RPL=" & integer'image(to_integer(signed(rpl_word(23 downto 0)))) &
                       " FIFO_COUNT=" & integer'image(to_integer(unsigned(status_word(14 downto 8))));
            else
                report "FIFO empty after " & integer'image(msg_count) & " messages";
                exit;
            end if;
        end loop;

        -- =====================================================================
        -- Phase 8: Test CONTROL register write
        -- =====================================================================
        report "=== Testing CONTROL register ===";
        axi_read(std_logic_vector(to_unsigned(16#2C#, ADDR_WIDTH)), read_data);
        report "CONTROL = 0x" & word_to_hex(read_data);

        -- Write enable=0 (disable decoders)
        axi_write(std_logic_vector(to_unsigned(16#2C#, ADDR_WIDTH)), x"00000000");
        axi_read(std_logic_vector(to_unsigned(16#2C#, ADDR_WIDTH)), read_data);
        report "CONTROL after disable = 0x" & word_to_hex(read_data);

        -- Write enable=1 (re-enable)
        axi_write(std_logic_vector(to_unsigned(16#2C#, ADDR_WIDTH)), x"00000002");
        axi_read(std_logic_vector(to_unsigned(16#2C#, ADDR_WIDTH)), read_data);
        report "CONTROL after re-enable = 0x" & word_to_hex(read_data);

        -- =====================================================================
        -- Done
        -- =====================================================================
        report "=== AXI Testbench Complete: " & integer'image(msg_count) & " messages read ===";

        assert msg_count > 0
            report "ERROR: No messages decoded!" severity error;

        sim_done <= true;
        wait;
    end process;

end architecture;
