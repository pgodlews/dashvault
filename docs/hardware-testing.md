# Hardware testing

The unit tests and the Docker harness never touch a radio. The two tools in `tests/hardware/` test the parts that only exist on a real board: joining Wi-Fi networks and first-run setup over the hotspot and Bluetooth.

Use a test card. Both tools expect a freshly flashed image, and a board that has finished setup will not run them.

## What you need

- A Linux machine with Docker, NetworkManager, `sudo`, `curl`, `dig`, `openssl` and `python3-dbus`.
- A **spare** USB Wi-Fi adapter that supports AP mode. `iw list` shows `AP` under "Supported interface modes".
- A Bluetooth adapter on the same machine, for the setup check.
- The board under test, within a few metres.

Both tools refuse an adapter that carries the default route, so they cannot cut off your connection to the machine. The Wi-Fi lab moves the adapter into a container while it runs, so the host's firewall and other interfaces are not changed. The adapter returns to the host when the lab ends.

## Wi-Fi security lab

The lab acts as a router and offers one security configuration after another. A small client on the board tries each network with the same `nmcli` command a real first boot uses, then reports whether it joined and what it negotiated.

| Mode | Router offers | Protected management frames |
|---|---|---|
| `wpa2-psk` | WPA2-PSK | Off |
| `wpa2-psk-pmf-optional` | WPA2-PSK | Optional |
| `wpa2-transition` | WPA2-PSK and WPA3-SAE | Optional |
| `wpa2-transition-h2e` | WPA2-PSK and WPA3-SAE, with H2E available | Optional |
| `psk-and-sha256` | PSK and PSK-SHA256 | Off |
| `psk-and-sha256-pmf` | PSK and PSK-SHA256 | Optional |
| `psk-sha256-only` | PSK-SHA256 | Required |
| `wpa3-sae` | WPA3-SAE | Required |
| `wpa3-sae-h2e` | WPA3-SAE with H2E | Required |

### Check the adapter first

Offering a mode does not prove a client can complete it. Before you trust a WPA3 failure, join the lab's WPA3 network from a phone that supports WPA3:

```bash
export LAB_PSK=$(openssl rand -hex 12)
echo "$LAB_PSK"
LAB_ONLY=wpa3-sae LAB_HOLD=300 tests/hardware/wifi-lab/run-lab.sh wlan1 ./lab-adapter 0
```

Replace `wlan1` with your adapter. The lab prints `client joined: akm=SAE pmf=yes` and exits when the phone connects. Passing `0` as the board count with no `LAB_ONLY` just checks that every mode starts.

### Run it on a board

1. Flash the image to a test card and mount its boot partition.
2. Install the client as the card's `run_once` script:

   ```bash
   sed "s/@LAB_PSK@/$LAB_PSK/" tests/hardware/wifi-lab/pi-client.py > /path/to/bootfs/run_once
   ```

3. Start the lab for one board:

   ```bash
   tests/hardware/wifi-lab/run-lab.sh wlan1 ./lab-results 1
   ```

4. Put the card in the board and power it on.

The lab waits up to 8 minutes for the board, or `LAB_HELLO_TIMEOUT` seconds. A full round takes 3 to 20 minutes, depending on how many modes fail. Results are written to `lab-results/results.json`, and the board keeps its own copy in `dvlab-results.jsonl` on the boot partition. If no lab appears within 15 minutes, the client exits. Either way the board then continues into normal first-run setup.

To test several boards in one round, pass their number instead of `1`. The lab moves on when that many boards have reported.

| Variable | Default | Purpose |
|---|---|---|
| `LAB_PSK` | Required | Throwaway password for the test networks |
| `LAB_HELLO_TIMEOUT` | 480 | Seconds to wait for boards to appear |
| `LAB_MODE_TIMEOUT` | 130 | Seconds to give each mode |
| `LAB_ONLY` | All modes | Offer a single mode (with a board count of `0`) |
| `LAB_HOLD` | 3 | Seconds to hold that mode, or until a client joins |
| `LAB_SSID` | `dvlab-<mode>` | Network name to use with `LAB_ONLY` |
| `LAB_COUNTRY` | Unset | Two-letter regulatory domain for the access point |

## First-run setup check

With a freshly flashed board waiting at its `Dashvault-XXXX` hotspot, run:

```bash
tests/hardware/provisioning/check-provisioning.sh wlan1 hci0
```

Pi Zero boards offer Bluetooth and the hotspot in turn rather than together, so set `SEQUENTIAL=1` for them:

```bash
SEQUENTIAL=1 tests/hardware/provisioning/check-provisioning.sh wlan1 hci0
```

It checks the setup page, captive DNS, input validation, Bluetooth, and recovery from a wrong Wi-Fi password over both the hotspot and Bluetooth. The board reboots twice. The check never sends working details, so the board finishes back at its hotspot, ready for a real setup from a phone.

## Measured results

Tested on 2026-11-21 with the Raspberry Pi OS Trixie (32-bit) image, kernel 6.18.60, NetworkManager 1.52.1 and wpa_supplicant 2.10.

| Mode | Pi 400 (firmware 7.84.17.1) | Pi Zero W (firmware 7.45.98) |
|---|---|---|
| `wpa2-psk` | Joined | Joined |
| `wpa2-psk-pmf-optional` | Joined | Joined |
| `wpa2-transition` | Joined, using PSK | Joined, using PSK |
| `wpa2-transition-h2e` | Joined, using PSK | Joined, using PSK |
| `psk-and-sha256` | Joined, using PSK-SHA256 | **Failed** |
| `psk-and-sha256-pmf` | Joined, using PSK-SHA256 | **Failed** |
| `psk-sha256-only` | Joined, using PSK-SHA256 | **Failed** |
| `wpa3-sae` | **Failed** | **Failed** |
| `wpa3-sae-h2e` | **Failed** | **Failed** |

A phone joined the lab's `wpa3-sae` network through the same adapter, so the `wpa3-sae` failures come from the boards. The H2E mode was not confirmed with a known-good client. The Pi Zero 2 W and other boards have not been through the lab yet.
