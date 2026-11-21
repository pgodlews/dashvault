#!/usr/bin/env python3
"""
FNIRSI FNB58 / FNB48 / FNB38 BLE Power & Throughput Benchmark Tool for TeslaUSB.

Connects to a Bluetooth LE USB power meter (using the RFstar UART BLE protocol from
https://github.com/pgodlews/fnirsi-fnb58-web-monitor) and coordinates remote SBC
workloads over SSH to measure exact power draw (Watts / mA / Volts) alongside:
  1. Idle baseline power
  2. Synchronous 160 MB SD card write benchmark (/root/bin/benchmark.sh)
  3. Outbound 100 MB Wi-Fi TCP transfer (run separately from disk writes)

Can also be used in standalone mode (--record-only) to log live BLE power traces to CSV.
"""

from __future__ import annotations

import argparse
import asyncio
import csv
import json
import shlex
import statistics
import struct
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

CHAR_NOTIFY = "0000ffe4-0000-1000-8000-00805f9b34fb"
CHAR_WRITE = "0000ffe9-0000-1000-8000-00805f9b34fb"
PKT_LENS = {0x03: 14, 0x04: 12, 0x05: 7, 0x06: 6, 0x07: 4, 0x08: 17}


def crc16xmodem(data: bytes) -> int:
    crc = 0
    for b in data:
        for i in range(8):
            bit = (b >> (7 - i)) & 1
            msb = (crc >> 15) & 1
            crc = (crc << 1) & 0xFFFF
            if bit ^ msb:
                crc ^= 0x1021
    return crc


def make_cmd(cmd_byte: int, payload: bytes = b"") -> bytes:
    inner = bytes([0xAA, cmd_byte, len(payload)]) + payload
    crc = crc16xmodem(inner)
    return inner + bytes([crc & 0xFF])


def build_remote_script(
    *,
    runs: int,
    idle_seconds: float,
    wifi_sink_host: str | None,
    wifi_sink_port: int,
    wifi_size_mb: int,
) -> str:
    wifi_enabled = "true" if wifi_sink_host else "false"
    sink_host = wifi_sink_host or "127.0.0.1"
    return f"""
set -euo pipefail

ORIG_GOV=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo conservative)
ORIG_MIN_FREQ=$(cat /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_min_freq 2>/dev/null || echo 600000)
ORIG_MAX_FREQ=$(cat /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq 2>/dev/null || echo 1800000)
ORIG_CORES=$(nproc --all 2>/dev/null || echo 1)
IW_BIN=$(command -v iw 2>/dev/null || ([ -x /sbin/iw ] && echo /sbin/iw) || ([ -x /usr/sbin/iw ] && echo /usr/sbin/iw) || echo "")

cleanup_and_restore() {{
  echo "STATUS|Restoring default CPU governor ($ORIG_GOV), clocks, cores, LEDs, and teslausb.service"
  for c in 1 2 3; do
    if [ -w "/sys/devices/system/cpu/cpu${{c}}/online" ] || sudo test -w "/sys/devices/system/cpu/cpu${{c}}/online" 2>/dev/null; then
      echo 1 | sudo tee "/sys/devices/system/cpu/cpu${{c}}/online" >/dev/null 2>&1 || true
    fi
  done
  echo "$ORIG_MAX_FREQ" | sudo tee /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq >/dev/null 2>&1 || true
  echo "$ORIG_GOV" | sudo tee /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor >/dev/null 2>&1 || true
  if [ -e /sys/class/devfreq/dmc/governor ]; then
    echo dmc_ondemand | sudo tee /sys/class/devfreq/dmc/governor >/dev/null 2>&1 || true
  fi
  for gpu_gov in /sys/class/devfreq/*.gpu/governor; do
    if [ -e "$gpu_gov" ]; then
      echo simple_ondemand | sudo tee "$gpu_gov" >/dev/null 2>&1 || true
    fi
  done
  if [ -n "$IW_BIN" ]; then
    sudo "$IW_BIN" dev wlan0 set power_save on >/dev/null 2>&1 || true
  fi
  if [ -e /sys/class/leds/ACT/trigger ]; then
    echo heartbeat | sudo tee /sys/class/leds/ACT/trigger >/dev/null 2>&1 || true
  fi
  rm -f /tmp/teslausb_wifi_send.py
  sudo systemctl start teslausb.service >/dev/null 2>&1 || true
}}
trap cleanup_and_restore EXIT

sudo modprobe cpufreq_powersave 2>/dev/null || true
sudo modprobe cpufreq_userspace 2>/dev/null || true
sudo modprobe governor_powersave 2>/dev/null || true
sudo modprobe governor_performance 2>/dev/null || true

sudo systemctl stop teslausb.service 2>/dev/null || true

cat > /tmp/teslausb_wifi_send.py << 'PYEOF'
import json, socket, sys, time
host = sys.argv[1]
port = int(sys.argv[2])
size_mb = int(sys.argv[3])
buf = b"\\x55" * (1024 * 1024)
t0 = time.monotonic()
with socket.create_connection((host, port), timeout=30) as s:
    for _ in range(size_mb):
        s.sendall(buf)
dt = time.monotonic() - t0
print(json.dumps({{
    "size_mb": size_mb,
    "elapsed_ms": round(dt * 1000),
    "send_mbps": round(size_mb / dt, 2)
}}))
PYEOF

read_hw_telemetry() {{
  local cpu_khz=$(sudo cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq 2>/dev/null || echo 0)
  local dmc_hz=0
  if [ -e /sys/class/devfreq/dmc/cur_freq ]; then
    dmc_hz=$(sudo cat /sys/class/devfreq/dmc/cur_freq 2>/dev/null || echo 0)
  elif command -v vcgencmd >/dev/null 2>&1; then
    dmc_hz=$(vcgencmd measure_clock core 2>/dev/null | cut -d= -f2 || echo 0)
  fi
  local temp_mc=$(sudo cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null || echo 0)
  local online=$(cat /sys/devices/system/cpu/online 2>/dev/null || echo "0-3")
  python3 -c "import json; print(json.dumps({{'cpu_mhz': round($cpu_khz/1000, 1), 'bus_mhz': round($dmc_hz/1e6, 1), 'soc_temp_c': round($temp_mc/1000, 1), 'online_cpus': '$online'}}))"
}}

AVAIL_GOVS=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_available_governors 2>/dev/null || echo "conservative performance")
HAS_HOTPLUG=false
if echo 1 | sudo tee /sys/devices/system/cpu/cpu3/online >/dev/null 2>&1; then
  HAS_HOTPLUG=true
fi
HAS_DEVFREQ=false
if [ -e /sys/class/devfreq/dmc/governor ]; then
  HAS_DEVFREQ=true
fi

declare -a MODES=()
for g in powersave conservative schedutil ondemand interactive performance; do
  if echo " $AVAIL_GOVS " | grep -q " $g "; then
    MODES+=("gov_${{g}}_${{ORIG_CORES}}c|${{g}} (${{ORIG_CORES}}c, default)|${{g}}|${{ORIG_CORES}}|default|max|on")
  fi
done

if [ "$HAS_HOTPLUG" = "true" ]; then
  MODES+=("conservative_2c_def|conservative (2c, default)|conservative|2|default|max|on")
  MODES+=("conservative_1c_def|conservative (1c, default)|conservative|1|default|max|on")
fi

if [ "$HAS_DEVFREQ" = "true" ]; then
  MODES+=("conservative_4c_devps|conservative (4c, powersave RAM/GPU)|conservative|4|powersave|max|on")
  if [ "$HAS_HOTPLUG" = "true" ]; then
    MODES+=("conservative_2c_devps|conservative (2c, powersave RAM/GPU)|conservative|2|powersave|max|on")
    MODES+=("powersave_4c_devps|powersave (4c, powersave RAM/GPU)|powersave|4|powersave|max|on")
    MODES+=("powersave_2c_devps|powersave (2c, powersave RAM/GPU)|powersave|2|powersave|max|on")
    MODES+=("powersave_1c_devps|powersave (1c, powersave RAM/GPU)|powersave|1|powersave|max|on")
  fi
  MODES+=("performance_4c_devperf|performance (4c, performance RAM/GPU)|performance|4|performance|max|on")
else
  # Discrete frequency caps & Wi-Fi power_save variants (e.g., Raspberry Pi Zero W / Zero 2 W)
  AVAIL_FREQS=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_available_frequencies 2>/dev/null || echo "")
  for f_khz in 700000 800000 900000; do
    if [ "$f_khz" -gt "$ORIG_MIN_FREQ" ] && [ "$f_khz" -lt "$ORIG_MAX_FREQ" ] && echo " $AVAIL_FREQS " | grep -q " $f_khz "; then
      f_mhz=$(( f_khz / 1000 ))
      MODES+=("cap_${{f_mhz}}mhz_conservative|conservative (cap ${{f_mhz}} MHz)|conservative|${{ORIG_CORES}}|default|${{f_khz}}|on")
    fi
  done
  if [ -n "$IW_BIN" ]; then
    MODES+=("conservative_wifipsoff|conservative (Wi-Fi power_save off)|conservative|${{ORIG_CORES}}|default|max|off")
    MODES+=("powersave_wifipsoff|powersave (Wi-Fi power_save off)|powersave|${{ORIG_CORES}}|default|max|off")
    MODES+=("performance_wifipsoff|performance (Wi-Fi power_save off)|performance|${{ORIG_CORES}}|default|max|off")
  fi
  if [ -e /sys/class/leds/ACT/trigger ]; then
    min_mhz=$(( ORIG_MIN_FREQ / 1000 ))
    MODES+=("powersave_led_off|powersave (${{min_mhz}} MHz, Wi-Fi PS on, LED off)|powersave|${{ORIG_CORES}}|led_off|max|on")
  fi
fi

apply_mode() {{
  local cpu_gov="$1"
  local cores="$2"
  local dev_gov="$3"
  local max_freq="$4"
  local wifi_ps="$5"

  if [ "$HAS_HOTPLUG" = "true" ]; then
    for c in 1 2 3; do
      echo 1 | sudo tee "/sys/devices/system/cpu/cpu${{c}}/online" >/dev/null 2>&1 || true
    done
  fi

  if [ "$max_freq" = "max" ]; then
    echo "$ORIG_MAX_FREQ" | sudo tee /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq >/dev/null 2>&1 || true
  else
    echo "$max_freq" | sudo tee /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq >/dev/null 2>&1 || true
  fi

  echo "$cpu_gov" | sudo tee /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor >/dev/null 2>&1 || true

  if [ "$HAS_HOTPLUG" = "true" ]; then
    if [ "$cores" -eq 2 ]; then
      echo 0 | sudo tee /sys/devices/system/cpu/cpu2/online >/dev/null
      echo 0 | sudo tee /sys/devices/system/cpu/cpu3/online >/dev/null
    elif [ "$cores" -eq 1 ]; then
      echo 0 | sudo tee /sys/devices/system/cpu/cpu1/online >/dev/null
      echo 0 | sudo tee /sys/devices/system/cpu/cpu2/online >/dev/null
      echo 0 | sudo tee /sys/devices/system/cpu/cpu3/online >/dev/null
    fi
  fi

  if [ "$HAS_DEVFREQ" = "true" ]; then
    if [ "$dev_gov" = "default" ]; then
      echo dmc_ondemand | sudo tee /sys/class/devfreq/dmc/governor >/dev/null 2>&1 || true
      for gpu_gov in /sys/class/devfreq/*.gpu/governor; do
        [ -e "$gpu_gov" ] && echo simple_ondemand | sudo tee "$gpu_gov" >/dev/null 2>&1 || true
      done
    else
      echo "$dev_gov" | sudo tee /sys/class/devfreq/dmc/governor >/dev/null 2>&1 || true
      for gpu_gov in /sys/class/devfreq/*.gpu/governor; do
        [ -e "$gpu_gov" ] && echo "$dev_gov" | sudo tee "$gpu_gov" >/dev/null 2>&1 || true
      done
    fi
  fi

  if [ -e /sys/class/leds/ACT/trigger ]; then
    if [ "$dev_gov" = "led_off" ]; then
      echo none | sudo tee /sys/class/leds/ACT/trigger >/dev/null 2>&1 || true
      echo 0 | sudo tee /sys/class/leds/ACT/brightness >/dev/null 2>&1 || true
    else
      echo heartbeat | sudo tee /sys/class/leds/ACT/trigger >/dev/null 2>&1 || true
    fi
  fi

  if [ -n "$IW_BIN" ]; then
    sudo "$IW_BIN" dev wlan0 set power_save "$wifi_ps" >/dev/null 2>&1 || true
  fi
}}

for entry in "${{MODES[@]}}"; do
  IFS='|' read -r mode_id mode_label cpu_gov cores dev_gov max_freq wifi_ps <<< "$entry"
  apply_mode "$cpu_gov" "$cores" "$dev_gov" "$max_freq" "$wifi_ps"
  sleep 1.0

  echo "PHASE_START|${{mode_id}}|${{mode_label}}|idle|1"
  sleep {idle_seconds}
  TELEM=$(read_hw_telemetry)
  echo "PHASE_END|${{mode_id}}|idle|1|${{TELEM}}"

  for run_idx in $(seq 1 {runs}); do
    sleep 1.0
    echo "PHASE_START|${{mode_id}}|${{mode_label}}|disk_write_160mb|${{run_idx}}"
    BENCH_JSON=$(sudo /root/bin/benchmark.sh 2>/dev/null | tail -n 1)
    TELEM=$(read_hw_telemetry)
    COMBINED=$(python3 -c "import json, sys; a=json.loads(sys.argv[1]); b=json.loads(sys.argv[2]); a.update(b); print(json.dumps(a))" "$BENCH_JSON" "$TELEM")
    echo "PHASE_END|${{mode_id}}|disk_write_160mb|${{run_idx}}|${{COMBINED}}"
  done

  if [ "{wifi_enabled}" = "true" ]; then
    for run_idx in $(seq 1 {runs}); do
      sleep 1.0
      echo "PHASE_START|${{mode_id}}|${{mode_label}}|wifi_send_100mb|${{run_idx}}"
      WIFI_JSON=$(python3 /tmp/teslausb_wifi_send.py "{sink_host}" "{wifi_sink_port}" "{wifi_size_mb}")
      TELEM=$(read_hw_telemetry)
      COMBINED=$(python3 -c "import json, sys; a=json.loads(sys.argv[1]); b=json.loads(sys.argv[2]); a.update(b); print(json.dumps(a))" "$WIFI_JSON" "$TELEM")
      echo "PHASE_END|${{mode_id}}|wifi_send_100mb|${{run_idx}}|${{COMBINED}}"
    done
  fi
done

echo "ALL_DONE"
"""


class FNB58PowerBenchmark:
    def __init__(self, output_dir: Path, meter_prefix: str = "FNB") -> None:
        self.output_dir = output_dir
        self.output_dir.mkdir(parents=True, exist_ok=True)
        self.meter_prefix = meter_prefix
        self.raw_csv_path = self.output_dir / "fnb58_raw_samples.csv"
        self.runs_json_path = self.output_dir / "benchmark_runs.json"
        self.summary_json_path = self.output_dir / "benchmark_summary.json"

        self.rx_buf = bytearray()
        self.samples: list[dict[str, Any]] = []
        self.current_temp: float | None = None
        self.dp: float | None = None
        self.dn: float | None = None

        self.active_mode_id = "init"
        self.active_workload = "none"
        self.active_run = 0
        self.runs_recorded: list[dict[str, Any]] = []

        self.csv_file = self.raw_csv_path.open("w", newline="")
        self.csv_writer = csv.writer(self.csv_file)
        self.csv_writer.writerow(
            [
                "iso_time",
                "mono_s",
                "mode_id",
                "workload",
                "run_idx",
                "voltage_v",
                "current_a",
                "power_w",
                "meter_temp_c",
                "dp_v",
                "dn_v",
            ]
        )

    def on_ble_notify(self, _sender: Any, data: bytearray) -> None:
        self.rx_buf.extend(data)
        now_mono = time.monotonic()
        now_iso = datetime.now(tz=timezone.utc).isoformat()
        i = 0
        while i < len(self.rx_buf):
            if self.rx_buf[i] != 0xAA:
                i += 1
                continue
            if i + 2 >= len(self.rx_buf):
                break
            pkt_type = self.rx_buf[i + 1]
            pkt_len = self.rx_buf[i + 2]
            expected = PKT_LENS.get(pkt_type)
            if not expected or pkt_len != expected:
                i += 1
                continue
            if i + 4 + pkt_len > len(self.rx_buf):
                break
            pkt_data = self.rx_buf[i + 3 : i + 3 + pkt_len]
            chk = self.rx_buf[i + 3 + pkt_len]
            inner = self.rx_buf[i : i + 3 + pkt_len]
            calc = crc16xmodem(inner) & 0xFF
            if chk != calc:
                i += 1
                continue

            if pkt_type == 0x07:
                v_raw, a_raw = struct.unpack("<HH", pkt_data[:4])
                v = v_raw / 1000.0
                a = a_raw / 1000.0
                w = v * a
                self.samples.append(
                    {
                        "mono_s": now_mono,
                        "iso_time": now_iso,
                        "mode_id": self.active_mode_id,
                        "workload": self.active_workload,
                        "run_idx": self.active_run,
                        "v": v,
                        "a": a,
                        "w": w,
                    }
                )
                self.csv_writer.writerow(
                    [
                        now_iso,
                        f"{now_mono:.4f}",
                        self.active_mode_id,
                        self.active_workload,
                        self.active_run,
                        f"{v:.4f}",
                        f"{a:.4f}",
                        f"{w:.4f}",
                        self.current_temp if self.current_temp is not None else "",
                        self.dp if self.dp is not None else "",
                        self.dn if self.dn is not None else "",
                    ]
                )
            elif pkt_type == 0x05:
                sign = 1 if pkt_data[4] > 0 else -1
                temp_raw = struct.unpack("<H", pkt_data[5:7])[0]
                self.current_temp = sign * temp_raw / 10.0
            elif pkt_type == 0x06:
                dp_raw, dn_raw = struct.unpack("<HH", pkt_data[:4])
                self.dp = dp_raw / 1000.0
                self.dn = dn_raw / 1000.0

            i += 4 + pkt_len
        self.rx_buf = self.rx_buf[i:]

    def compute_window_stats(self, t_start: float, t_end: float) -> dict[str, Any]:
        win = [s for s in self.samples if t_start <= s["mono_s"] <= t_end]
        if not win:
            return {"sample_count": 0}
        vs = [s["v"] for s in win]
        cs = [s["a"] for s in win]
        ws = [s["w"] for s in win]
        energy_j = 0.0
        for idx in range(1, len(win)):
            dt = win[idx]["mono_s"] - win[idx - 1]["mono_s"]
            energy_j += 0.5 * (win[idx]["w"] + win[idx - 1]["w"]) * dt
        return {
            "sample_count": len(win),
            "avg_w": round(statistics.mean(ws), 4),
            "max_w": round(max(ws), 4),
            "min_w": round(min(ws), 4),
            "stdev_w": round(statistics.stdev(ws), 4) if len(ws) > 1 else 0.0,
            "avg_ma": round(statistics.mean(cs) * 1000.0, 2),
            "max_ma": round(max(cs) * 1000.0, 2),
            "min_ma": round(min(cs) * 1000.0, 2),
            "avg_v": round(statistics.mean(vs), 4),
            "min_v": round(min(vs), 4),
            "energy_j": round(energy_j, 3),
            "energy_mwh": round((energy_j / 3600.0) * 1000.0, 4),
        }

    async def _connect_meter(self) -> tuple[Any, Any]:
        try:
            from bleak import BleakClient, BleakScanner
        except ImportError as exc:
            raise RuntimeError(
                "Missing 'bleak' package. Install with: pip install bleak"
            ) from exc

        print(f"Scanning for BLE power meter matching prefix '{self.meter_prefix}'...")
        device = await BleakScanner.find_device_by_filter(
            lambda d, _adv: bool(
                d.name and d.name.upper().startswith(self.meter_prefix.upper())
            ),
            timeout=10.0,
        )
        if not device:
            raise RuntimeError(
                f"No BLE power meter matching '{self.meter_prefix}' found."
            )
        return BleakClient(device), device

    async def run_record_only(self, duration: float) -> None:
        client, device = await self._connect_meter()
        print(f"Connecting to {device.name}...")
        async with client:
            await client.start_notify(CHAR_NOTIFY, self.on_ble_notify)
            await client.write_gatt_char(CHAR_WRITE, make_cmd(0x81), response=False)
            await asyncio.sleep(2.0)
            await client.write_gatt_char(CHAR_WRITE, make_cmd(0x82), response=False)
            self.active_mode_id = "record_only"
            self.active_workload = "continuous"
            self.active_run = 1
            t0 = time.monotonic()
            print(f"Recording live power for {duration:.1f}s...")
            while time.monotonic() - t0 < duration:
                await asyncio.sleep(1.0)
                if self.samples:
                    last = self.samples[-1]
                    elapsed = time.monotonic() - t0
                    sys.stdout.write(
                        f"\r[{elapsed:5.1f}s / {duration:5.1f}s] "
                        f"{last['v']:.3f}V | {last['a']*1000:.1f}mA | {last['w']:.3f}W "
                        f"({len(self.samples)} samples)"
                    )
                    sys.stdout.flush()
            print()
            await client.stop_notify(CHAR_NOTIFY)
        self.csv_file.close()
        stats = self.compute_window_stats(0.0, float("inf"))
        print(json.dumps(stats, indent=2))

    async def run_matrix(
        self,
        *,
        ssh_target: str,
        ssh_jump: str | None,
        runs: int,
        idle_seconds: float,
        wifi_sink_host: str | None,
        wifi_sink_port: int,
        wifi_size_mb: int,
    ) -> None:
        client, device = await self._connect_meter()
        print(f"Connecting to {device.name}...")
        async with client:
            await client.start_notify(CHAR_NOTIFY, self.on_ble_notify)
            await client.write_gatt_char(CHAR_WRITE, make_cmd(0x81), response=False)
            await asyncio.sleep(2.0)
            await client.write_gatt_char(CHAR_WRITE, make_cmd(0x82), response=False)
            await asyncio.sleep(1.0)
            print(
                f"BLE stream active ({len(self.samples)} warmup samples). "
                f"Starting remote benchmark matrix on {ssh_target}..."
            )

            ssh_cmd = ["ssh", "-A"]
            if ssh_jump:
                ssh_cmd.extend(["-J", ssh_jump])
            ssh_cmd.extend([ssh_target, "bash -s"])

            proc = await asyncio.create_subprocess_exec(
                *ssh_cmd,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.STDOUT,
            )
            assert proc.stdin is not None
            assert proc.stdout is not None

            remote_script = build_remote_script(
                runs=runs,
                idle_seconds=idle_seconds,
                wifi_sink_host=wifi_sink_host,
                wifi_sink_port=wifi_sink_port,
                wifi_size_mb=wifi_size_mb,
            )
            proc.stdin.write(remote_script.encode("utf-8"))
            await proc.stdin.drain()
            proc.stdin.close()

            phase_start_mono = 0.0
            current_label = ""

            while True:
                line_bytes = await proc.stdout.readline()
                if not line_bytes:
                    break
                line = line_bytes.decode("utf-8", errors="replace").strip()
                if not line:
                    continue

                if line.startswith("PHASE_START|"):
                    parts = line.split("|")
                    _, mode_id, mode_label, workload, run_str = parts
                    self.active_mode_id = mode_id
                    self.active_workload = workload
                    self.active_run = int(run_str)
                    current_label = mode_label
                    phase_start_mono = time.monotonic()
                    print(f"  -> START [{mode_id}] {workload} #{run_str}", flush=True)

                elif line.startswith("PHASE_END|"):
                    t_end = time.monotonic()
                    parts = line.split("|", 4)
                    _, mode_id, workload, run_str, payload_json = parts
                    run_idx = int(run_str)
                    self.active_workload = "between_runs"
                    self.csv_file.flush()

                    pstats = self.compute_window_stats(phase_start_mono, t_end)
                    try:
                        hw_metrics = json.loads(payload_json)
                    except Exception:
                        hw_metrics = {"raw": payload_json}

                    record = {
                        "mode_id": mode_id,
                        "mode_label": current_label,
                        "workload": workload,
                        "run_idx": run_idx,
                        "duration_s": round(t_end - phase_start_mono, 3),
                        "power": pstats,
                        "metrics": hw_metrics,
                    }
                    self.runs_recorded.append(record)
                    self.runs_json_path.write_text(
                        json.dumps(self.runs_recorded, indent=2) + "\n"
                    )

                    avg_w = pstats.get("avg_w", 0.0)
                    max_w = pstats.get("max_w", 0.0)
                    avg_ma = pstats.get("avg_ma", 0.0)
                    max_ma = pstats.get("max_ma", 0.0)
                    extra = ""
                    if "write_mbps" in hw_metrics:
                        extra = f" | Write: {hw_metrics['write_mbps']} MB/s ({hw_metrics.get('rating')})"
                    elif "send_mbps" in hw_metrics:
                        extra = f" | Wi-Fi: {hw_metrics['send_mbps']} MB/s"
                    print(
                        f"     DONE  [{mode_id}] {workload} #{run_idx}: "
                        f"avg {avg_w:.3f}W ({avg_ma:.0f}mA), peak {max_w:.3f}W ({max_ma:.0f}mA)"
                        f"{extra}",
                        flush=True,
                    )
                elif line.startswith("STATUS|"):
                    print(f"  [target] {line.split('|', 1)[1]}", flush=True)

            await proc.wait()
            await client.stop_notify(CHAR_NOTIFY)

        self.csv_file.close()
        self._write_summary()

    def _write_summary(self) -> None:
        grouped: dict[str, dict[str, Any]] = {}
        for r in self.runs_recorded:
            mid = r["mode_id"]
            if mid not in grouped:
                grouped[mid] = {
                    "mode_id": mid,
                    "mode_label": r["mode_label"],
                    "workloads": {},
                }
            wl = r["workload"]
            grouped[mid]["workloads"].setdefault(wl, []).append(r)

        aggregated: list[dict[str, Any]] = []
        for mid, mdata in grouped.items():
            entry: dict[str, Any] = {
                "mode_id": mid,
                "mode_label": mdata["mode_label"],
            }
            for wl, runs in mdata["workloads"].items():
                valid = [x for x in runs if x["power"].get("sample_count", 0) > 0]
                avg_ws = [x["power"]["avg_w"] for x in valid]
                max_ws = [x["power"]["max_w"] for x in valid]
                min_ws = [x["power"]["min_w"] for x in valid]
                avg_mas = [x["power"]["avg_ma"] for x in valid]
                max_mas = [x["power"]["max_ma"] for x in valid]
                min_vs = [x["power"]["min_v"] for x in valid]

                wl_stat: dict[str, Any] = {
                    "runs": len(runs),
                    "avg_w": round(statistics.mean(avg_ws), 3) if avg_ws else 0.0,
                    "max_w": round(max(max_ws), 3) if max_ws else 0.0,
                    "min_w": round(min(min_ws), 3) if min_ws else 0.0,
                    "avg_ma": round(statistics.mean(avg_mas), 1) if avg_mas else 0.0,
                    "max_ma": round(max(max_mas), 1) if max_mas else 0.0,
                    "min_v": round(min(min_vs), 3) if min_vs else 0.0,
                }
                if wl == "disk_write_160mb":
                    mbps = [
                        float(x["metrics"]["write_mbps"])
                        for x in runs
                        if x["metrics"].get("write_mbps") is not None
                    ]
                    if mbps:
                        wl_stat["avg_write_mbps"] = round(statistics.mean(mbps), 2)
                        wl_stat["min_write_mbps"] = round(min(mbps), 2)
                        wl_stat["max_write_mbps"] = round(max(mbps), 2)
                elif wl == "wifi_send_100mb":
                    mbps = [
                        float(x["metrics"]["send_mbps"])
                        for x in runs
                        if x["metrics"].get("send_mbps") is not None
                    ]
                    if mbps:
                        wl_stat["avg_send_mbps"] = round(statistics.mean(mbps), 2)
                        wl_stat["min_send_mbps"] = round(min(mbps), 2)
                        wl_stat["max_send_mbps"] = round(max(mbps), 2)

                if runs:
                    last_m = runs[-1]["metrics"]
                    wl_stat["cpu_mhz"] = last_m.get("cpu_mhz")
                    wl_stat["bus_mhz"] = last_m.get("bus_mhz")
                    wl_stat["soc_temp_c"] = last_m.get("soc_temp_c")
                    wl_stat["online_cpus"] = last_m.get("online_cpus")
                entry[wl] = wl_stat
            aggregated.append(entry)

        self.summary_json_path.write_text(json.dumps(aggregated, indent=2) + "\n")
        print(f"Summary saved to {self.summary_json_path}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="FNIRSI FNB58 BLE Power & Throughput Benchmark for TeslaUSB SBCs"
    )
    parser.add_argument(
        "--ssh-target",
        default="pi@dashvault.local",
        help="SSH user@host for the TeslaUSB board (default: pi@dashvault.local)",
    )
    parser.add_argument(
        "--ssh-jump",
        default=None,
        help="Optional SSH ProxyJump host(s) (-J)",
    )
    parser.add_argument(
        "--meter-prefix",
        default="FNB",
        help="BLE device name prefix to match (default: FNB)",
    )
    parser.add_argument(
        "--runs",
        type=int,
        default=3,
        help="Number of benchmark iterations per workload per power mode (default: 3)",
    )
    parser.add_argument(
        "--idle-seconds",
        type=float,
        default=3.5,
        help="Idle baseline window per mode in seconds (default: 3.5)",
    )
    parser.add_argument(
        "--wifi-sink-host",
        default=None,
        help="Optional LAN IP/host running a TCP discard sink for the 100 MB Wi-Fi send test",
    )
    parser.add_argument(
        "--wifi-sink-port",
        type=int,
        default=19999,
        help="TCP port for the Wi-Fi discard sink (default: 19999)",
    )
    parser.add_argument(
        "--wifi-size-mb",
        type=int,
        default=100,
        help="Outbound Wi-Fi transfer size in MiB per run (default: 100)",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path("./power_benchmark_results"),
        help="Directory for raw CSV and JSON results",
    )
    parser.add_argument(
        "--record-only",
        action="store_true",
        help="Only record live BLE power from the FNB58 without connecting to an SSH target",
    )
    parser.add_argument(
        "--duration",
        type=float,
        default=30.0,
        help="Recording duration in seconds when using --record-only (default: 30)",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    runner = FNB58PowerBenchmark(
        output_dir=args.output_dir,
        meter_prefix=args.meter_prefix,
    )
    if args.record_only:
        asyncio.run(runner.run_record_only(duration=args.duration))
    else:
        quoted = shlex.join(sys.argv)
        print(f"Starting benchmark: {quoted}")
        asyncio.run(
            runner.run_matrix(
                ssh_target=args.ssh_target,
                ssh_jump=args.ssh_jump,
                runs=args.runs,
                idle_seconds=args.idle_seconds,
                wifi_sink_host=args.wifi_sink_host,
                wifi_sink_port=args.wifi_sink_port,
                wifi_size_mb=args.wifi_size_mb,
            )
        )


if __name__ == "__main__":
    main()
