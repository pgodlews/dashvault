# Dev Workflow Reference

How to work on this repo. For AI agents picking up a task.

---

## Environment Requirements

| Tool | Version | Purpose |
|------|---------|---------|
| Docker | any | Integration harness (`just harness-*`) |
| [just](https://github.com/casey/just) | any | Task runner |
| [uv](https://github.com/astral-sh/uv) | any | Python dependency management |
| Go | 1.22+ | Go backend (`teslausb-www/go-backend/`) |
| Node.js | 20 | New UI (`teslausb-www/new/`) |
| pnpm | 10 | New UI package manager |
| shellcheck | any | Shell linting |
| gitleaks | any | Secret scanning |
| bash | ≥ 4 | Some scripts use bash 4 features |

Bootstrap a fresh host:
```bash
git clone https://github.com/pgodlews/dashvault.git
cd dashvault
just bootstrap
```

---

## Justfile Commands

Run from repo root. `just ci` is the gate — must be green before pushing.

| Command | What it runs |
|---------|-------------|
| `just bootstrap` | `uv sync`, pre-commit install |
| `just ci` | Full pipeline: lint → test → test-backend → build-ui → test-ui → harness-smoke |
| `just lint` | All linters (shell + python + secrets + nginx-auth) |
| `just lint-shell` | shellcheck on `run/`, `setup/`, `tools/` shell scripts |
| `just lint-python` | ruff check/format + mypy (strict) |
| `just lint-secrets` | gitleaks on full repo |
| `just lint-nginx-auth` | Grep check: asserts no `auth_basic off` in `teslausb-www/` |
| `just fmt` | Auto-format: ruff format + shfmt |
| `just test` | `uv run pytest` |
| `just test-backend` | `go vet ./... && go test ./...` in `go-backend/` |
| `just build-ui` | `pnpm install && pnpm build` in `new/` |
| `just build-backend` | Cross-compile Go for linux/arm64 |
| `just harness-up` | `docker-compose up -d` (integration harness) |
| `just harness-down` | `docker-compose down` |
| `just harness-smoke` | Run smoke tests against harness |
| `just harness-shell` | Shell into harness container |

The harness-smoke target requires Docker. It fails locally if Docker is absent — this is expected and is non-blocking in local dev. CI runs it.

---

## Python Tooling

All Python tooling is configured in `pyproject.toml`.

### Running tests
```bash
uv run pytest                          # all tests
uv run pytest tests/python/test_X.py  # specific file
uv run pytest -v -k "test_status"      # by name
```

Test location: `tests/python/`. Files named `test_*.py`.  
Coverage report printed to terminal after each run (sources: `run/`).

### Type checking
```bash
uv run mypy run/teslausb_telegram_bot.py --ignore-missing-imports
```

Strict mode is enabled. All `run/*.py` files must pass.  
Common overrides needed: `requests`, `teslapy`, `nio` have missing stubs → `ignore_missing_imports = true` in `pyproject.toml`.

### Linting
```bash
uv run ruff check run/ tests/          # lint
uv run ruff format run/ tests/         # auto-format
```

Line length: 100. Target: Python 3.11.

---

## Go Tooling

```bash
cd teslausb-www/go-backend

go build ./...                # build
go test ./... -v              # test with verbose output
go vet ./...                  # static analysis
go test -run TestCSRF ./...   # specific test
```

Tests use `net/http/httptest`. No external services needed.  
Module: `github.com/pgodlews/dashvault/go-backend`

Cross-compile for the three supported boards. `just build-backend` still writes the arm64 binary used for a Radxa copy. A release contains all three, and CI runs each one under QEMU user-mode (`tests/qemu-backend.sh`):

| Board | `uname -m` | Binary |
| --- | --- | --- |
| Radxa Zero 3 | `aarch64` | `teslausb-backend.arm64` |
| Pi Zero 2 W | `armv7l` | `teslausb-backend.armv7` |
| Pi Zero W | `armv6l` | `teslausb-backend.armv6` |

The armv7 binary does not run on the original Zero. OTA selects the file from `uname -m`.

---

## Branch Conventions

| Prefix | Use for |
|--------|---------|
| `fix/` | Bug fixes |
| `feat/` | New features |
| `chore/` | Tooling, deps, CI |
| `refactor/` | Code restructure with no behavior change |
| `docs/` | Documentation only |

Trunk: `main`. PRs must target `main`.

---

## Commit Message Format

Conventional Commits, enforced by convention (no hook yet):

```
type(scope): short description

Optional longer body.

```

AI assistance may be credited with a `Co-Authored-By` trailer.

Types: `fix`, `feat`, `chore`, `refactor`, `docs`, `test`  
Scopes: `webui`, `telegram`, `ci`, `shell`, `go`, `ui`, `pi`, `security`

One logical change per commit. One topic per PR.

---

## PR Workflow

Open pull requests against `main`. With the GitHub CLI:

```bash
gh pr create -R pgodlews/dashvault --base main --head feat/my-branch --title "..." --body "..."
gh pr list -R pgodlews/dashvault
gh pr merge -R pgodlews/dashvault <N> --squash
```

---

## CI Pipeline

Workflow definitions live in `.github/workflows/` and run on GitHub.

| Area | What it checks |
|------|----------------|
| Shell | ShellCheck and script checks; Bash 4+ is required locally |
| Python | Ruff/mypy policy plus blocking pytest tests |
| Go | `go vet` and race-enabled Go tests |
| Frontend | TypeScript, ESLint, and production build |
| Harness | Docker fake-Pi/Samba smoke test |
| Secrets | Gitleaks |

For physical SD-card validation, follow [`sd-card-write-testing.md`](sd-card-write-testing.md).

---

## Testing Strategy

### Shell Scripts
- Subprocess tests in Python (`tests/python/`) using `subprocess.run`
- Fake Pi filesystem in `tmp_path` fixtures
- Path constants overridable via env vars (`SETUP_VARS`, `HTPASSWD`, etc.)

### Go Backend
- `httptest.NewRequest` + `httptest.NewRecorder` — no real HTTP server needed
- `Collector` struct has `ProcRoot` + `ArchiveFile` fields for test overrides
- Create fake `/proc/uptime`, `/sys/class/thermal/...` files in `t.TempDir()`

### Python Bot / Config Scripts
- Mock `requests.post` / `urllib.request.urlopen` with `unittest.mock.patch`
- `monkeypatch.setattr(bot, "Path", fake_path)` to redirect sentinel file creation
- `importlib.reload(m)` in the `bot` fixture to re-read env vars between tests

### Adding Tests
- New Python test file: `tests/python/test_<module>.py`
- New Go test file: `teslausb-www/go-backend/<topic>_test.go`
- Run `just test` (Python) or `just test-backend` (Go) to verify

---

## Deployment to Pi

**Pi address**: `pi@dashvault.local` by default  
**Rule**: Never run CI against a real Pi.

Manual deployment requires an explicit target and preserves existing configuration. The Pi root filesystem is normally read-only:

```bash
sudo /root/bin/remountfs_rw
# Install the intended scripts, backend, or built UI.
sudo systemctl restart teslausb-backend  # only when the backend changed
sudo systemctl restart nginx             # only when nginx/config changed
sudo mount / -o remount,ro
```

For UI changes, run `just build-ui` and copy `teslausb-www/new/dist/` to `/var/www/html/new/`. For backend changes, use the `dist/teslausb-backend` produced by `just build-backend`. Verify service health and confirm `/` returned to read-only. Do not change live credentials as a side effect of deployment.

---

## Key Config File (on Pi)

`/root/teslausb_setup_variables.conf` — primary shell-format configuration, sourced by archiveloop and parsed by backend configuration readers. Telegram uses a separate systemd-format environment file.

Important vars for current features:

```bash
# Telegram
export TELEGRAM_ENABLED=true
export TELEGRAM_BOT_TOKEN=123456789:ABCdef...
export TELEGRAM_CHAT_ID=111222333
export TELEGRAM_SILENT_NOTIFY=false
export TELEGRAM_SEND_CLIPS=true

# Archive
export ARCHIVE_SYSTEM=cifs
export ARCHIVE_SERVER=nas.local
export SHARE_NAME=tesla/dashcam
export SHARE_USER=tesla
export SHARE_PASSWORD=...

# Web UI
export WEBUI_USERNAME=admin
export WEBUI_PASSWORD=...

# Notification title
export NOTIFICATION_TITLE=teslausb
```

---

## Common Tasks Quick Reference

**Run all checks before pushing:**
```bash
just ci
```

**Add a new Go API endpoint:**
1. Write handler in `*_ops.go`
2. Register in `main.go` with `read()` or `mutate()`
3. Write test in `*_test.go`
4. If it changes `/api/status` shape: update `Status` struct + TypeScript interface

**Add a new UI route:**
1. Create `teslausb-www/new/src/routes/name.tsx`
2. Add `<Link>` in `__root.tsx`
3. `pnpm build` regenerates `routeTree.gen.ts`

**Add a new notification channel:**
1. Add `send_X()` function in `run/send-push-message`
2. Add `[ "${X_ENABLED:-false}" = "true" ] && send_X` at bottom
3. Document env vars needed
4. Add validation in `configure.sh` if needed

**Fix a shellcheck warning:**
```bash
shellcheck --severity=warning --exclude=SC1091 run/myscript.sh
```

**Check CI status for a commit:**
```bash
gh run list -R pgodlews/dashvault --branch main --limit 5
```
