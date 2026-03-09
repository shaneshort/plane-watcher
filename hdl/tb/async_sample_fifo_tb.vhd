library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library std;
    use std.env.all;

library work;
    use work.adsb_pkg.all;

entity async_sample_fifo_tb is
end entity;

architecture tb of async_sample_fifo_tb is
    constant WR_CLK_PERIOD : time := 16 ns;
    constant RD_CLK_PERIOD : time := 10 ns;
    constant FIFO_DEPTH    : positive := 16;
    constant EXPECTED_COUNT : natural := 12;

    type sample_list_t is array (0 to EXPECTED_COUNT - 1) of integer;
    constant EXPECTED_SAMPLES : sample_list_t := (
        11, 22, 33, 44, 55, 66, 77, 88,
        101, 202, 303, 404
    );

    signal wr_clock : std_logic := '0';
    signal wr_reset : std_logic := '1';
    signal wr_data  : signed(INPUT_POWER_WIDTH-1 downto 0) := (others => '0');
    signal wr_en    : std_logic := '0';
    signal full     : std_logic;
    signal overflow : std_logic;

    signal rd_clock : std_logic := '0';
    signal rd_reset : std_logic := '1';
    signal rd_data  : signed(INPUT_POWER_WIDTH-1 downto 0);
    signal rd_en    : std_logic;
    signal empty    : std_logic;

    signal read_count : natural := 0;
begin

    U_fifo : entity work.async_sample_fifo
        generic map (
            WIDTH => INPUT_POWER_WIDTH,
            DEPTH => FIFO_DEPTH
        )
        port map (
            wr_clock => wr_clock,
            wr_reset => wr_reset,
            wr_data  => wr_data,
            wr_en    => wr_en,
            full     => full,
            overflow => overflow,
            rd_clock => rd_clock,
            rd_reset => rd_reset,
            rd_data  => rd_data,
            rd_en    => rd_en,
            empty    => empty
        );

    wr_clock <= not wr_clock after WR_CLK_PERIOD / 2;

    rd_clock_gen : process
    begin
        wait for 3 ns;
        while true loop
            rd_clock <= '1';
            wait for RD_CLK_PERIOD / 2;
            rd_clock <= '0';
            wait for RD_CLK_PERIOD / 2;
        end loop;
    end process;

    rd_en <= '1' when (rd_reset = '0' and empty = '0') else '0';

    stimulus : process
        procedure push_sample(value : integer; idle_cycles_after : natural := 0) is
        begin
            wr_data <= to_signed(value, INPUT_POWER_WIDTH);
            wr_en <= '1';
            wait until rising_edge(wr_clock);
            wr_en <= '0';
            for i in 1 to idle_cycles_after loop
                wait until rising_edge(wr_clock);
            end loop;
        end procedure;
    begin
        wait until rising_edge(wr_clock);
        wait until rising_edge(rd_clock);
        wr_reset <= '0';
        rd_reset <= '0';

        -- Phase A: normal ordered transfer with bursty writes.
        push_sample(11, 0);
        push_sample(22, 1);
        push_sample(33, 0);
        push_sample(44, 2);
        push_sample(55, 0);
        push_sample(66, 0);
        push_sample(77, 1);
        push_sample(88, 0);

        wait until read_count = 8;

        -- Phase B: queue junk while the read side is held in reset, then
        -- reset the write side to prove the FIFO flushes stale entries.
        rd_reset <= '1';
        push_sample(901, 0);
        push_sample(902, 0);
        push_sample(903, 1);
        push_sample(904, 0);

        wait until rising_edge(wr_clock);
        wr_reset <= '1';
        wait until rising_edge(wr_clock);
        wr_reset <= '0';

        wait until rising_edge(rd_clock);
        wait until rising_edge(rd_clock);
        rd_reset <= '0';

        -- Phase C: only post-reset samples should be observed.
        push_sample(101, 0);
        push_sample(202, 0);
        push_sample(303, 1);
        push_sample(404, 0);

        wait until read_count = EXPECTED_COUNT;
        wait until rising_edge(rd_clock);

        assert overflow = '0'
            report "sample FIFO overflowed during test"
            severity failure;

        report "async_sample_fifo_tb passed" severity note;
        stop;
        wait;
    end process;

    checker : process(rd_clock)
    begin
        if rising_edge(rd_clock) then
            if rd_reset = '0' and rd_en = '1' and empty = '0' then
                assert rd_data = to_signed(EXPECTED_SAMPLES(read_count), INPUT_POWER_WIDTH)
                    report "unexpected FIFO output at index " & integer'image(read_count)
                    severity failure;
                read_count <= read_count + 1;
            end if;
        end if;
    end process;

end architecture;
