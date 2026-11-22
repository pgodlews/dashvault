# Go Backend Reference

Location: `teslausb-www/go-backend/`  
Binary: `/var/www/html/go-backend/teslausb-backend` (on the Pi)  
Service: `teslausb-backend.service` + `teslausb-backend.socket` (socket-activated)  
Listen: `127.0.0.1:8080` (proxied by nginx)

---

## File Map

| File | Purpose |
|------|---------|
| `main.go` | Entry point, route registration, socket activation, graceful shutdown |
| `collect.go` | `Collector` — reads proc/sys/statfs, emits `Status` struct |
| `sse.go` | `Broker` — fans status snapshots to SSE subscribers |
| `file_ops.go` | Handlers: ls, download, downloadzip, upload, rm, mkdir, cp, mv |
| `system_ops.go` | Handlers: reboot, trigger_sync, toggle_drives, config, BLE |
| `archive_mounts.go` | Validated archive fstab/credential transaction and shell reconciliation |
| `archive_config.go` | Multi-backend archive settings and per-backend selection |
| `telegram_config.go` | Atomic Telegram settings, service state, and test messages |
| `setup_ops.go` | First-run status and configuration-preserving setup apply |
| `benchmark_ops.go` | Serialized SD benchmark execution, history, and endurance threshold |
| `telemetry_ops.go` | Track history, per-clip playback data, retention, and map settings |
| `power_ops.go` | Temperature history logging, CPU power profiles, and dynamic thermal soft-throttling |
| `update_ops.go` | OTA status, check, apply, rollback, and logs |
| `log_ops.go` | Handlers: `/api/logs` snapshot + `/api/logs/stream` SSE (journalctl) |
| `csrf.go` | `issueCSRFCookie`, `requireCSRF` middleware |
| `ratelimit.go` | Per-IP auth failure tracker + lockout |
| `audit.go` | `auditMiddleware` — JSON audit entries to stderr + `/mutable/audit.log` |
| `helpers.go` | `GetDocumentRoot`, `GetSafePath`, `ParseURLArgs`, `uploadLimitBytes` |
| `go.mod` | Module: `github.com/pgodlews/dashvault/go-backend` |

---

## Route Table

Routes are registered in `main.go`. The complete stack is `authRateLimit(requireBasicAuth(...))`; nginx also enforces Basic Auth. Read endpoints issue the CSRF cookie, and mutations additionally require CSRF validation and write audit entries.

### Read endpoints — middleware: `issueCSRFCookie`

| Path | Handler | Description |
|------|---------|-------------|
| `/cgi-bin/ls.sh` | `HandleLs` | Directory listing (depth 2, with sizes) |
| `/cgi-bin/download.sh` | `HandleDownload` | Single file download via `http.ServeFile` |
| `/cgi-bin/downloadzip.sh` | `HandleDownloadZip` | Folder as streaming ZIP |
| `/cgi-bin/checkBLEstatus.sh` | `HandleCheckBLEStatus` | Runs `/root/bin/checkBLEstatus.sh` |
| `/cgi-bin/randomdata.sh` | `HandleRandomData` | Runs `/root/bin/randomdata.sh` |
| `/api/status` | `broker.ServeSnapshot` | Latest `Status` as JSON |
| `/api/auth/check` | `authGate` | nginx `auth_request` target: 204 for a correct password, 401 otherwise, 403 while the address is locked out after repeated wrong passwords. Handled outside the route chain |
| `/api/status/stream` | `broker.ServeSSE` | SSE stream — `data:` JSON every poll interval |
| `/api/logs` | `HandleLogSnapshot` | `journalctl -n N` as JSON array |
| `/api/logs/stream` | `HandleLogStream` | `journalctl -f` as SSE |
| `/api/config/archive` | `HandleArchiveConfig` | Read archive backends and active selection. The CIFS password is never returned; `cifs.password_set` says whether one is saved |
| `/api/config/telegram` | `HandleTelegramConfig` | Read redacted Telegram config and observed service state |
| `/api/config/rsync/ssh-key` | `HandleRsyncSSHKey` | Read the configured public key |
| `/api/wifi/networks`, `/api/wifi/scan` | WiFi handlers | List saved networks or scan with NetworkManager |
| `/api/setup/status` | `HandleSetupStatus` | Report first-run/completed setup state |
| `/api/benchmark/status`, `/api/benchmark/threshold` | Benchmark handlers | Read benchmark history and wear threshold |
| `/api/telemetry/tracks`, `/api/telemetry/clip`, `/api/telemetry/days`, `/api/telemetry/settings` | Telemetry handlers | Read retained tracks (optionally a date range of simplified lines), one clip, per-day driving totals, or map settings |
| `/api/power/config`, `/api/power/history` | Power handlers | Read power profile/thermal config, hardware capabilities, and temperature history |
| `/api/update/status`, `/api/update/log` | Update handlers | Read OTA state and log |

### Mutating endpoints — middleware: `requireCSRF → issueCSRFCookie → auditMiddleware`

| Path | Handler | Description |
|------|---------|-------------|
| `/cgi-bin/upload.sh` | `HandleUpload` | Write file to `DOCUMENT_ROOT`; body capped at `TESLAUSB_MAX_UPLOAD_MB` |
| `/cgi-bin/rm.sh` | `HandleRm` | `os.RemoveAll` (recursive) |
| `/cgi-bin/mkdir.sh` | `HandleMkdir` | `os.MkdirAll(path, 0755)` |
| `/cgi-bin/cp.sh` | `HandleCp` | Shells `cp -r` |
| `/cgi-bin/mv.sh` | `HandleMv` | `os.Rename` |
| `/cgi-bin/reboot.sh` | `HandleReboot` | `go func { sleep 2s; exec sudo reboot }()` |
| `/cgi-bin/toggledrives.sh` | `HandleToggleDrives` | Checks `/sys/kernel/config/usb_gadget/teslausb`, runs enable/disable_gadget.sh |
| `/cgi-bin/trigger_sync.sh` | `HandleTriggerSync` | `os.Create("/tmp/archive_is_unreachable")` |
| `/cgi-bin/pairBLEkey.sh` | `HandlePairBLEKey` | Runs `/root/bin/pairBLEkey.sh` |
| `/api/config/archive` | `HandleArchiveConfig` | Save backend configuration atomically; `recent_cameras` (omit to keep) maps to `RECENTCLIPS_CAMERAS`, empty when every camera is selected; GET also returns `detected_cameras` from clip file names under `/mutable/TeslaCam`. An empty `cifs.password` keeps the saved one; `cifs.clear_password: true` removes it. Values with control characters, or rsync/rclone names that could be read as command-line options, are rejected |
| `/api/config/telegram`, `/api/config/telegram/test`, `/api/config/telegram/find-chats` | Telegram handlers | Save config, reconcile service state, or send a test |
| `/api/config/rsync/keygen`, `/api/config/rsync/import-key` | rsync key handlers | Generate or import SSH keys |
| `/api/wifi/networks`, `/api/wifi/networks/{ssid}` | WiFi handlers | Add or delete NetworkManager profiles |
| `/api/setup/apply` | `HandleSetupApply` | Complete setup while preserving unrelated settings |
| `/api/benchmark/run`, `/api/benchmark/threshold` | Benchmark handlers | Run a serialized benchmark or set the wear threshold |
| `/api/telemetry/settings`, `/api/telemetry/clear` | Telemetry handlers | Save/prune settings or clear track history |
| `/api/street/near`, `/api/street/frame`, `/api/street/install-ffmpeg` | `telemetry_street.go` | Drives near a point, one camera's still at a moment, one-time ffmpeg install |
| `/api/power/config`, `/api/power/history/clear` | Power handlers | Save power profile / soft-throttle settings or clear temperature history |
| `/api/update/check`, `/api/update/apply`, `/api/update/rollback` | Update handlers | OTA operations |

---

## Middleware Stack

```
Incoming request
       │
       ▼
authRateLimit(mux)               ← ratelimit.go
  │  tracks failed Basic-Auth per IP; 429 on lockout
  │
  ├─ Read endpoints:
  │    issueCSRFCookie(handler)   ← csrf.go
  │      sets teslausb_csrf cookie if absent
  │      calls handler
  │
  └─ Mutating endpoints:
       requireCSRF(
         issueCSRFCookie(
           auditMiddleware(handler)  ← audit.go
         )
       )
       requireCSRF validates cookie == X-CSRF-Token header (constant-time)
       auditMiddleware records JSON entry after handler returns
```

---

## Key Data Structures

### Status (collect.go)
```go
type Status struct {
    CollectedAt time.Time   `json:"collected_at"`
    UptimeSecs  float64     `json:"uptime_seconds"`
    CPUTempC    float64     `json:"cpu_temp_c"`
    Disks       []DiskStat  `json:"disks"`
    WiFi        *WiFiInfo   `json:"wifi,omitempty"`
    Archive     ArchiveStat `json:"archive"`
}

type DiskStat struct {
    Mount   string  `json:"mount"`
    TotalGB float64 `json:"total_gb"`
    UsedGB  float64 `json:"used_gb"`
    FreeGB  float64 `json:"free_gb"`
    UsedPct float64 `json:"used_pct"`
}

type WiFiInfo struct {
    SSID      string `json:"ssid"`
    LinkLevel int    `json:"link_level"`   // 0–70 from /proc/net/wireless
    SignalDBm int    `json:"signal_dbm"`
    NoiseDBm  int    `json:"noise_dbm"`
}

type ArchiveStat struct {
    State   string `json:"state"`           // "idle"|"archiving"|"error"|"unknown"
    Message string `json:"message,omitempty"`
}
```

### Collector (collect.go)
```go
type Collector struct {
    ProcRoot    string   // "" in prod; override in tests to point at tmpdir
    DiskMounts  []string // default: ["/", "/mutable", "/backingfiles"]
    ArchiveFile string   // default: "/run/teslausb/archive_state"
}
```

`Collector.Collect()` reads:
- `/proc/uptime` → `UptimeSecs`
- `/sys/class/thermal/thermal_zone0/temp` (millidegrees) → `CPUTempC`
- `syscall.Statfs` for each `DiskMounts` entry → `Disks`
- `/proc/net/wireless` + `iwgetid <iface> -r` → `WiFi`
- `ArchiveFile` plain text → `Archive.State`

### Broker (sse.go)
- Polls `Collector.Collect()` every `poll` duration (default 2s, `-poll` flag in main)
- Maintains subscriber channel slice; each subscriber is a `chan Status`
- `ServeSSE` — opens SSE connection, sends immediate snapshot, then waits on channel
- `ServeSnapshot` — returns last snapshot as JSON (no SSE)
- Slow subscriber channels are dropped (non-blocking send)
- Run via `go broker.Run(ctx)` in main

---

## CSRF Details (csrf.go)

- Token: 32 random bytes → hex string → cookie value
- Cookie name: `teslausb_csrf`
- Cookie flags: `Path=/`, `SameSite=Strict`, **NOT** `HttpOnly` (JS must read it)
- Header: `X-CSRF-Token` must match cookie exactly (constant-time compare)
- Bypass: `TESLAUSB_DISABLE_CSRF=1` env var (for curl/API scripts)
- Read endpoints call `issueCSRFCookie` which sets cookie if missing, then delegates
- Mutating endpoints call `requireCSRF` which validates then delegates to `issueCSRFCookie(auditMiddleware(h))`

---

## Rate Limiting (ratelimit.go)

Tracks failed Basic-Auth responses per source IP in a `sync.Map`.

| Env var | Default | Meaning |
|---------|---------|---------|
| `TESLAUSB_RATE_LIMIT_ATTEMPTS` | 10 | Max failures in window before lockout |
| `TESLAUSB_RATE_LIMIT_WINDOW` | 60s | Rolling window duration |
| `TESLAUSB_RATE_LIMIT_LOCKOUT` | 300s | Lockout duration after threshold hit |

- Background goroutine GCs stale entries every 5 minutes
- Locked IPs get `429 Too Many Requests` immediately (no upstream call)
- `authRateLimit` wraps the entire mux in main.go

---

## Audit Log (audit.go)

Appended to both `stderr` (forwarded to journald by systemd) and `/mutable/audit.log`.

Example entry:
```json
{
  "time": "2026-04-25T12:00:00Z",
  "method": "POST",
  "path": "/cgi-bin/rm.sh",
  "args": ["TeslaCam/SavedClips", "2026-04-01_front.mp4"],
  "source_ip": "192.168.1.10",
  "user": "admin",
  "status": 200,
  "duration_ms": 12
}
```

Query param redaction: any param named `password`, `token`, or `api_key` → `***redacted***`.

---

## Log API (log_ops.go)

**Unit whitelist** — maps UI filter name → journald unit:

| UI name | journald `-u` arg |
|---------|-----------------|
| `backend` | `teslausb-backend` |
| `archive` | `archiveloop` |
| `nginx` | `nginx` |
| `kernel` | `kernel` (uses `-k` flag) |
| (empty / "all") | no `-u` filter |

`/api/logs?unit=backend&n=100` — snapshot; `n` clamped to 2000 max.  
`/api/logs/stream?unit=archive` — SSE; each `journalctl -f` line → one SSE event.

If `journalctl` is not available (non-Pi dev machine), both handlers return a graceful empty/fallback response.

---

## File Operations Details (file_ops.go)

### Path Safety
All ops call `GetSafePath(args)` from `helpers.go`:
1. Join args with `filepath.Join`
2. `filepath.Clean` result
3. Reject if result does not start with `filepath.Clean(GetDocumentRoot())`
4. → 400 Bad Request on traversal attempt

### URL Arg Format
The frontend uses a non-standard query string format: `?base/&subpath&filename`.  
`ParseURLArgs(r)` splits `r.URL.RawQuery` on `&` and URL-decodes each part.

### HandleLs Output Format (text/plain)
```
d:SavedClips                 # directory at depth 1
D:SavedClips/2026-04-01      # directory at depth 2
f:front.mp4:1234567          # file: name:size_bytes
s:10737418240:107374182400   # free_bytes:total_bytes (disk stats)
```

### Upload Size Limit
`TESLAUSB_MAX_UPLOAD_MB` env var (default 2048 = 2 GB).  
Enforced via `http.MaxBytesReader`. On overflow: 413 + JSON error + partial file deleted.

---

## Environment Variables

| Var | Default | Effect |
|-----|---------|--------|
| `DOCUMENT_ROOT` | `/var/www/html` | Base dir for all file ops |
| `PORT` | `8080` | Listen port (ignored when socket-activated) |
| `TESLAUSB_MAX_UPLOAD_MB` | `2048` | Upload body size cap |
| `TESLAUSB_DISABLE_CSRF` | unset | Set to `1` to skip CSRF validation |
| `TESLAUSB_RATE_LIMIT_ATTEMPTS` | `10` | Auth failures before lockout |
| `TESLAUSB_RATE_LIMIT_WINDOW` | `60s` | Failure rolling window |
| `TESLAUSB_RATE_LIMIT_LOCKOUT` | `300s` | Lockout duration |
| `LISTEN_FDS` | unset | Set to `1` by systemd for socket activation (uses fd 3) |

---

## Adding a New Route

1. Write handler function in the appropriate `*_ops.go` file.
2. Register in `main.go` — choose `read(handler)` or `mutate(handler)`.
3. If mutating: handler will automatically get CSRF validation + audit logging.
4. Write test in `*_test.go` using `httptest.NewRequest` / `httptest.NewRecorder`.
5. For new data in `/api/status`: add field to `Status` struct in `collect.go`, populate in `Collector.Collect()`, add test in `collect_test.go`.


## GPS Telemetry API

- `GET /api/telemetry/tracks` — enabled state, retention, and all retained tracks with full points (kept for older clients).
- `GET /api/telemetry/tracks?from=YYYY-MM-DD[THH:MM]&to=...` — clips recorded in `[from, to)` (the car's local time, as in the file names), each with a simplified `line` of `[lat, lon]` pairs instead of the full `track`: precomputed for packed days, simplified on the fly (Douglas-Peucker 5 m, a point at least every 30 s) for today's raw tracks. Only the day files in the range are read. With `full=1` (and both bounds at most 48 hours apart) each clip has its full `track` instead, for trip replay.
- `GET /api/telemetry/days` — per day: clips with GPS, driving seconds, first/last clip, driving minutes per hour, Saved/Recent counts; plus total clips and store size in bytes. Reads day files only.
- `GET /api/telemetry/clip?id=SavedClips/event` — one clip's track for the viewer.
- `GET /api/telemetry/settings` — feature enabled state and retention days.
- `POST /api/telemetry/settings` — update the toggle or retention (CSRF required).
- `GET /api/street/near?lat=&lon=[&radius=m][&limit=n]` — passes within the radius (default 100 m, max 5 km), newest first: consecutive minutes of one drive are merged to their closest moment (`id`, `at`, `t`, `distance_m`, `speed_kmh`, `heading`, `lat`, `lon`, `cameras` whose video is still on the Pi), plus `ffmpeg` (whether stills can be made). Clip bounding boxes (`bbox`, written by the indexer) skip far clips without reading their tracks. Needs the GPS track history on.
- `GET /api/street/frame?id=&camera=&t=[&w=320|480|640|960]` — one JPEG from the static ffmpeg (`/backingfiles/tools/ffmpeg/ffmpeg`), `t` clamped to the clip; at most one ffmpeg per CPU core, a 24 MB cache in `/tmp/teslausb-frames`. 404 when the clip or that camera's video is gone, 503 `{"install": true}` without ffmpeg.
- `POST /api/street/install-ffmpeg` — runs `run/install-ffmpeg.sh` (CSRF required).
- `POST /api/telemetry/clear` — delete telemetry history without deleting clips (CSRF required). Takes the indexer's lock (`/mutable/telemetry/.lock`, waiting up to 60 s) so a running indexer cannot write tracks back.

`run/telemetry_index.py` extracts front-camera SEI data after archiving (and, with `TELEMETRY_WHILE_DRIVING`, after every snapshot) and stores Float32 tracks under `/mutable/telemetry`: one metadata file per day in `days/`, raw per-clip tracks in `tracks/` until the day is packed into `days/<day>.<gen>.f32` plus `.line`. `telemetry_store.go` reads both, and the legacy `clips.json` while it still exists; `tests/python/test_telemetry_contract.py` checks that the backend reads a store the indexer wrote. The default is disabled, with 30-day retention. Settings writes preserve unrelated configuration and trigger immediate pruning; clear removes telemetry only, not clips.

Tracks support SavedClips and RecentClips, with SentryClips excluded. Responses distinguish `available`, `archived`, and `overwritten`; only available local clips play. Settings expose both `TELEMETRY_TILE_URL` and optional `TELEMETRY_MAP_STYLE_URL`, validated as HTTP(S) URLs. Track metadata and blobs remain on the Pi, while the browser contacts the configured map server. NAS playback is not provided.

Archive settings saves stop the archive loop and unmount its previous destinations through `reconfigure-archive.sh` before committing fstab, credentials, and setup keys. Missing CIFS/NFS mount helpers are installed inside the rootfs transaction. Busy unmounts abort without changing configuration; write failures restore files and leave archiving stopped. Service reconciliation failures return HTTP 500. Unrelated fstab entries and setup keys are preserved. CIFS credentials are created with mode 0600 even when switching from a no-archive installation.

OTA reads `OTA_ENABLED` and `OTA_RELEASE_URL` from `teslausb_setup_variables.conf`; explicitly supplied service environment values take precedence, including an empty URL or disabled value. The shell-format file is parsed as configuration and is not executed by the backend.

Before installing, the backend requires `SHA256SUMS.minisig` to be a valid minisign signature of `SHA256SUMS` by the key embedded from `release-signing.pub`, with the trusted comment `dashvault-release <tag>`. It then checks the archive against the signed checksum and its `VERSION` against the tag. A release that is unsigned, altered, made for another tag, or not newer than the installed version is refused. Rollback restores a local backup and needs no signature.

At startup the backend invokes `ota_finalize.sh` when installed. It handles releases applied by older installers that copied only `bin/`, backing up and upgrading the effective Telegram unit after acquiring the shared rootfs transaction lock. A migration failure is logged to journald; an already matching unit is a no-op.

## Power & Thermal API

Implemented in `power_ops.go`. Settings persist in `/mutable/power_config.json`.

| Setting | Default | Meaning |
|---|---|---|
| `temp_logging_enabled` | `true` | Record CPU (and GPU, when the SoC exposes a `*gpu*` thermal zone) temperature, CPU clock, governor and archive state. |
| `power_profile` | `"system"` | `system` = unmanaged: the backend never writes to sysfs and archiveloop's `CPU_GOVERNOR` applies. Other values: `auto`, `powersave`, `conservative`, `schedutil`, `ondemand`, `interactive`, `performance`. |
| `dynamic_throttle_enabled` | `false` | Two-stage soft-throttling ahead of the hardware throttle point. |
| `warn_temp_c` / `crit_temp_c` / `hysteresis_c` | `68` / `75` / `4` | Stage 1 caps `scaling_max_freq` to the middle available step; Stage 2 forces `powersave` at the minimum step. Each stage releases only after cooling `hysteresis_c` below its threshold. |

- `GET /api/power/config` → `{config, capabilities, status}`; capabilities list available governors/frequencies, `has_devfreq`, `has_gpu_temp`.
- `POST /api/power/config` — save and apply immediately (CSRF required). Invalid values are normalised (e.g. `crit_temp_c` is forced above `warn_temp_c`).
- `GET /api/power/history` — merged on-disk (1-min, 24 h) and in-memory (15 s, 1 h) samples plus min/avg/max.
- `POST /api/power/history/clear` — delete the history (CSRF required).

Behaviour notes:

- **Sampling** piggybacks on the status collector tick. The on-disk log `/mutable/thermal_history.jsonl` is append-only (~150 B/min) and only compacted when it exceeds 2× the 1,440-sample cap, keeping SD-card writes negligible.
- **Baseline restore**: before the first sysfs write, the current governor, `scaling_max_freq` and devfreq governors are snapshotted to `/mutable/power_baseline.json`. Switching back to `system` (with soft-throttling off) restores that snapshot exactly and deletes it.
- **`auto`** uses `powersave` (and devfreq `powersave` where present) while recording/idle and the pre-existing default while `archive.state == "archiving"`.
- Governors the kernel does not offer are mapped to the closest available one (`conservative` → `ondemand` → `schedutil` → `powersave`).
- The existing `temperature_monitor` script (push notifications on `TEMPERATURE_WARNING`/`TEMPERATURE_CAUTION`) is independent and unchanged.
