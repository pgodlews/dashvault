package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestFanRPM(t *testing.T) {
	c := &Collector{ProcRoot: t.TempDir()}
	if c.readFanRPM() != nil {
		t.Fatal("missing fan must be omitted")
	}
	p := filepath.Join(c.ProcRoot, "sys/devices/platform/cooling_fan/hwmon/hwmon0/fan1_input")
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"2400\n", 2400}, {"0\n", 0}, {"-1", -1}, {"invalid", -1},
	} {
		writeFile(t, p, tc.value)
		got := c.readFanRPM()
		if tc.want < 0 {
			if got != nil {
				t.Fatal("invalid fan value accepted")
			}
		} else if got == nil || *got != tc.want {
			t.Fatal("incorrect fan reading")
		}
	}
}

func TestFirmwareTelemetry(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"EXT5V_V=5.123V\n", true},
		{"EXT5V_V volt(24)=5.123V\n", true},
		{"OTHER=5.123V", false}, {"EXT5V_V=NaNV", false},
		{"EXT5V_V=+InfV", false}, {"EXT5V_V=-1V", false},
		{"EXT5V_V=5.123", false}, {"unsupported", false},
	} {
		c := &Collector{Vcgencmd: func(args ...string) ([]byte, error) {
			if strings.Join(args, " ") != "pmic_read_adc EXT5V_V" {
				t.Fatal("incorrect command")
			}
			return []byte(tc.output), nil
		}}
		got := c.readVoltage("EXT5V_V")
		if (got != nil) != tc.want || (got != nil && *got != 5.123) {
			t.Fatal("incorrect voltage parsing")
		}
	}
	for _, tc := range []struct {
		output string
		want   *uint32
	}{
		{"throttled=0x0\n", uint32Ptr(0)},
		{"throttled=0x50005", uint32Ptr(0x50005)},
		{"throttled=0x100000000", nil}, {"throttled=invalid", nil}, {"unsupported", nil},
	} {
		c := &Collector{Vcgencmd: func(...string) ([]byte, error) { return []byte(tc.output), nil }}
		got := c.readThrottled()
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Fatal("incorrect throttle parsing")
		}
	}
	unsupported := &Collector{Vcgencmd: func(...string) ([]byte, error) { return nil, errors.New("unsupported") }}
	if unsupported.readVoltage("BATT_V") != nil || unsupported.readThrottled() != nil {
		t.Fatal("unsupported metrics must be omitted")
	}
}

func uint32Ptr(v uint32) *uint32 { return &v }

func TestOptionalHardwareJSON(t *testing.T) {
	data, err := json.Marshal(Status{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"fan_rpm", "external_5v", "rtc_battery_v", "throttled"} {
		if strings.Contains(string(data), key) {
			t.Fatal("unsupported hardware field present")
		}
	}
	zero := 0
	data, err = json.Marshal(Status{FanRPM: &zero, Throttled: uint32Ptr(0)})
	if err != nil || !strings.Contains(string(data), `"fan_rpm":0`) || !strings.Contains(string(data), `"throttled":0`) {
		t.Fatal("zero readings must remain available")
	}
}
