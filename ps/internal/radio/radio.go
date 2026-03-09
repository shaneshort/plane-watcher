package radio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSysfsBase = "/sys/bus/iio/devices"
	deviceName       = "ad9361-phy"

	rxLOFreq   = "1090000000"
	rxBW       = "2000000"
	sampleRate = "30720000"
	rxPort     = "A_BALANCED"
)

var validGainModes = map[string]bool{
	"manual":      true,
	"slow_attack": true,
	"fast_attack": true,
	"hybrid":      true,
}

type Status struct {
	RXLO       string `json:"rx_lo"`
	RXBW       string `json:"rx_bw"`
	SampleRate string `json:"sample_rate"`
	GainMode   string `json:"gain_mode"`
	GainDB     string `json:"gain_db"`
	RSSI       string `json:"rssi"`
	RXPort     string `json:"rx_port"`
	TunedOK    bool   `json:"tuned_ok"`
}

type Radio struct {
	devPath string
	tunedOK bool
}

func findDevice(baseDir string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", fmt.Errorf("scan IIO devices: %w", err)
	}
	for _, e := range entries {
		// sysfs entries are symlinks, not real directories
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		nameFile := filepath.Join(baseDir, e.Name(), "name")
		data, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == deviceName {
			return filepath.Join(baseDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no IIO device with name %q found in %s", deviceName, baseDir)
}

func Open() (*Radio, error) {
	return OpenAt(defaultSysfsBase)
}

func OpenAt(baseDir string) (*Radio, error) {
	devPath, err := findDevice(baseDir)
	if err != nil {
		return nil, err
	}
	return &Radio{devPath: devPath}, nil
}

func (r *Radio) write(attr, value string) error {
	return os.WriteFile(filepath.Join(r.devPath, attr), []byte(value), 0o644)
}

func (r *Radio) writeAny(attrs []string, value string) error {
	var (
		lastErr error
		wrote   bool
	)
	for _, attr := range attrs {
		if err := r.write(attr, value); err != nil {
			lastErr = err
		} else {
			wrote = true
		}
	}
	if wrote {
		return nil
	}
	return lastErr
}

func (r *Radio) read(attrs ...string) string {
	for _, attr := range attrs {
		data, err := os.ReadFile(filepath.Join(r.devPath, attr))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// Tune sets the radio to 1090 MHz ADS-B receive parameters.
func (r *Radio) Tune(gainDB string) error {
	settings := []struct {
		attrs []string
		value string
	}{
		{[]string{"ensm_mode"}, "fdd"},
		{[]string{"out_altvoltage0_RX_LO_frequency"}, rxLOFreq},
		{[]string{"in_voltage_rf_bandwidth", "in_voltage0_rf_bandwidth"}, rxBW},
		{[]string{"in_voltage_sampling_frequency", "in_voltage0_sampling_frequency"}, sampleRate},
		{[]string{"in_voltage0_rf_port_select"}, rxPort},
		{[]string{"in_voltage0_gain_control_mode"}, "manual"},
		{[]string{"in_voltage0_hardwaregain"}, gainDB},
	}
	for _, s := range settings {
		if err := r.writeAny(s.attrs, s.value); err != nil {
			return fmt.Errorf("tune %s=%s: %w", strings.Join(s.attrs, "|"), s.value, err)
		}
	}
	r.tunedOK = true
	return nil
}

// SetGainMode changes the AD9361 RX gain control mode.
func (r *Radio) SetGainMode(mode string) error {
	if !validGainModes[mode] {
		return fmt.Errorf("invalid gain mode %q (valid: manual, slow_attack, fast_attack, hybrid)", mode)
	}
	return r.write("in_voltage0_gain_control_mode", mode)
}

// SetGain sets the AD9361 to manual gain mode and applies the given gain in dB.
func (r *Radio) SetGain(gainDB string) error {
	if err := r.write("in_voltage0_gain_control_mode", "manual"); err != nil {
		return fmt.Errorf("set manual mode: %w", err)
	}
	return r.write("in_voltage0_hardwaregain", gainDB)
}

func (r *Radio) ReadStatus() Status {
	return Status{
		RXLO:       r.read("out_altvoltage0_RX_LO_frequency"),
		RXBW:       r.read("in_voltage_rf_bandwidth", "in_voltage0_rf_bandwidth"),
		SampleRate: r.read("in_voltage_sampling_frequency", "in_voltage0_sampling_frequency"),
		GainMode:   r.read("in_voltage0_gain_control_mode"),
		GainDB:     r.read("in_voltage0_hardwaregain"),
		RSSI:       r.read("in_voltage0_rssi"),
		RXPort:     r.read("in_voltage0_rf_port_select"),
		TunedOK:    r.tunedOK,
	}
}
