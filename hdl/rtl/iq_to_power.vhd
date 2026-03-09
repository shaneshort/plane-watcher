-- =============================================================================
-- iq_to_power.vhd -- Convert signed I/Q samples to scalar power magnitude
-- =============================================================================
--
-- First-pass ingress adapter for the vendor AD936x receive path. This keeps
-- the decoder fed with a scalar non-negative signal without changing the
-- decoder core to consume complex samples directly.
--
-- Important: this must match the testbench/reference path, which computes
-- I^2 + Q^2 from the signed I/Q samples. An earlier abs(I)+abs(Q) shortcut
-- produced a very different waveform and broke live-hardware decoding while
-- the offline sim path still worked.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

library work;
    use work.adsb_pkg.all;

entity iq_to_power is
    generic (
        IQ_WIDTH : positive := 16
    );
    port (
        clock     : in  std_logic;
        reset     : in  std_logic;
        in_i      : in  signed(IQ_WIDTH-1 downto 0);
        in_q      : in  signed(IQ_WIDTH-1 downto 0);
        in_valid  : in  std_logic;
        out_power : out signed(INPUT_POWER_WIDTH-1 downto 0);
        out_valid : out std_logic
    );
end entity;

architecture rtl of iq_to_power is
    -- AD9361 delivers 12-bit ADC samples packed into 16-bit transport words.
    -- Only square the real 12-bit payload; spending timing on the replicated
    -- sign-extension bits buys nothing and was the exact reg->DSP path failing
    -- on rx_clk.
    constant ADC_WIDTH : positive := 12;

    signal i_in_r     : signed(ADC_WIDTH-1 downto 0) := (others => '0');
    signal q_in_r     : signed(ADC_WIDTH-1 downto 0) := (others => '0');
    signal i_sq_r     : signed((ADC_WIDTH * 2) - 1 downto 0) := (others => '0');
    signal q_sq_r     : signed((ADC_WIDTH * 2) - 1 downto 0) := (others => '0');
    signal valid_s0   : std_logic := '0';
    signal valid_s1   : std_logic := '0';
begin

    -- Stage 0: register the incoming I/Q samples so the DSP squarers do not
    -- sit directly on the AD9361 receive flops.
    stage0 : process(clock, reset)
    begin
        if reset = '1' then
            i_in_r   <= (others => '0');
            q_in_r   <= (others => '0');
            valid_s0 <= '0';
        elsif rising_edge(clock) then
            valid_s0 <= in_valid;
            if in_valid = '1' then
                i_in_r <= resize(in_i, ADC_WIDTH);
                q_in_r <= resize(in_q, ADC_WIDTH);
            end if;
        end if;
    end process;

    -- Stage 1: square each signed component
    stage1 : process(clock, reset)
        variable i_mul : signed((ADC_WIDTH * 2) - 1 downto 0);
        variable q_mul : signed((ADC_WIDTH * 2) - 1 downto 0);
    begin
        if reset = '1' then
            i_sq_r   <= (others => '0');
            q_sq_r   <= (others => '0');
            valid_s1 <= '0';
        elsif rising_edge(clock) then
            valid_s1 <= valid_s0;

            if valid_s0 = '1' then
                i_mul := i_in_r * i_in_r;
                q_mul := q_in_r * q_in_r;
                i_sq_r <= i_mul;
                q_sq_r <= q_mul;
            end if;
        end if;
    end process;

    -- Stage 2: add the squared terms and scale down to fit the signed 24-bit
    -- decoder power path.
    --
    -- Using the real 12-bit ADC payload, I^2 + Q^2 can still hit 8,388,608
    -- in the worst case, which is one count above the positive range of a
    -- signed 24-bit value. Shift right by 2 so the scalar power path stays
    -- strictly non-negative without wraparound and matches the decoder's
    -- threshold assumptions.
    stage2 : process(clock, reset)
        variable sum_sq : signed((ADC_WIDTH * 2) downto 0);
        variable scaled : signed((ADC_WIDTH * 2) downto 0);
    begin
        if reset = '1' then
            out_power <= (others => '0');
            out_valid <= '0';
        elsif rising_edge(clock) then
            out_valid <= valid_s1;

            if valid_s1 = '1' then
                sum_sq := resize(i_sq_r, sum_sq'length) + resize(q_sq_r, sum_sq'length);
                scaled := shift_right(sum_sq, 2);
                out_power <= resize(scaled, INPUT_POWER_WIDTH);
            else
                out_power <= (others => '0');
            end if;
        end if;
    end process;

end architecture;
