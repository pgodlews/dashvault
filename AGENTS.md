# TeslaUSB Agent Guide

This file applies to the entire repository. Read `docs/architecture.md` before changing runtime behavior and the relevant component guide before editing a subsystem.

## Project Shape

- `run/`: root-owned Pi runtime, archive backends, telemetry extraction, notifications, Telegram, and SD-card tools.
- `setup/` and `pi-gen-sources/`: installation and image-build inputs.
- `teslausb-www/go-backend/`: authenticated Go HTTP service.
- `teslausb-www/new/`: React 19, TypeScript, Vite, TanStack Router, Tailwind, and PWA frontend.
- `tests/python/`: pytest unit and subprocess tests.
- `tests/harness/`: Docker fake-Pi/Samba integration environment.
- `docs/`: architecture, component references, and operational guides.

The archive/USB layer intentionally remains shell. The Go service owns HTTP APIs, not mount or gadget orchestration.

## Required Checks

Run the full gate before pushing when the host has Docker:

```bash
just ci
```

On macOS, `check.sh` requires Bash 4 or newer. If `/bin/bash` is version 3, run:

```bash
/opt/homebrew/bin/bash ./check.sh
```

Useful focused checks:

```bash
uv run pytest
uv run ruff check <changed-python-files>
uv run ruff format --check <changed-python-files>
uv run mypy <changed-python-files>

cd teslausb-www/go-backend
go vet ./...
go test -race -count=1 ./...

cd ../new
pnpm install --frozen-lockfile
pnpm typecheck
pnpm lint
pnpm build
```

Run `gitleaks detect --no-git --config .gitleaks.toml --verbose` before committing. Some repository-wide Ruff findings are legacy and non-blocking in the current Justfile; do not rewrite unrelated legacy files just to make that output empty.

If Docker is unavailable, complete all non-Docker checks and state explicitly that the harness was not run.

## Change Conventions

- Add tests when changing behavior; follow existing pytest and Go `httptest` patterns.
- Keep shell strict mode: `#!/bin/bash -eu` plus `set -o pipefail`.
- Use `run/lib/paths.sh` and existing environment overrides rather than duplicating path constants in shell code.
- Use `apiFetch()` for frontend mutations. Register backend reads with `read()` and mutations with `mutate()` so Basic Auth, CSRF, rate limiting, and audit behavior remain intact.
- Preserve unrelated setup keys and use the existing atomic-write helpers for live configuration.
- Do not manually edit `teslausb-www/new/src/routeTree.gen.ts`; regenerate it through the Vite build/dev tooling.
- Do not commit `teslausb-www/new/dist/`, local configuration, credentials, tokens, or generated backend binaries.
- Update affected documentation and diagrams in the same change. Mermaid blocks are used in `docs/architecture.md`.

## Git and Remotes

- Trunk: `main`.
- `origin`: `pgodlews/dashvault` on GitHub, the public repository and release host.
- Dashvault is based on `marcone/teslausb`, but the public repository does not share its history.
- Use topic branches and Conventional Commits. Delete only branches confirmed merged; preserve unmerged work and other worktrees.
- Check `git status` before and after builds/tests. Do not overwrite or commit unrelated user changes.

## Real Pi and Hardware Safety

A real Pi is a deployment target, never a CI worker. Do not change its credentials or network configuration unless explicitly requested.

The root filesystem is normally read-only. For an explicitly authorized deployment:

```bash
sudo /root/bin/remountfs_rw
# install only the intended files
sudo mount / -o remount,ro
```

Verify the final mount state. Preserve existing configuration and restart only the services affected by the deployment.

Never run write benchmarks while the Tesla is recording or archiving is active. Use `docs/sd-card-write-testing.md` for the two-sided workload/capability strategy. Test files must be unique, mounted-target checks are mandatory, and cleanup must touch only files created by the test.

## Telemetry and Maps

- Telemetry is disabled by default and retained locally under `/mutable/telemetry`.
- Index SavedClips and RecentClips before archiving; never index SentryClips.
- Use Tesla filename timestamps for display and retention, MP4 PTS for playback alignment, and SEI for telemetry values.
- Archived/overwritten tracks remain labelled but are not playable; NAS playback is out of scope.
- OpenStreetMap is the default tile provider. `TELEMETRY_MAP_STYLE_URL` supports a trusted local MapLibre vector style.

## Documentation Map

- `docs/architecture.md`: system context, Mermaid flows, storage, security, and extension points.
- `docs/shell-layer.md`: archive loop, notifications, Telegram, setup, and paths.
- `docs/go-backend.md`: API routes, middleware, data structures, and telemetry endpoints.
- `docs/new-ui.md`: routes, maps, viewer, settings, and frontend conventions.
- `docs/provisioning.md`: headless BLE, captive portal, web setup, and validation boundaries.
- `docs/sd-card-write-testing.md`: Tesla-like load and raw card benchmarks.
- `docs/dev-workflow.md`: detailed tooling, CI, branches, and deployment.
