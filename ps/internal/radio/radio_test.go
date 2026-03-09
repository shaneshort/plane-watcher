package radio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupMockSysfs(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	devDir := filepath.Join(base, "iio:device0")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devDir, "name"), []byte("ad9361-phy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

func writeMockAttr(t *testing.T, base, attr, value string) {
	t.Helper()
	devDir := filepath.Join(base, "iio:device0")
	if err := os.WriteFile(filepath.Join(devDir, attr), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindDevice(t *testing.T) {
	base := setupMockSysfs(t)
	dev, err := findDevice(base)
	if err != nil {
		t.Fatalf("findDevice: %v", err)
	}
	want := filepath.Join(base, "iio:device0")
	if dev != want {
		t.Errorf("got %q, want %q", dev, want)
	}
}

func TestFindDeviceNotPresent(t *testing.T) {
	base := t.TempDir()
	_, err := findDevice(base)
	if err == nil {
		t.Fatal("expected error when no device present")
	}
}

func TestTune(t *testing.T) {
	base := setupMockSysfs(t)
	for _, attr := range []string{
		"ensm_mode",
		"out_altvoltage0_RX_LO_frequency",
		"in_voltage_rf_bandwidth",
		"in_voltage0_rf_bandwidth",
		"in_voltage_sampling_frequency",
		"in_voltage0_sampling_frequency",
		"in_voltage0_rf_port_select",
		"in_voltage0_gain_control_mode",
		"in_voltage0_hardwaregain",
	} {
		writeMockAttr(t, base, attr, "0")
	}

	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Tune("54"); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if !r.tunedOK {
		t.Error("tunedOK should be true after successful tune")
	}

	devDir := filepath.Join(base, "iio:device0")
	data, _ := os.ReadFile(filepath.Join(devDir, "out_altvoltage0_RX_LO_frequency"))
	if got := strings.TrimSpace(string(data)); got != "1090000000" {
		t.Errorf("RX_LO = %q, want 1090000000", got)
	}

	data, _ = os.ReadFile(filepath.Join(devDir, "ensm_mode"))
	if got := strings.TrimSpace(string(data)); got != "fdd" {
		t.Errorf("ensm_mode = %q, want fdd", got)
	}

	data, _ = os.ReadFile(filepath.Join(devDir, "in_voltage_sampling_frequency"))
	if got := strings.TrimSpace(string(data)); got != "30720000" {
		t.Errorf("in_voltage_sampling_frequency = %q, want 30720000", got)
	}

	data, _ = os.ReadFile(filepath.Join(devDir, "in_voltage0_sampling_frequency"))
	if got := strings.TrimSpace(string(data)); got != "30720000" {
		t.Errorf("in_voltage0_sampling_frequency = %q, want 30720000", got)
	}

	data, _ = os.ReadFile(filepath.Join(devDir, "in_voltage0_rf_port_select"))
	if got := strings.TrimSpace(string(data)); got != "A_BALANCED" {
		t.Errorf("in_voltage0_rf_port_select = %q, want A_BALANCED", got)
	}
}

func TestSetGainMode(t *testing.T) {
	base := setupMockSysfs(t)
	writeMockAttr(t, base, "in_voltage0_gain_control_mode", "manual")

	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetGainMode("slow_attack"); err != nil {
		t.Fatalf("SetGainMode: %v", err)
	}
	devDir := filepath.Join(base, "iio:device0")
	data, _ := os.ReadFile(filepath.Join(devDir, "in_voltage0_gain_control_mode"))
	if got := strings.TrimSpace(string(data)); got != "slow_attack" {
		t.Errorf("gain_control_mode = %q, want slow_attack", got)
	}
}

func TestSetGainModeInvalid(t *testing.T) {
	base := setupMockSysfs(t)
	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetGainMode("turbo"); err == nil {
		t.Error("expected error for invalid gain mode")
	}
}

func TestSetGain(t *testing.T) {
	base := setupMockSysfs(t)
	writeMockAttr(t, base, "in_voltage0_gain_control_mode", "slow_attack")
	writeMockAttr(t, base, "in_voltage0_hardwaregain", "54")

	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetGain("40"); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	devDir := filepath.Join(base, "iio:device0")
	data, _ := os.ReadFile(filepath.Join(devDir, "in_voltage0_gain_control_mode"))
	if got := strings.TrimSpace(string(data)); got != "manual" {
		t.Errorf("gain_control_mode = %q, want manual", got)
	}
	data, _ = os.ReadFile(filepath.Join(devDir, "in_voltage0_hardwaregain"))
	if got := strings.TrimSpace(string(data)); got != "40" {
		t.Errorf("hardwaregain = %q, want 40", got)
	}
}

func TestReadStatus(t *testing.T) {
	base := setupMockSysfs(t)
	writeMockAttr(t, base, "out_altvoltage0_RX_LO_frequency", "1090000000")
	writeMockAttr(t, base, "in_voltage_rf_bandwidth", "2000000")
	writeMockAttr(t, base, "in_voltage0_hardwaregain", "54.000000 dB")

	r, err := OpenAt(base)
	if err != nil {
		t.Fatal(err)
	}
	s := r.ReadStatus()
	if s.RXLO != "1090000000" {
		t.Errorf("RXLO = %q, want 1090000000", s.RXLO)
	}
	if s.GainDB != "54.000000 dB" {
		t.Errorf("GainDB = %q, want '54.000000 dB'", s.GainDB)
	}
	if s.TunedOK {
		t.Error("TunedOK should be false before Tune()")
	}
}
