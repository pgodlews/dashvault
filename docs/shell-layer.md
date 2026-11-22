# Shell Layer Reference

The shell layer is the Pi-side runtime. It owns USB gadget management, the archive loop, and notifications. It runs as root, talks to the hardware directly, and is intentionally NOT ported to Go.

---

## Key Files

| File | What it does |
|------|--------------|
| `run/ota_finalize.sh` | Finalize Telegram unit upgrades from legacy OTA installers, extending the existing rollback backup |
| `run/reconfigure-archive.sh` | Stop/unmount old archive destinations and restart after a settings transaction |
| `run/archiveloop` | Main daemon: USB gadget ↔ archive loop |
| `run/send-push-message` | Notification dispatcher (12 backends) |
| `run/teslausb_telegram_bot.py` | Long-polling inbound Telegram bot, including `/sdcard` |
| `run/telemetry_index.py` | Pre-archive Saved/Recent SEI extraction and track retention |
| `run/benchmark.sh` | 160 MiB zero-data, 4 MiB-block durable SD-card benchmark |
| `tools/sustained-write-test.py` | Four-camera, one-minute-cycle write-load generator |
| `run/teslausb-config` | Backup / restore Pi config |
| `run/lib/paths.sh` | Single source of truth for filesystem paths |
| `setup/pi/configure.sh` | Main Pi setup orchestrator |
| `setup/pi/configure-web.sh` | Installs nginx, Go backend, cttseraser |
| `setup/pi/configure-webui-auth.sh` | Creates `.htpasswd`, configures nginx auth |
| `setup/pi/teslausb-telegram-bot.service` | systemd unit for telegram bot |

---

## archiveloop (`run/archiveloop`)

Systemd service: `teslausb.service`  
Runs as: root  
ExecStart: `/bin/bash /root/bin/archiveloop`

### Startup Sequence
1. Source `run/lib/paths.sh` for path constants
2. Acquire flock on self (only one instance allowed)
3. Write PID to `/run/teslausb/archiveloop.pid`
4. Enter main loop

### Main Loop
```
forever:
  wait_for_archive_to_be_reachable()
    └─ polls archive-is-reachable.sh every N seconds
    └─ also returns if /tmp/archive_is_unreachable is touched (sync trigger)
  
  disconnect_usb_drives_from_host()
    └─ disable_gadget.sh, fsck cam_disk.bin
  
  ensure_cam_file_is_mounted()
  
  archive_teslacam_clips()    ← main work
    └─ build snapshot/overlay view of TeslaCam
    └─ archive enabled categories through the selected backend
       (SavedClips, SentryClips, TeslaTrackMode, RecentClips, in that order)
    └─ index eligible SavedClips + RecentClips telemetry after the copy
    └─ write archive_state file
    └─ send-push-message on start + finish
  
  wait_for_archive_to_be_unreachable()
    └─ archive is up but car is now active (consuming archive)
  
  connect_usb_drives_to_host()
    └─ enable_gadget.sh
```

### Key Functions

**`archive_teslacam_clips()`**
- Builds an overlay over the current snapshot before constructing the candidate list.
- Copies enabled `TeslaCam/{SavedClips,SentryClips,RecentClips,TeslaTrackMode}` categories through the selected archive backend.
- Copies in priority order: SavedClips, then SentryClips, then TeslaTrackMode, then RecentClips (then anything an `archive-filter` added). `split_archive_list()` writes one list per category and `archive-clips.sh` gets one `(source, list)` pair per category, so each is its own backend pass; a single rsync pass would sort the files and copy RecentClips first. If the car leaves mid-copy, the events are already archived. The log shows `Copy order: SavedClips N, SentryClips N, ...`.
- Saved and Sentry default to enabled; Recent defaults to disabled. The Settings page exposes all three dashcam category flags.
- Snapshots (how RecentClips outlive the car's rolling buffer, and the experimental `RECENTCLIPS_PRUNE_SNAPSHOTS` pruning in `make_snapshot.sh`) are described in [`snapshots.md`](snapshots.md).
- `filter_recent_cameras()` drops RecentClips files from cameras not listed in `RECENTCLIPS_CAMERAS` (space or comma separated; unset means every camera). Saved, Sentry and track mode files are never filtered, and files from cameras the list does not know are kept. Snapshots on the Pi still hold every camera, so telemetry indexing (front camera) is unaffected, and marking a clip archived counts any archived camera of that minute.
- Telemetry scans Saved and Recent independently of archive category toggles, excluding Sentry and Recent aliases of Saved/Sentry.
- **Index while driving (experimental, `TELEMETRY_WHILE_DRIVING`, needs `TELEMETRY_MAP_ENABLED`):** after every successful snapshot, `snapshotloop` starts `index_telemetry_after_snapshot()` in the background. It runs `telemetry_index.py index-batch --scan /mutable/TeslaCam --no-wait --budget <SNAPSHOT_INTERVAL/2>` (the same scan as the archive cycle) at `nice 19`/`ionice -c3`, with no archive reachability check. Both settings are read from the config file on each snapshot (`current_setting()`), so the Settings toggle applies without a restart. Runs that find nothing new log nothing; others log `Telemetry (after snapshot): ...`. All `telemetry_index.py` commands share a lock (`/mutable/telemetry/.lock`): a background run skips if another holds it (exit 75), the archive cycle waits for it. Each run stops starting clips once its budget is used, and the clips are read oldest first, since those are the first lost when snapshots are released.
- Reloads setup variables at each cycle boundary. Telemetry is indexed **after** copying, so a backlog of unindexed clips never delays the copy: `index_telemetry()` runs `index-batch --scan /mutable/TeslaCam`, which lists every eligible clip still on the Pi (`scan_tree()`: SavedClips events and RecentClips front files, Sentry excluded, archived or not) through the links in the lower dir, since copied files are hidden from the overlay. Clips recorded before the retention period (`TELEMETRY_RETENTION_DAYS`) are not read, and a RecentClips link is only followed (which mounts its snapshot) when its clip is new. The archive state shows `archiving: Indexing telemetry` meanwhile. All clips go to one `telemetry_index.py index-batch` process, which skips clips already indexed and logs a one-line summary (`Telemetry: N clips checked, M new, F failed in Ss`). Then `telemetry_index.py mark-listed` marks every clip in the cumulative archived list, which also covers clips indexed in a later cycle than the one that copied them, and `telemetry_index.py compact` packs each finished day (before today, or nothing recorded for an hour) into one track file and one map-line file (logged as `Telemetry: packed N day(s) ...`). Telemetry failure does not fail archiving.
- Writes `idle`, `archiving: <details>`, or `error: <details>` to `/run/teslausb/archive_state`.
- Calls `send-push-message` at archive start and finish.

**`wait_for_archive_to_be_reachable()`**  
- Polls `archive-is-reachable.sh` (tests SMB/NFS mount or rsync)
- Also checks if `/tmp/archive_is_unreachable` exists and was removed (sync trigger path)
- Sleep interval: `ARCHIVE_DELAY` env var (default 20s)

**`freespacemanager()`**  
- Monitors free space on the filesystem holding `cam_disk.bin`
- Runs `manage_free_space.sh` when free space falls below the reserve. It sends no notification

**`wifichecker()`**  
- Watches kernel messages for Wi-Fi firmware failures
- Reloads the Wi-Fi driver after repeated failures. It sends no notification

### Sync Trigger Mechanism
Touching `/tmp/archive_is_unreachable` signals archiveloop to skip waiting and run a fresh archive cycle immediately. Used by:
- UI Settings page (`HandleTriggerSync` → `os.Create("/tmp/archive_is_unreachable")`)
- Telegram bot `/sync` command (`Path("/tmp/archive_is_unreachable").touch()`)
- `HandleTriggerSync` in Go backend

### Archive State File
Path: `/run/teslausb/archive_state`  
Written by archiveloop; read by `Collector.readArchive()` in Go backend every 2s.

```
idle
archiving: copying SavedClips (3 events)
error: SMB mount failed
```

---

## send-push-message (`run/send-push-message`)

Called by archiveloop with: `send-push-message "$title" "$message" "$type"`  
`$type` is `start` or `finish` (used by some notifiers to decide what to send).

### Enabled Notifiers (all gated on `*_ENABLED=true` env var)

| Flag | Backend | Key env vars |
|------|---------|-------------|
| `SIGNAL_ENABLED` | Signal REST | `SIGNAL_URL`, `SIGNAL_FROM_NUM`, `SIGNAL_TO_NUM` |
| `PUSHOVER_ENABLED` | Pushover | `PUSHOVER_APP_KEY`, `PUSHOVER_USER_KEY` |
| `GOTIFY_ENABLED` | Gotify | `GOTIFY_DOMAIN`, `GOTIFY_APP_TOKEN`, `GOTIFY_PRIORITY` |
| `DISCORD_ENABLED` | Discord webhook | `DISCORD_WEBHOOK_URL` |
| `IFTTT_ENABLED` | IFTTT | `IFTTT_EVENT_NAME`, `IFTTT_KEY` |
| `SNS_ENABLED` | AWS SNS | `AWS_SNS_TOPIC_ARN` + AWS credentials |
| `MATRIX_ENABLED` | Matrix | `MATRIX_SERVER_URL`, `MATRIX_USERNAME`, `MATRIX_PASSWORD`, `MATRIX_ROOM` |
| `TELEGRAM_ENABLED` | Telegram Bot API | `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID` |
| `WEBHOOK_ENABLED` | Custom HTTP | `WEBHOOK_URL` |
| `SLACK_ENABLED` | Slack webhook | `SLACK_WEBHOOK_URL` |
| `NOTIFICATION_COMMAND_ENABLED` | Shell eval | `NOTIFICATION_COMMAND_START`, `NOTIFICATION_COMMAND_FINISH` |
| `NTFY_ENABLED` | ntfy | `NTFY_URL`, `NTFY_TOKEN`, `NTFY_PRIORITY` |

### Telegram-specific send_telegram() logic
- Builds JSON safely via `python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))"` (no injection risk)
- Supports comma-separated `TELEGRAM_CHAT_ID` (sends to each)
- If `TELEGRAM_SEND_CLIPS=true` AND `$type == "finish"`:  
  finds newest `.mp4` under `TeslaCam/SentryClips` newer than `/proc/1` (i.e. from this session)  
  sends via `sendVideo` if file < 50MB

---

## Telegram Bot (`run/teslausb_telegram_bot.py`)

Systemd service: `teslausb-telegram-bot.service`  
Config: env vars loaded from `/mutable/teslausb_telegram.env` via `EnvironmentFile` (with fallback to `/root/teslausb_setup_variables.conf`)

### Configuration

| Env var | Required | Notes |
|---------|----------|-------|
| `TELEGRAM_BOT_TOKEN` | Yes | From @BotFather |
| `TELEGRAM_CHAT_ID` | Yes | Comma-separated int list; acts as allowlist |
| `TELEGRAM_SILENT_NOTIFY` | No | `true` disables notification sound |
| `TELEGRAM_NEAR_ENABLED` | No | `true` enables `/near` and location pins (experimental, default off) |
| `BACKEND_URL` | No | Default `http://127.0.0.1:8080` |
| `MUTABLE_DIR` | No | Default `/mutable` |
| `TESLACAM_DIR` | No | Default `$MUTABLE_DIR/TeslaCam` |
| `ARCHIVE_MOUNT` | No | Default `/mnt/archive` |

### Commands

| Command | Implementation | Notes |
|---------|---------------|-------|
| `/status` | GET `$BACKEND_URL/api/status` → parse JSON | Falls back to `uptime -p` if API unavailable |
| `/freespace` | `df -h $CAM_DISK` and `df -h $ARCHIVE_MOUNT` | Shows "not mounted" if path missing |
| `/sdcard` | Reads `/sys/block/mmcblk0` and `/mutable/sdcard_*` | Capacity, writes, endurance threshold, and benchmark history |
| `/sync` | `Path("/tmp/archive_is_unreachable").touch()` | Triggers archive cycle |
| `/lastclip` | Finds newest .mp4 in SavedClips + SentryClips | Sends via `sendVideo` (50MB limit) |
| `/recent <n>` | Same as /lastclip, up to min(n, 5) | |
| `/restart` | `systemctl restart teslausb.service` | |
| `/near <lat>,<lon> [m]` or a shared location | GET `$BACKEND_URL/api/street/near` (default radius 100 m) | Experimental, `TELEGRAM_NEAR_ENABLED=true`. Lists the passes, newest first, with a 📷 button for those whose video is still on the Pi |
| 📷 button | GET `$BACKEND_URL/api/street/frame` → `sendPhoto` | Front camera by default, with buttons for the other cameras. If the backend reports no ffmpeg (503), the bot runs `install-ffmpeg.sh` once and retries |
| `/help` | Lists all commands | |

Button presses arrive as `callback_query` updates; their `callback_data` is `nf|<token>|<pass>|<camera>`, where the token points at the bot's in-memory list of the last 20 searches (Telegram limits callback data to 64 bytes). Frames show the street as recorded, including other people's number plates and faces, which is why the feature is opt-in.

`run/install-ffmpeg.sh` installs a static ffmpeg 7.0.2 (John Van Sickle's builds: arm64, armhf, or armel for the ARMv6 Pi Zero W) into `/backingfiles/tools`, checked against pinned SHA-256 hashes; the root filesystem stays read-only. `--check` reports whether it is there. When the upstream release moves on, the hash no longer matches and it refuses: update `VERSION` and the hashes. Measured: about 12 s and 49 MB on the Radxa; a frame takes about 0.4 s there and 3 s on a Pi Zero W.

### Security
- Any `update.message.chat.id` (or a button's `callback_query.message.chat.id`) not in `ALLOWED_IDS` → silently ignored + logged to journald
- Bot token in env var (never hardcoded)
- Service hardened: `NoNewPrivileges=yes`, `ProtectSystem=strict`, `ReadWritePaths=/mutable /backingfiles /mnt /tmp /run/teslausb`

### /status Response Format
```
🚗 TeslaUSB status
Archive: idle
Disk /mutable: 42% used
CPU temp: 55.0°C
WiFi: MyNetwork
Host uptime: up 2 hours, 30 minutes
```

---

## SD-Card Write Tools

`run/benchmark.sh` writes 160 MiB from `/dev/zero` with 4 MiB blocks and `oflag=dsync conv=fsync`. Every block waits for durable completion, the test has a 60-second timeout, and the last three results are stored in `/mutable/sdcard_benchmark.json`. `--check` never allocates or removes a scratch file. Each actual run allocates a unique scratch file beside `BENCHMARK_TEST_FILE` (a filename prefix) and removes only its own file. The web backend serializes benchmark runs and blocks them while archiving is active. Telegram `/sdcard` and the web UI read the same result files.

`tools/sustained-write-test.py` provides the complementary Tesla workload: four concurrent clips, approximately 36 MiB each per minute, one-second write pacing, and five one-minute cycles by default. It validates a mounted target, checks free space, leaves source clips unchanged, and removes only its generated test directory.

See [`sd-card-write-testing.md`](sd-card-write-testing.md) for commands, direct-I/O comparison, buffering semantics, units, and measured results.

---

## Config Backup/Restore (`run/teslausb-config`)

Installed to `/usr/local/sbin/teslausb-config` on the Pi by `configure-web.sh`.  
Must run as root.

### `teslausb-config backup [--dest DIR]`

Collects (from hard-coded paths):
| File | Always? |
|------|---------|
| `/root/teslausb_setup_variables.conf` | Yes |
| `/etc/nginx/auth/.htpasswd` | If present |
| `/root/.ssh/authorized_keys` | If present |
| `/etc/wpa_supplicant/wpa_supplicant.conf` | If present |
| `/root/teslausb_bt_key` | If present |
| `/mutable/teslausb_telegram.env` | If present |

Prompts for passphrase (twice, must match).  
Output: `teslausb_config_YYYYMMDD_HHMMSS.tar.gz.enc` in `--dest` dir (default `/mnt/archive`).  
Encryption: `openssl enc -aes-256-cbc -pbkdf2 -iter 100000`.  
File perms: `chmod 600`.

### `teslausb-config restore <file>`

1. Prompts for passphrase
2. Decrypts to tmpdir
3. Restores each file to original absolute path (`mkdir -p` parent dirs)
4. `chmod 600` each restored file
5. Prints reminder to `systemctl restart teslausb-backend nginx`

---

## Path Constants (`run/lib/paths.sh`)

Source this file in any shell script that needs path constants. Do not execute directly.

```bash
source run/lib/paths.sh
```

All paths are overridable via env vars (useful in tests):

| Var | Default |
|-----|---------|
| `MUTABLE_DIR` | `/mutable` |
| `BACKING_DIR` | `/backingfiles` |
| `SNAPSHOTS_DIR` | `$BACKING_DIR/snapshots` |
| `SNAP_MNT_BASE` | `/tmp/snapshots` |
| `CAM_MOUNT` | `/mnt/cam` |
| `ARCHIVE_MOUNT` | `/mnt/archive` |
| `MUSIC_ARCHIVE_MOUNT` | empty; set to `/mnt/musicarchive` when a music drive is configured |
| `LOG_FILE` | `$MUTABLE_DIR/archiveloop.log` |
| `CAM_DISK` | `$BACKING_DIR/cam_disk.bin` |
| `TESLACAM_DIR` | `$MUTABLE_DIR/TeslaCam` |

---

## Shell Conventions

All runtime shell scripts must:
```bash
#!/bin/bash -eu
set -o pipefail
```

- `set -e` — exit on first error
- `set -u` — exit on unset variable
- `set -o pipefail` — exit if any pipe segment fails

No `eval` in archiveloop or runtime scripts.  
`shellcheck --severity=warning --exclude=SC1091` must return zero warnings (enforced in CI).

---

## Setup Scripts

### Generic SBC installer

`setup/generic/install.sh` prepares non-prebuilt systems for the main setup flow. It defaults to the `pgodlews/dashvault` sources and accepts `SOURCE_DIR` as a local source tree. When the boot disk has less than 32 GiB unallocated, it calculates the root target as current usage plus 5 GiB, rounded up to whole GiB with an 8 GiB minimum. It verifies that the target fits and leaves 32 GiB for TeslaUSB before using the initramfs resize path. The root partition must be the last partition.

### `configure-web.sh`
Runs during initial Pi setup. Key actions:
1. `apt-get install nginx golang-go fuse libfuse-dev g++ net-tools wireless-tools ethtool zip`
2. Sets up `tmpfs` mounts for nginx logs/lib in `/etc/fstab`
3. Copies `teslausb-www/html/` (favicon, icons) to `/var/www/html/`
4. Copies `teslausb-www/go-backend/` to `/var/www/html/go-backend/`
5. Compiles cttseraser: `g++ -o /root/cttseraser cttseraser.cpp -lfuse`
6. Creates `/sbin/mount.ctts` wrapper, adds fstab entry for cttseraser FUSE mount
7. Builds Go backend: `cd go-backend && go build -o teslausb-backend`
8. Installs + enables `teslausb-backend.service` + `teslausb-backend.socket`
9. Uses the local React build from `teslausb-www/new/dist/` when available; otherwise downloads the UI release into `/var/www/html/`
10. Installs `teslausb-config` to `/usr/local/sbin/`
11. Configures nginx `www-data` sudo permissions (for diagnostics)

### `configure-webui-auth.sh`
Runs during initial Pi setup. Key actions:
1. Reads `WEBUI_USERNAME` / `WEBUI_PASSWORD` (or legacy `WEB_USERNAME` / `WEB_PASSWORD`)
2. If interactive and no env vars: prompts for username + password
3. If credentials provided: `apt-get install apache2-utils`, `htpasswd -bc .htpasswd`, `chmod 600 .htpasswd`
4. If no credentials: removes `.htpasswd`, sed-patches nginx config to `auth_basic off`

### `configure.sh` (Telegram section)
`check_and_configure_telegram()`:
- Checks `TELEGRAM_ENABLED=true`
- Validates `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` are set and non-default
- Exits with instructions if validation fails

`install_telegram_bot_service()`:
- Installs `teslausb-telegram-bot.service` to `/lib/systemd/system/`
- `systemctl daemon-reload && enable && start`
- Only runs if `TELEGRAM_ENABLED=true`

`install_push_message_scripts()`:
- Copies `run/send-push-message` → `/root/bin/`
- Copies `run/teslausb_telegram_bot.py` → `/root/bin/`
- Copies `run/lib/paths.sh` → `/root/bin/lib/`
