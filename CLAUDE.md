# CLAUDE.md — teslausb fork

AI agent and developer orientation. Read this file first, then follow links into `docs/` for depth.

---

## What this is

A fork of [marcone/teslausb](https://github.com/marcone/teslausb) that turns a Raspberry Pi into a Tesla dashcam archiver with a modern web UI and Telegram bot. The Pi presents a USB mass storage device to the car, periodically detaches it, copies clips to a network share, then re-attaches — all while serving a React PWA and a Go HTTP API.

---

## Tech stack by layer

| Layer | Technology | Location |
|-------|-----------|----------|
| Archive daemon | Bash, runs as root | `run/archiveloop` |
| Notification dispatcher | Bash + Python snippets | `run/send-push-message` |
| Telegram bot | Python 3 (stdlib only — keep it that way) | `run/teslausb_telegram_bot.py` |
| HTTP backend | Go 1.22+ | `teslausb-www/go-backend/` |
| Web UI | React 19 + Vite 8 + TanStack Router + Tailwind v4 | `teslausb-www/new/src/` |
| Reverse proxy | nginx | `teslausb-www/teslausb.nginx` |
| Pi setup | Bash | `setup/pi/` |
| Task runner | `just` | `Justfile` |
| Python tooling | `uv` + `ruff` + `mypy` + `pytest` | `pyproject.toml` |

---

## Directory map

```
run/
  archiveloop              Main daemon: USB gadget ↔ archive loop (bash, root)
  send-push-message        Notification dispatcher; add new channels here
  teslausb_telegram_bot.py Inbound Telegram bot
  teslausb-config          Backup / restore Pi config (encrypted tarball)
  lib/paths.sh             Single source of truth for all filesystem paths
  lib/dependencies.txt     External tools each feature/backend needs (read by check-deps.sh)
  check-deps.sh            Checks (and with --install, installs) dependencies.txt entries
  <name>_archive/          One directory per archive backend (cifs, nfs, rsync, rclone, none)
    archive-is-reachable.sh
    connect-archive.sh
    disconnect-archive.sh
    archive-clips.sh
    verify-and-configure-archive.sh
    copy-music.sh          (optional — cifs/nfs only)

setup/pi/
  configure.sh             Main Pi setup orchestrator — registers archive backends here
  configure-web.sh         Installs nginx, Go backend, cttseraser FUSE
  configure-webui-auth.sh  Creates .htpasswd, configures nginx auth
  envsetup.sh              Sources teslausb_setup_variables.conf, sets defaults
  teslausb-telegram-bot.service  systemd unit

teslausb-www/
  go-backend/              Go HTTP server source + tests
    main.go                Route registration, socket activation
    collect.go             Status collector (reads /proc, /sys, archive_state)
    sse.go                 SSE broker
    file_ops.go            File handlers (ls, download, upload, rm, …)
    system_ops.go          System handlers (reboot, trigger_sync, …)
    csrf.go / ratelimit.go / audit.go  Middleware
  new/src/                 React PWA
    routes/                One file per page (index, viewer, clips, logs, settings, setup)
    lib/api.ts             apiFetch() wrapper (CSRF-aware)
    hooks/useStatus.ts     SSE status subscription
  teslausb.nginx           nginx config template
  teslausb-backend.service systemd unit
  teslausb-backend.socket  socket activation

docs/
  index.md                 Documentation index
  architecture.md          System diagram, key concepts, data flows
  shell-layer.md           archiveloop, send-push-message, Telegram bot deep dive
  go-backend.md            Go backend routes, middleware, data structures
  new-ui.md                React PWA routes, components, API patterns
  dev-workflow.md          CI, testing, branch conventions, gh CLI
  extending/
    add-notifier.md        How to add a new notification channel
    add-archive-backend.md How to add a new archive backend

tests/
  python/                  pytest — bot + config backup tests
  harness/                 Docker integration harness (fake-pi + samba)
```

---

## Extension points

Two areas are designed to be forked and extended. Both have step-by-step guides:

- **New notification channel** → `docs/extending/add-notifier.md`
- **New archive backend** → `docs/extending/add-archive-backend.md`

---

## Dev commands

```bash
just bootstrap      # install uv, pre-commit, hooks (once per machine)
just ci             # full local CI: lint + test + test-backend + build-ui + harness-smoke
just lint           # shellcheck + ruff + gitleaks
just test           # Python pytest only
just test-backend   # Go vet + test -race
just build-ui       # pnpm build for the React PWA
just harness-smoke  # Docker integration smoke test (no Pi needed)
just harness-shell  # shell into the fake-pi container
```

---

## Key runtime files on the Pi

| Path | Description |
|------|-------------|
| `/root/teslausb_setup_variables.conf` | All config: env vars sourced by archiveloop + setup scripts |
| `/root/bin/` | Runtime scripts (installed from `run/` by setup) |
| `/run/teslausb/archive_state` | Plain-text state written by archiveloop; read by Go backend |
| `/tmp/archive_is_unreachable` | Touch this to trigger an immediate archive cycle |
| `/mutable/TeslaCam/` | Live camera recordings (tmpfs-backed, survives USB disconnect) |
| `/mutable/archiveloop.log` | Archiveloop log (capped at 1 MB, tail-rotated) |
| `/mutable/audit.log` | Go backend audit trail (JSON, one entry per mutating request) |
| `/backingfiles/cam_disk.bin` | FAT image presented as USB drive to the car |
| `/var/www/html/new/` | React PWA build output |

---

## Hard constraints

- **Never run CI against a real Pi.** Use the Docker harness.
- **No `eval` in `run/archiveloop` or other runtime scripts.** shellcheck enforces this.
- **All shell scripts must start with `#!/bin/bash -eu` + `set -o pipefail`.**
- **Do not port archiveloop to Go.** It owns USB gadget hardware interactions that belong in shell.
- **`just ci` must be green before pushing** to `main`.
- **Secrets never committed.** `/root/teslausb_setup_variables.conf` lives only on the Pi.
- Trunk branch is `main`. Topic branches: `fix/*`, `feat/*`, `chore/*`, `refactor/*`.

---

## Key data flows (quick reference)

```
Archive cycle:
  archiveloop → connect-archive.sh → archive-clips.sh
              → send-push-message "start" → (all enabled notifiers fire)
              → (copy complete)
              → send-push-message "finish"
              → disconnect-archive.sh

Status in the UI:
  Go Collector reads /proc + archive_state every 2s
  → SSE Broker fans to subscribers
  → useStatus() hook → Dashboard tiles re-render

Sync trigger (UI button or Telegram /sync):
  touch /tmp/archive_is_unreachable
  → archiveloop.wait_for_archive_to_be_reachable() breaks out of wait
  → immediate archive cycle
```

---

## Where to look first

| Task | Start here |
|------|-----------|
| Add a notification channel | `docs/extending/add-notifier.md` |
| Add an archive backend | `docs/extending/add-archive-backend.md` |
| Add a Go API route | `docs/go-backend.md` → "Adding a New Route" |
| Add a UI page | `docs/new-ui.md` |
| Understand the full architecture | `docs/architecture.md` |
