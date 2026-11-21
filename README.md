# Dashvault

**A Raspberry Pi that plugs into your Tesla as the dashcam drive, copies every clip to your own storage when you get home, and gives you a web app to watch them.**

The car sees an ordinary USB drive. Behind it is a small computer that joins your home Wi-Fi when you park, sends the new dashcam and Sentry clips to your NAS or cloud storage, and frees the space again. You never pull the drive out of the glovebox.

Dashvault is built on [teslausb](https://github.com/marcone/teslausb), which has done the USB drive emulation and archiving since 2018. It adds a new web app, a Telegram bot, a drive log with maps, and tools for looking after the microSD card. See [Credits](#credits).

<!-- screenshot: dashboard -->
<!-- screenshot: clip viewer with map and telemetry -->
<!-- screenshot: drive map -->

> [!IMPORTANT]
> Dashvault is a hobby project with no warranty. It is not made, endorsed or supported by Tesla. Read [Before you install](#before-you-install) first.

## What you get

**Archiving**

- Dashcam, Sentry and Track Mode clips are copied to a Windows/Samba share, an NFS export, an rsync server or any [rclone](https://rclone.org) remote as soon as the car is in Wi-Fi range.
- Archiving starts when new clips appear, without waiting for a timer.
- RecentClips can be kept for much longer than the car's one hour, as far as the card has room.
- The drive is checked and repaired after the car cuts USB power without unmounting it.
- Optional extra drives for music, light shows and Boombox sounds, all on the same card.

**Web app**

- A dashboard with live archive progress, storage, Wi-Fi signal, temperature and power status.
- A clip browser and a player that shows all cameras in sync, on a phone or a desktop. You can add it to your phone's home screen.
- Logs, settings for every archive type, Wi-Fi network management, and updates with rollback.

**Drive log**

- GPS tracks from your saved and recent clips drawn on a map.
- Speed, gear, steering, accelerator, brake, indicators and Autopilot state replayed next to the video.
- Tracks are read from the video files on the Pi and stay on the Pi.

**Telegram bot**

- A message when archiving starts and finishes.
- Commands for status, free space, card health, starting a sync, and getting the latest clips sent to you.

**microSD care**

- A write-speed benchmark that tells you whether a card can keep up with the car.
- A running total of data written, with a wear warning at a threshold you set.

Some features are marked experimental in Settings and are off by default.

## Hardware

| Board | Status |
|---|---|
| Raspberry Pi Zero 2 W | Tested. The recommended board. |
| Raspberry Pi Zero W | Tested. Works, but the web app and archiving are slow. |
| Radxa Zero 3 | Works with the [install script](#radxa-and-other-boards). No flashable image and no hotspot setup. |
| Raspberry Pi 400 | Image, hotspot and Bluetooth setup, and install tested. USB connection to a car not tested, and as a keyboard computer it is not a practical choice. Does not join WPA3-only networks. |
| Raspberry Pi 4 and 5 | Supported by the setup scripts inherited from teslausb. Not tested with this release. |

You also need:

- A microSD card of 64 GB or more. Buy a high-endurance card made for dashcams or security cameras. An ordinary card wears out much sooner.
- A USB **data** cable from the board's data port to the car. Charge-only cables do not work.
- Home Wi-Fi that reaches where you park. The Pi Zero W only supports 2.4 GHz.
- A router using WPA2 or mixed WPA2/WPA3. WPA3-only networks do not work on the Pi Zero W or Pi 400, and the Pi Zero W also cannot join routers that offer PSK-SHA256. See the [measured results](docs/hardware-testing.md#measured-results).
- Somewhere to archive to: a NAS, a computer with a shared folder, or cloud storage through rclone.

## Install

The image carries the Dashvault release it was built from. First boot still needs internet access, to install a few Debian packages and set the clock.

1. Download the image from the [latest release](https://github.com/pgodlews/dashvault/releases/latest) and write it to the microSD card with [Raspberry Pi Imager](https://www.raspberrypi.com/software/) (choose **Use custom** at the bottom of the operating system list, and say no if it offers to apply customisation settings).
2. Put the card in the Pi and power it from a USB charger. After about two minutes (three on a Pi Zero W) a Wi-Fi network called `Dashvault-XXXX` appears.
3. Join it from your phone or laptop. The setup page opens by itself; if it doesn't, go to `http://192.168.4.1`. Choose your Wi-Fi and a login. Clips stay on the Pi until you add a NAS or cloud storage in the web app, under Settings → Archive storage.
4. The Pi restarts, joins your Wi-Fi and installs itself. This takes several minutes, and longer on a Pi Zero W. If it can't join your network, the setup hotspot comes back so you can correct the details.
5. Open `http://dashvault.local` and sign in with the login you chose.
6. Plug the Pi into the car's USB data port (the glovebox port on recent cars) and check that the dashcam icon appears on the screen.

The setup hotspot is open and unencrypted, and whatever you enter on it, including your Wi-Fi password, travels in the clear. Anyone nearby could also reach the page before you do. Do this at home, and finish it promptly.

**Prefer a file?** Before the first boot, open the card's `bootfs` partition, copy `teslausb_setup_variables.conf.sample` to `teslausb_setup_variables.conf` and fill it in. With that file present there is no hotspot, and nothing is sent over the air. The settings most people need are:

```bash
export SSID='your wifi name'
export WIFIPASS='your wifi password'

export ARCHIVE_SYSTEM=cifs
export ARCHIVE_SERVER=nas.local
export SHARE_NAME='TeslaCam'
export SHARE_USER=dashcam
export SHARE_PASSWORD='share password'

export WEBUI_USERNAME='admin'
export WEBUI_PASSWORD='a long password'
export PI_PASSWORD='another long password'
```

The Pi's name on your network is `dashvault`; set `TESLAUSB_HOSTNAME` to change it. The configuration file, its settings and the paths on the Pi keep the `teslausb` name on purpose, so teslausb guides and tools still apply. The [setup guide](doc/OneStepSetup.md) covers quoting rules for passwords, the LED codes during setup, and troubleshooting.

### Radxa and other boards

For experienced users. There is no Dashvault image and no hotspot or Bluetooth setup for these boards.

**Why:** a ready-to-flash image would have to include the board's Wi-Fi firmware, and we could not confirm that we are allowed to redistribute it. So Dashvault only ships its own code, and you get the operating system and firmware from the vendor.

1. Flash the vendor's Debian image (or Armbian) to a card of at least 64 GB, and get the board onto your network with SSH working.
2. Change the default passwords. Radxa's images create `radxa` and `rock` accounts with well-known passwords.
3. Download and run the install script. It fetches the latest release and verifies its checksum:

   ```bash
   curl -fsSL -o install.sh https://raw.githubusercontent.com/pgodlews/dashvault/main/setup/generic/install.sh
   ```

   ```bash
   sudo bash install.sh
   ```

4. It may shrink the root filesystem and reboot; if so, run it again with `sudo -i` as it tells you. Then edit `/teslausb/teslausb_setup_variables.conf` and run `/etc/rc.local` to finish.


### The web password is required

Setup stops near the end if `WEBUI_USERNAME` and `WEBUI_PASSWORD` are missing, and does not finish until they are set. The [setup guide](doc/OneStepSetup.md#security) explains how to recover. The web app can read and delete your footage and change the Pi's configuration, so it is never served without a password.

### Change the login password

The image's `pi` user starts with the well-known password `raspberry`. Setup over the hotspot replaces it with the password you choose. With a configuration file, set `PI_PASSWORD`, or use an SSH key with `SSH_ROOT_PUBLIC_KEY` and `SSH_DISABLE_PASSWORD_AUTHENTICATION=true`.

## Notifications

Telegram is the only notification channel. The Pushover, Gotify, IFTTT, Discord, Slack, Matrix, Signal, ntfy, AWS SNS and webhook options of teslausb were removed, and their settings are ignored. [Telegram setup](doc/ConfigureNotificationsForArchive.md) takes about five minutes and can be done from Settings in the web app.

## Updates

**Dashvault itself** is updated from Settings in the web app, with a rollback to the previous version.

**The operating system** (Debian and Raspberry Pi OS packages, including security fixes) is updated with a command over SSH, because the Pi's system partition is normally read-only and a plain `apt-get upgrade` fails or gets undone:

```bash
sudo /root/bin/os-update.sh check
```

```bash
sudo /root/bin/os-update.sh apply
```

`check` lists what is pending and changes nothing. `apply` makes the system writable, installs the updates without asking questions, keeps Dashvault's settings, and reboots. If power is lost part-way, the update is finished at the next boot.

Run `apply` while the Pi has steady power, for example plugged into a charger or with the car awake at home. Losing power while the kernel is being replaced can leave the card unable to boot, and then it has to be flashed again. Your clips on the archive are not affected.

## Before you install

**No warranty.** Dashvault is provided as is, under the [MIT licence](LICENSE), with no warranty of any kind. You use it at your own risk. The authors are not liable for lost footage, damaged hardware, a drained battery, or anything else that follows from using it.

**Not affiliated with Tesla.** This is an independent project. Tesla does not make, endorse or support it. Tesla is a trademark of Tesla, Inc. A software update to the car can change how the dashcam behaves and break this without notice.

**Do not rely on it as your only record.** Footage can be lost: a worn or slow card, a power cut while writing, a full disk, a failed archive, or a bug. If a recording matters, for an insurance claim or the police, save it in the car and copy it somewhere safe yourself as soon as you can. Check now and then that the dashcam icon in the car shows it is recording.

**microSD cards wear out.** A dashcam writes all the time. Use a high-endurance card, watch the wear figures in the web app, and expect to replace the card.

**Heat.** A parked car gets hot enough to throttle or shut down a small computer. Use a case that sheds heat, and keep the board out of direct sun.

**Keep it on your home network.** The web app is protected by a single password over plain HTTP, with a five-minute lockout after ten wrong passwords from one address. It is meant for your own network. Do not forward a port to it or expose it to the internet. Use a VPN if you want access from outside.

**Recording other people.** Dashcam and Sentry footage shows other people and their number plates. Laws on recording, keeping and sharing it differ from country to country. Following them is your responsibility.

**What leaves the Pi.** Clips go only to the archive you configure. The map loads its tiles from a map server, which is OpenStreetMap's unless you change it in Settings, so that server sees which areas you look at. Telegram messages, and any clips you ask the bot for, pass through Telegram's servers.

**Your credentials are on the card.** The configuration file holds your Wi-Fi password and archive credentials in readable form. If the Pi is stolen, change them.

## Documentation

- [Setup guide](doc/OneStepSetup.md)
- Archive targets: [Windows/Samba share](doc/SetupShare.md), [rsync](doc/SetupRSync.md), [rclone](doc/SetupRClone.md)
- [Telegram notifications and bot](doc/ConfigureNotificationsForArchive.md)
- [External drive on a Pi 4](doc/Pi4ExternalDriveSetup.md)
- [Backing up and restoring the Pi's configuration](docs/pi-backup-restore.md)
- [How RecentClips are kept](docs/snapshots.md)
- [Testing a microSD card](docs/sd-card-write-testing.md) and [power use by board](docs/power-benchmarking.md)
- [Architecture](docs/architecture.md) and the [developer documentation index](docs/index.md)

## Developing

You do not need a Pi to work on Dashvault. A Docker harness runs the scripts against a fake Pi and a Samba server.

```bash
git clone https://github.com/pgodlews/dashvault.git
cd dashvault
just bootstrap        # Python tooling and pre-commit hooks
just ci               # lint, tests, UI build and the integration harness
```

You need Docker, [just](https://github.com/casey/just), [uv](https://github.com/astral-sh/uv), Go 1.22 or later, and Node 20 with pnpm. [CONTRIBUTING.md](CONTRIBUTING.md) has the conventions.

| Part | Technology | Location |
|---|---|---|
| Archive loop and USB gadget | Bash | `run/` |
| Telegram bot | Python 3, standard library only | `run/teslausb_telegram_bot.py` |
| API | Go | `teslausb-www/go-backend/` |
| Web app | React, Vite, Tailwind | `teslausb-www/new/` |
| Device setup | Bash | `setup/` |
| SD image | pi-gen | `pi-gen-sources/` |

## Credits

Dashvault exists because teslausb does. The idea came from a [2018 r/teslamotors thread](https://www.reddit.com/r/teslamotors/comments/9m9gyk/build_a_smart_usb_drive_for_your_tesla_dash_cam/). **cimryan** published the first scripts, and **[marcone](https://github.com/marcone/teslausb)** has maintained and extended teslausb for years since, with many contributors. The USB drive emulation, snapshots, archive loop and device setup in this repository are their work.

If you want the original project, with its wider range of notification services and its own web interface, use [marcone/teslausb](https://github.com/marcone/teslausb).

## Licence

[MIT](LICENSE), the same as teslausb.
