"""
RP2040 16 MHz clock generator via PIO.

System clock is set to 128 MHz so the PIO state machine's 8-cycle loop
(4 cycles high + 4 cycles low) divides exactly to 16 MHz with zero
fractional-divider jitter. Accuracy is bounded by the onboard 12 MHz
crystal (typically +/- 50 ppm).

Output: GP0, exposed as pad D6 on the Seeed XIAO RP2040.
Change OUT_PIN below if you want a different pin (XIAO breaks out
GP0/1/2/3/4/6/7/26/27/28/29 as D6/D7/D8/D10/D9/D4/D5/D0/D1/D2/D3).
"""

import machine
import rp2

OUT_PIN = 0
SYS_CLK_HZ = 128_000_000   # 12 MHz xtal * 32/3 -> 128 MHz, divides evenly to 16 MHz

# XIAO RP2040 onboard user LED is the RGB anode-common cluster, active-low.
# GP17 = red, GP16 = green, GP25 = blue. Pick one for status.
LED_PIN = 17
LED_HZ = 2


# Set system clock first so the StateMachine sees the right frequency.
machine.freq(SYS_CLK_HZ)


@rp2.asm_pio(set_init=rp2.PIO.OUT_LOW)
def clock_16mhz():
    # Each instruction is 1 PIO cycle plus its delay slots.
    # 8 cycles total per wrap -> 128 MHz / 8 = 16 MHz.
    set(pins, 1) [3]   # noqa: F821 -- 1 cycle high + 3 delay = 4 cycles high
    set(pins, 0) [3]   # noqa: F821 -- 1 cycle low  + 3 delay = 4 cycles low


sm = rp2.StateMachine(
    0,
    clock_16mhz,
    freq=SYS_CLK_HZ,
    set_base=machine.Pin(OUT_PIN),
)
sm.active(1)

# Status LED: hardware Timer toggles the pin in the background so the main
# thread (and any future REPL interaction) stays free.
led = machine.Pin(LED_PIN, machine.Pin.OUT, value=1)  # active-low: 1 = off

def _toggle_led(_t):
    led.value(not led.value())

machine.Timer().init(
    freq=LED_HZ * 2,                  # toggle twice per blink period
    mode=machine.Timer.PERIODIC,
    callback=_toggle_led,
)

print(
    f"16 MHz clock active on GP{OUT_PIN} (sys_clk = {SYS_CLK_HZ/1e6:.0f} MHz); "
    f"status LED on GP{LED_PIN} @ {LED_HZ} Hz"
)
