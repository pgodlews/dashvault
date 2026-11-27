# Radxa Zero 3W: cooling and Wi-Fi antennas

Measurements from 2026-11-27 on a Radxa Zero 3W (RK3566, AIC8800 Wi-Fi 6). The board had new thermal paste and a copper heatsink, and was tested with its built-in antenna and three external ones. Software: vendor rsdk Debian 12 image (kernel 6.1.84-10-rk2410-nocsf) with Dashvault v0.1.4 installed. The CPU governor was `conservative`, the Dashvault default. KDE was installed, but no desktop was running during the tests: SDDM was disabled, and GDM tried to start at each boot but failed. The generic installer now turns the desktop off on its own (see [`shell-layer.md`](shell-layer.md#generic-sbc-installer)).

**Summary**

- **Cooling:** normal Dashvault work (Wi-Fi copying, disk writes) stays at 58–63 °C. That is level with the earlier baseline in [`power-benchmarking.md`](power-benchmarking.md) and 12 °C below the first thermal trip. Only an artificial all-core CPU stress gets near the limits.
- **Antenna:** use the **sticker antenna** (external connector). Every access point stays above 5 MB/s with it, about twice the 2.5 MB/s that four cameras record. The built-in antenna is fastest right next to an access point but drops below 1 MB/s at range.

The scripts are in [`tools/radxa-bench/`](../tools/radxa-bench/). See [Re-running the tests](#re-running-the-tests).

## 1. Thermal

The test runs these phases one after another, logging every 2 s. Wi-Fi copying streams a 1 GiB file from the card to a wired machine. Disk writes are 160 MiB `dd` chunks with `oflag=dsync`, like the car's. CPU stress is `stress-ng --cpu 4`. The combined phase runs all three at once. Ambient temperature was not measured.

| Phase | Length | SoC avg | SoC max | CPU clock | Notes |
|---|---|---|---|---|---|
| Idle | 5 min | 51.0 °C | 53.8 °C | 408–1416 MHz | |
| Wi-Fi copy | 10 min | 58.4 °C | 63.3 °C | avg 1343 MHz | 24.0 MB/s |
| Disk write | 5 min | 61.0 °C | 63.3 °C | avg 1096 MHz | |
| CPU stress (4 cores) | 10 min | 71.7 °C | 81.1 °C | 1800 MHz all the time | Above 75 °C for 214 s, no throttling |
| Combined | 10 min | 82.7 °C | 83.9 °C | 1416–1608 MHz | Mild throttling, Wi-Fi down to 10.5 MB/s |
| Cool-down | 5 min | — | — | — | 81 → 65.8 °C |

**Thermal limits** (`thermal_zone0`, `soc-thermal`, `power_allocator` governor): the governor starts at 75 °C, its target is 85 °C, and the critical trip is 115 °C. Throttling shows as a lower CPU clock. In `/sys/class/thermal`, the CPU's cooling device is `cooling_device3` (`cpufreq-cpu0`). `cooling_device0` is the video encoder.

What this means:

- Archiving (Wi-Fi plus card reads) and recording (card writes) stay 12 °C or more below the 75 °C trip, and never reach the 68 °C where Dashvault's optional soft-throttling starts.
- The heatsink holds a full 4-core load at 1800 MHz for 10 minutes without throttling. Only stress, disk and Wi-Fi together push it into throttling, and even then it stays below the 85 °C target.
- The earlier baseline in [`power-benchmarking.md`](power-benchmarking.md) was 61.7–63.9 °C on the same kind of work (worst 69.4 °C). Disk and Wi-Fi now peak at 63.3 °C. The new paste and copper heatsink don't change normal running much, but they leave plenty of headroom.

## 2. Wi-Fi antennas

### Enabling the external antenna connector

The board switches between its built-in antenna and the external u.FL connector with two GPIOs. The base device tree selects the built-in antenna: its `board-antenna` regulator drives gpio-123 (GPIO3_D3) low. **An external antenna does nothing until you enable the overlay**, which drives gpio-122 (GPIO3_D2) low instead:

```bash
sudo /root/bin/remountfs_rw          # Dashvault keeps / read-only
sudo mv /boot/dtbo/radxa-zero3-external-antenna.dtbo.disabled \
        /boot/dtbo/radxa-zero3-external-antenna.dtbo
sudo u-boot-update                    # adds fdtoverlays to /boot/extlinux/extlinux.conf
sudo reboot
```

Check it after the reboot. The external connector is active when the line shows `gpio-122 ... board-antenna ... out lo`:

```bash
sudo grep board-antenna /sys/kernel/debug/gpio
```

To go back to the built-in antenna, rename the file back to `.dtbo.disabled`, run `u-boot-update` and reboot. With the overlay on and no antenna connected, Wi-Fi range is almost nothing.

### Method

- The board sat in one spot for every run, away from the access points. The network is a router plus two mesh nodes, all with the same SSID. In the tables, node A is the nearest and node B the furthest.
- Each mode pins the connection to one access point BSSID with a temporary `nmcli` setting. Each run is 30 s idle, then 150 s of upload: a 1 GiB random file read from the card and streamed to a discard server on a wired machine.
- **MB/s** is the average of the Wi-Fi interface's sent-bytes counter during the upload. **dBm** is the median signal under load from `iw dev wlan0 link`. (`iw station dump` returns nothing on the AIC8800 driver.)
- Every run had zero transmit errors, and the SoC peaked at 41–53 °C.
- There was one run per antenna per mode. A repeat of the automatic-choice run on the built-in antenna came out at 27.5 against 26.5 MB/s, and individual 2-second samples vary by 10–15%. Treat gaps smaller than about 2 MB/s as noise.

### Results (upload MB/s, signal in dBm)

| Access point | Built-in | Sticker | Stick ("toothpick") | Antenna 3 |
|---|---|---|---|---|
| Node A, 5 GHz ch 116 (nearest) | **27.7** (-52) | 23.4 (-52) | 22.4 (-54) | 21.0 (-52) |
| Router, 5 GHz ch 112 | 7.2 (-70) | **23.5** (-60) | 18.2 (-69) | 19.9 (-63) |
| Node B, 5 GHz ch 48 (furthest) | 0.6 (-86) | **10.9** (-71) | 5.8 (-74) | 7.9 (-74) |
| Node A, 2.4 GHz ch 1 | 5.0 (-74) | **10.8** (-55) | 9.6 (-49) | 9.6 (-52) |
| Router, 2.4 GHz ch 8 | 0.9 (-79) | 5.3 (-62) | 6.1 (-58) | **7.9** (-51) |
| Automatic choice | 26.5 (node A 5 GHz) | 23.3 (node A 5 GHz) | 22.6 (node A 5 GHz) | 23.5 (node A 5 GHz) |

Built-in antenna only: router 5 GHz ch 48 got 4.7 MB/s (-68), node A 5 GHz ch 48 got 7.2 (-62), and node B 2.4 GHz ch 5 got 0.4 (-84). Turning power save off made no difference while uploading: 25.0 MB/s, against 26.5 with it on.

### What the results show

- **The sticker antenna is the best overall.** It wins on 5 GHz at middle and long range, which is what matters for archiving from a car parked some distance from the house. Its worst result was 5.3 MB/s.
- **Antenna 3 comes second.** It has the strongest 2.4 GHz signal and is close to the sticker on 5 GHz, but behind it at the furthest node (7.9 against 10.9 MB/s).
- **The stick antenna** has a good 2.4 GHz signal but is 3–9 dB behind the sticker on 5 GHz.
- **The built-in antenna** is fine within sight of an access point (27.7 MB/s, the best result of the test) but collapses with distance: below 1 MB/s at -79 dBm and weaker. Throughput falls off sharply below about -75 dBm.
- Near an access point, the external antennas are about 15% slower than the built-in one. That is probably the cable and connector loss, and it doesn't matter at these speeds.
- Signal strength alone doesn't predict speed. On router 5 GHz, the stick antenna and the built-in one had the same signal (-69/-70 dBm), but the stick was 2.5× faster. Compare antennas by throughput.
- NetworkManager's automatic choice always picked node A on 5 GHz, the fastest access point, so pinning a BSSID isn't worth it.

## Re-running the tests

All scripts run as root on the board. Large files go in `/backingfiles` and results in `/mutable`. Copy the folder to the board first, for example to `/mutable/radxa-bench/`.

1. On a wired machine, start the discard server: `python3 tools/radxa-bench/tcp-sink.py 19999`
2. Thermal test (about 45 min; each phase length can be set with `IDLE`, `WIFI`, `DISK`, `CPU`, `COMBO`, `COOL` in seconds, and 0 skips a phase). It needs `stress-ng` and 3 GB free in `/backingfiles`:

   ```bash
   sudo /mutable/radxa-bench/thermal-test.sh SINK_IP 19999
   uv run python tools/radxa-bench/analyse-thermal.py thermal.csv
   ```

3. Antenna test: copy `modes.example.txt`, fill in your access points' BSSIDs (`nmcli -f SSID,BSSID,CHAN,SIGNAL dev wifi list`), then run all the modes for one antenna (about 3½ minutes per mode). It changes the connection temporarily and restores automatic choice and power save when it ends:

   ```bash
   sudo setsid nohup /mutable/radxa-bench/mode-matrix.sh SINK_IP 19999 sticker- /mutable/radxa-bench/modes.txt \
     > /mutable/antenna-sticker.log 2>&1 < /dev/null &
   uv run python tools/radxa-bench/analyse-wifi.py sticker-*.csv   # CSVs copied from /mutable/antenna-test/
   ```

   For one quick run on the current connection: `antenna-test.sh LABEL SINK_IP 19999 [UPLOAD_SECONDS]`.

Don't run these while the car is recording or archiving is active: they compete for the card and the Wi-Fi.
