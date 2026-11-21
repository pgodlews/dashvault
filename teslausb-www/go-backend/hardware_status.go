package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (c *Collector) readFanRPM() *int {
	paths, _ := filepath.Glob(c.ProcRoot + "/sys/devices/platform/cooling_fan/hwmon/*/fan1_input")
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rpm, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && rpm >= 0 {
			return &rpm
		}
	}
	return nil
}

func (c *Collector) firmwareOutput(args ...string) ([]byte, error) {
	if c.Vcgencmd != nil {
		return c.Vcgencmd(args...)
	}
	if c.ProcRoot != "" {
		return nil, fmt.Errorf("firmware unavailable in fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// The backend service runs as root; no sudo subprocess is necessary.
	return exec.CommandContext(ctx, "vcgencmd", args...).Output()
}

func (c *Collector) readVoltage(channel string) *float64 {
	data, err := c.firmwareOutput("pmic_read_adc", channel)
	if err != nil {
		return nil
	}
	name, value, ok := strings.Cut(strings.TrimSpace(string(data)), "=")
	if !ok || (strings.TrimSpace(name) != channel && !strings.HasPrefix(name, channel+" ")) || !strings.HasSuffix(value, "V") {
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "V")), 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func (c *Collector) readThrottled() *uint32 {
	data, err := c.firmwareOutput("get_throttled")
	if err != nil {
		return nil
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "throttled=0x")
	if !ok {
		return nil
	}
	bits, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return nil
	}
	flags := uint32(bits)
	return &flags
}
