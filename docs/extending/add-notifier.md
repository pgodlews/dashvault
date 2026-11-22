# Adding a New Notification Channel

`run/send-push-message` is the notification dispatcher. It is called by `archiveloop` at the start and end of every archive cycle, and by other runtime scripts for temperature alerts and music copy results.

This guide shows you how to add a new channel. No other file in the runtime needs to change.

---

## How it works

`send-push-message` receives three positional arguments:

```bash
send-push-message "$title" "$message" "$type"
# $type is "start", "finish", or empty
```

At the bottom of the script each notifier is called if its enable flag is set:

```bash
[ "${TELEGRAM_ENABLED:-false}"  = "true" ] && send_telegram
[ "${PUSHOVER_ENABLED:-false}"  = "true" ] && send_pushover
[ "${DISCORD_ENABLED:-false}"   = "true" ] && send_discord
# ... and so on for all 12 channels
```

Every channel follows the same pattern:

1. **A shell function** named `send_<name>()` — reads `$title`, `$message`, `$type` from the enclosing scope.
2. **An enable gate** at the bottom of the script — one line: `[ "${X_ENABLED:-false}" = "true" ] && send_x`.
3. **Env vars** declared in `/root/teslausb_setup_variables.conf` on the Pi.

The function has access to `log()` (writes to `$LOG_FILE`) and to all env vars sourced from `teslausb_setup_variables.conf`.

---

## Step-by-step: adding "Apprise" as an example

[Apprise](https://github.com/caronc/apprise) is a universal notification library with an HTTP API. This walkthrough adds it as channel 13.

### 1. Write the function

Open `run/send-push-message` and add the function before the final block of enable gates:

```bash
function send_apprise () {
  log "Sending Apprise notification."
  local url="${APPRISE_URL}"          # e.g. http://apprise.lan:8000
  local tags="${APPRISE_TAGS:-all}"   # optional tag filter

  # Build payload. Use python3 for safe JSON encoding — no injection risk.
  local body
  body=$(python3 -c "
import sys, json
title   = sys.argv[1]
message = sys.argv[2]
print(json.dumps({'title': title, 'body': message, 'tag': sys.argv[3].split(',')}))
" "$title" "$message" "$tags")

  curl -sf -X POST "${url}/notify" \
    -H "Content-Type: application/json" \
    -d "$body" \
    >> "$LOG_FILE" 2>&1 || log "Apprise notification failed"
}
```

Key rules:
- Always write curl errors to `$LOG_FILE` (not stderr — archiveloop captures stdout/stderr itself).
- Use `|| log "..."` rather than letting `set -e` kill the whole script on a network failure.
- Build JSON via `python3 -c "import json; ..."` rather than string interpolation. `$title` and `$message` can contain quotes, backslashes, and emoji.
- Use `$type` if you want to send different content on start vs. finish (e.g. attach a clip only on finish).

### 2. Add the enable gate

At the bottom of `run/send-push-message`, after the existing gates:

```bash
[ "${APPRISE_ENABLED:-false}" = "true" ] && send_apprise
```

### 3. Document the env vars

Add a section to `doc/ConfigureNotificationsForArchive.md` (the user-facing setup doc):

```markdown
### Apprise

Set these variables in `teslausb_setup_variables.conf`:

| Variable | Required | Example |
|----------|----------|---------|
| `APPRISE_ENABLED` | Yes | `true` |
| `APPRISE_URL` | Yes | `http://apprise.lan:8000` |
| `APPRISE_TAGS` | No | `teslausb` (default: `all`) |
```

### 4. (Optional) Add setup validation

If your channel requires tokens or URLs that are easy to misconfigure, add a check in `setup/pi/configure.sh` inside `check_and_configure_notifications()`. Follow the pattern used for Telegram:

```bash
if [ "${APPRISE_ENABLED:-false}" = "true" ]
then
  if [ -z "${APPRISE_URL:+x}" ]
  then
    log_progress "STOP: APPRISE_ENABLED=true but APPRISE_URL is not set."
    exit 1
  fi
fi
```

---

## Testing without a Pi

```bash
# Export minimum required vars and call the script directly:
export APPRISE_ENABLED=true
export APPRISE_URL=http://localhost:8000   # point at a local test server
export LOG_FILE=/tmp/test.log

bash run/send-push-message "TeslaUSB" "Test archive complete" "finish"
cat /tmp/test.log
```

For a local Apprise server: `docker run -p 8000:8000 caronc/apprise`.

To test a finish-only attachment (like Telegram's `TELEGRAM_SEND_CLIPS`):

```bash
export type=finish   # send-push-message sets this from $3; set it manually for testing
```

---

## The `$type` parameter

| Value | When sent | Typical use |
|-------|-----------|-------------|
| `start` | Archive cycle begins | "Archiving X clips starting at HH:MM" |
| `finish` | Archive cycle ends | "Archived X clips in 3m12s" |
| *(empty)* | Temperature alerts and music copy results | Short status message |

Only act on `$type` if your channel needs to behave differently (e.g. attaching a video only on finish). Most simple notifiers can ignore it.

---

## Existing channels as reference implementations

| Complexity | Channel | What to look at |
|-----------|---------|-----------------|
| Simplest | `send_discord` | Single curl POST, JSON body, no conditionals |
| Moderate | `send_webhook` | Same pattern, user-supplied URL |
| Complex | `send_telegram` | Multi-recipient loop, conditional video attach, SD health warning, python3 JSON |
| Shell eval | `send_notification_command` | Posts a JSON payload (`title`, `message`, `type`, `hostname`, `timestamp`) to `$NOTIFICATION_COMMAND_URL` |
