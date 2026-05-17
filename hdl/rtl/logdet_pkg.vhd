-- =============================================================================
-- logdet_pkg.vhd -- Log-detector frontend parameters
-- =============================================================================
--
-- Holds constants specific to the log-detector RF chain
-- (ADL5513 -> AD8138 -> AD9203 -> FPGA). Separate from adsb_pkg.vhd so the
-- frontend-specific parameters (ADC clock rate, channel wiring) stay with
-- the frontend and the decode-core package keeps its focus on pipeline
-- timing, thresholds, and sample-width definitions.
-- =============================================================================

library ieee;
    use ieee.std_logic_1164.all;
    use ieee.numeric_std.all;

package logdet_pkg is

    -- ADC output width from the AD9203 prototype frontend.
    constant LOGDET_ADC_WIDTH : positive := 10;

    -- ADC encode clock rate driven by the fabric MMCM onto the AD9203 CLK pin.
    --
    -- The AD9203 is a 40 MSPS converter. The default remains 16 MHz for
    -- conservative board bring-up; raise toward 40 MHz after SI and decode
    -- timing are validated on the prototype board.
    --
    -- Changing this value requires regenerating the Clocking Wizard IP
    -- instance that produces the encode clock — the MMCM configuration is
    -- baked into the IP, not read from this package at elaboration time.
    constant ENCODE_CLK_HZ : integer := 16_000_000;

end package;
