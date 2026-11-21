# Pi Backup & Restore

How to snapshot the Pi's working configuration (without dashcam clips) and restore it later.

---

## Quick backup

Run this from any machine with SSH access to the Pi. It streams the tar directly to your local machine so you don't need free space on the Pi's SD card.

```bash
ssh pi@dashvault.local '
    sudo tar czf - \
      --exclude="TeslaCam" \
      --exclude="snap-*/mnt/TeslaCam" \
      --exclude="snap-*/mnt/SavedClips" \
      --exclude="snap-*/mnt/SentryClips" \
      --exclude="*.log" \
      --exclude="*.log.*" \
      --exclude="/var/log" \
      --exclude="/tmp" \
      --exclude="/var/cache" \
      --exclude="/var/tmp" \
      --exclude="/backingfiles" \
      --exclude="/mutable/archiveloop.log" \
      --exclude="/mutable/audit.log" \
      --exclude="/run" \
      --exclude="/sys" \
      --exclude="/proc" \
      --exclude="/dev" \
      --exclude="/mnt" \
      --warning=no-file-changed \
      /root/teslausb_setup_variables.conf \
      /root/.teslaCamArchiveCredentials \
      /root/bin \
      /root/.ssh \
      /etc/nginx \
      /etc/systemd/system/teslausb* \
      /etc/fstab \
      /etc/sudoers.d \
      /var/www/html \
      /mutable \
      /usr/local/sbin/teslausb-config \
      /usr/local/sbin/teslausb_telegram_bot.py \
      2>/dev/null
  ' > teslausb-pi-snapshot-$(date +%Y%m%d_%H%M%S).tar.gz
```

**What you get (~6–8 MB):**

| Path | What it is |
|------|-----------|
| `/root/teslausb_setup_variables.conf` | Main configuration |
| `/root/.teslaCamArchiveCredentials` | CIFS/SMB archive credentials |
| `/root/bin/` | Runtime scripts (archiveloop, archive-clips, etc.) |
| `/root/.ssh/` | SSH authorized_keys |
| `/etc/nginx/` | nginx config + the active site symlink |
| `/etc/systemd/system/teslausb*` | All TeslaUSB systemd units |
| `/etc/fstab` | Mount configuration |
| `/etc/sudoers.d/` | sudo rules (www-data, pi, etc.) |
| `/var/www/html/` | Old UI + React PWA + Go backend binary |
| `/mutable/` | Mutable state (archive tracking, Telegram env, etc.) |
| `/usr/local/sbin/teslausb-config` | Config backup/restore tool |
| `/usr/local/sbin/teslausb_telegram_bot.py` | Telegram bot script |

**Excluded:**
- `/backingfiles/` — cam disk images and snapshots (the actual clips)
- `/mutable/TeslaCam/` — live camera recordings
- All log files, caches, temp files

---

## Restore

Copy the snapshot back to the Pi and extract it over the existing filesystem.

```bash
# 1. Copy snapshot to the Pi
scp teslausb-pi-snapshot-YYYYMMDD_HHMMSS.tar.gz pi@dashvault.local:/tmp/

# 2. SSH in and remount root read-write
ssh pi@dashvault.local
sudo mount -o remount,rw /

# 3. Extract the snapshot (preserves paths)
sudo tar xzf /tmp/teslausb-pi-snapshot-YYYYMMDD_HHMMSS.tar.gz -C /

# 4. Reload systemd and restart services
sudo systemctl daemon-reload
sudo systemctl restart nginx teslausb-backend teslausb-telegram-bot

# 5. (Optional) Verify nginx config and restart if needed
sudo rm -f /tmp/nginx-error.log /tmp/nginx-access.log
sudo systemctl restart nginx
```

---

## Built-in minimal backup

The Pi already has a smaller encrypted backup tool that only captures 5 files:

```bash
sudo teslausb-config backup --dest /mnt/archive
```

This creates an **AES-256-CBC encrypted** archive of:
- `/root/teslausb_setup_variables.conf`
- `/etc/nginx/auth/.htpasswd`
- `/root/.ssh/authorized_keys`
- `/etc/wpa_supplicant/wpa_supplicant.conf`
- `/root/teslausb_bt_key`

It does **not** include web UI files, Go backend, nginx configs, or systemd units — so it's not enough to restore a fully working appliance.

---

## Notes

- The root filesystem on TeslaUSB is mounted **read-only** by default to protect the SD card. Any restore or manual editing requires `sudo mount -o remount,rw /` first.
- The snapshot tar will warn about "file changed as we read it" — this is normal on a live system and can be ignored.
- After restore, the compiled Go backend binary is included, so you don't need to install Go or rebuild.
