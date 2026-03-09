package regs

import "math"

const (
	adcPayloadBits       = 12
	adcFullScale         = 1 << (adcPayloadBits - 1)   // 2048
	samplePowerFullScale = (2 * adcFullScale * adcFullScale) >> 2

	// RplFullScale matches the current RTL path:
	//   iq_to_power:        (I^2 + Q^2) >> 2
	//   preamble_detector:  (sum0 + sum1 + sum2 + sum3) >> 4
	// where each pulse window sum spans 5 samples.
	RplFullScale = (4 * 5 * samplePowerFullScale) >> 4 // 2,621,440
)

// RplToSignalByte maps the current 24-bit PL RPL field to the conventional
// 0-255 Beast-style signal byte. RPL is already a power-like quantity, so use
// a sqrt mapping to get an amplitude-like byte for downstream consumers.
func RplToSignalByte(rpl uint32) byte {
	if rpl == 0 {
		return 0
	}

	if rpl > RplFullScale {
		rpl = RplFullScale
	}

	signal := uint32(math.Sqrt(float64(rpl)/float64(RplFullScale)) * 255.0)
	if signal == 0 {
		signal = 1
	}
	if signal > 255 {
		signal = 255
	}
	return byte(signal)
}
