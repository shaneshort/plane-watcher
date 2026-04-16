-- =============================================================================
-- ad9238_ingress.vhd -- Parallel CMOS capture for AD9238 dual 12-bit ADC
-- =============================================================================
--
-- Latches the two 12-bit parallel CMOS data buses from an AD9238-65 ADC.
-- The FPGA drives the encode clock via a fabric PLL, so the data outputs
-- are synchronous to the fabric clock domain — no CDC is required.
--
-- The AD9238 data outputs transition on the falling edge of ENCODE and are
-- stable well before the next rising edge (t_OD < 5.8 ns at 65 MSPS, period
-- 15.4 ns). A single input register stage is sufficient at this rate.
--
-- Both channels are captured. The downstream pipeline selects which channel
-- to use; the other is available for PS-side diagnostics or future use.
--
-- Out-of-range (OTR) flags are captured per channel and exposed as outputs
-- for debug monitoring.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

entity ad9238_ingress is
    generic (
        ADC_WIDTH : positive := 12
    );
    port (
        clock     : in  std_logic;
        reset     : in  std_logic;

        -- AD9238 channel A (active decode channel)
        adc_data_a : in  std_logic_vector(ADC_WIDTH-1 downto 0);
        adc_otr_a  : in  std_logic;

        -- AD9238 channel B (passthrough for PS)
        adc_data_b : in  std_logic_vector(ADC_WIDTH-1 downto 0);
        adc_otr_b  : in  std_logic;

        -- Registered outputs — channel A
        out_data_a : out unsigned(ADC_WIDTH-1 downto 0);
        out_otr_a  : out std_logic;
        out_valid  : out std_logic;

        -- Registered outputs — channel B
        out_data_b : out unsigned(ADC_WIDTH-1 downto 0);
        out_otr_b  : out std_logic
    );
end entity;

architecture rtl of ad9238_ingress is

    -- Input register stage. The AD9238 outputs are stable on the rising edge
    -- of the encode clock (which is this fabric clock). One register stage
    -- gives a clean timing boundary for downstream logic.
    signal data_a_r : std_logic_vector(ADC_WIDTH-1 downto 0) := (others => '0');
    signal data_b_r : std_logic_vector(ADC_WIDTH-1 downto 0) := (others => '0');
    signal otr_a_r  : std_logic := '0';
    signal otr_b_r  : std_logic := '0';

    -- Valid flag. Asserted one cycle after reset deasserts, stays high
    -- continuously — the ADC produces a sample every clock cycle.
    signal valid_r  : std_logic := '0';

begin

    process(clock, reset)
    begin
        if reset = '1' then
            data_a_r <= (others => '0');
            data_b_r <= (others => '0');
            otr_a_r  <= '0';
            otr_b_r  <= '0';
            valid_r  <= '0';
        elsif rising_edge(clock) then
            data_a_r <= adc_data_a;
            data_b_r <= adc_data_b;
            otr_a_r  <= adc_otr_a;
            otr_b_r  <= adc_otr_b;
            valid_r  <= '1';
        end if;
    end process;

    -- The AD9238 outputs unsigned codes: 0x000 = negative full-scale,
    -- 0x800 = mid-scale (zero), 0xFFF = positive full-scale.
    -- Expose as unsigned and let the downstream LUT handle interpretation.
    out_data_a <= unsigned(data_a_r);
    out_data_b <= unsigned(data_b_r);
    out_otr_a  <= otr_a_r;
    out_otr_b  <= otr_b_r;
    out_valid  <= valid_r;

end architecture;
