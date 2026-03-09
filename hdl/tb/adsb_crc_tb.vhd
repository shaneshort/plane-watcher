library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity adsb_crc_tb is
end entity;

architecture tb of adsb_crc_tb is
    constant CLK_PERIOD : time := 62.5 ns;
    signal clock      : std_logic := '0';
    signal reset      : std_logic := '1';
    signal data       : std_logic_vector(111 downto 0) := (others => '0');
    signal data_valid : std_logic := '0';
    signal busy       : std_logic;
    signal crc        : std_logic_vector(23 downto 0);
    signal crc_good   : std_logic;
    signal crc_valid  : std_logic;
    signal sim_done : boolean := false;

    -- Known good DF17 message.
    -- Mode-S byte order: 8D75804B580FF2CF7E9BA6F701D0
    -- FPGA/swizzled byte order: D001F7A69B7ECFF20F584B80758D
    constant GOOD_MSG_SWIZZLED : std_logic_vector(111 downto 0) :=
        112x"d001f7a69b7ecff20f584b80758d";
    constant GOOD_MSG_MODE_S : std_logic_vector(111 downto 0) :=
        112x"8d75804b580ff2cf7e9ba6f701d0";

    function reverse_bits8(x : std_logic_vector(7 downto 0)) return std_logic_vector is
        variable rv : std_logic_vector(7 downto 0);
    begin
        for i in 0 to 7 loop
            rv(i) := x(7 - i);
        end loop;
        return rv;
    end function;

    function reverse_bits_per_byte(x : std_logic_vector(111 downto 0)) return std_logic_vector is
        variable rv : std_logic_vector(111 downto 0);
    begin
        for i in 0 to 13 loop
            rv(111 - i*8 downto 104 - i*8) :=
                reverse_bits8(x(111 - i*8 downto 104 - i*8));
        end loop;
        return rv;
    end function;

    function reverse_bytes(x : std_logic_vector(111 downto 0)) return std_logic_vector is
        variable rv : std_logic_vector(111 downto 0);
    begin
        for i in 0 to 13 loop
            rv(111 - i*8 downto 104 - i*8) :=
                x(111 - (13 - i)*8 downto 104 - (13 - i)*8);
        end loop;
        return rv;
    end function;
begin
    clock <= not clock after CLK_PERIOD / 2 when not sim_done else '0';

    UUT: entity work.adsb_crc
        port map (
            clock      => clock,
            reset      => reset,
            busy       => busy,
            data       => data,
            data_valid => data_valid,
            crc        => crc,
            crc_good   => crc_good,
            crc_valid  => crc_valid
        );

    stim: process
        procedure wait_clocks(n : positive) is
        begin
            for i in 1 to n loop
                wait until rising_edge(clock);
            end loop;
        end procedure;
    begin
        reset <= '1';
        wait_clocks(10);
        reset <= '0';
        wait_clocks(5);

        -- Test 1: Known good message in swizzled/FPGA order
        report "Test 1: Known good swizzled/FPGA-order message";
        data       <= GOOD_MSG_SWIZZLED;
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        assert crc_good = '1'
            report "CRC should pass for known good swizzled message"
            severity failure;
        report "Test 1: PASSED";
        wait_clocks(5);

        -- Test 2: Same message in standard Mode-S order
        report "Test 2: Known good Mode-S-order message";
        data       <= GOOD_MSG_MODE_S;
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        report "Test 2 result: crc_good=" & std_logic'image(crc_good) severity note;
        wait_clocks(5);

        -- Test 3: Mode-S order with bits reversed inside each byte
        report "Test 3: Mode-S-order message with bits reversed in each byte";
        data       <= reverse_bits_per_byte(GOOD_MSG_MODE_S);
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        report "Test 3 result: crc_good=" & std_logic'image(crc_good) severity note;
        wait_clocks(5);

        -- Test 4: Swizzled order with bits reversed inside each byte
        report "Test 4: Swizzled-order message with bits reversed in each byte";
        data       <= reverse_bits_per_byte(GOOD_MSG_SWIZZLED);
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        report "Test 4 result: crc_good=" & std_logic'image(crc_good) severity note;
        wait_clocks(5);

        -- Test 5: Corrupted message (flip bit 1) in the known-good CRC order
        report "Test 5: Corrupted swizzled message";
        data       <= GOOD_MSG_SWIZZLED xor (111x"0" & '1');
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        assert crc_good = '0'
            report "CRC should fail for corrupted message"
            severity failure;
        report "Test 5: PASSED";
        wait_clocks(5);

        -- Test 6: All-zero message
        report "Test 6: All-zero message";
        data       <= (others => '0');
        data_valid <= '1';
        wait_clocks(1);
        data_valid <= '0';
        wait until crc_valid = '1' for 200 * CLK_PERIOD;
        assert crc_valid = '1'
            report "CRC did not complete in time"
            severity failure;
        assert crc_good = '0'
            report "CRC should fail for all-zero message"
            severity failure;
        report "Test 6: PASSED";

        report "== All adsb_crc tests PASSED ==" severity note;
        sim_done <= true;
        wait;
    end process;
end architecture;
