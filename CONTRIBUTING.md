# Contributing to Dashvault

Dashvault uses strict CI, local reproducibility, fast feedback loops, and a local harness so you never need to flash a Pi to test changes.

## Prerequisites

- **Docker** (Desktop on macOS, Engine on Linux) — runs the integration harness.
- **[just](https://github.com/casey/just)** — task runner. `brew install just`.
- **[uv](https://github.com/astral-sh/uv)** — Python env + deps. `brew install uv` or `curl -LsSf https://astral.sh/uv/install.sh | sh`.

First-time setup:

```bash
just bootstrap    # installs deps + pre-commit hooks
```

## Dev loop

```bash
just fmt          # auto-format shell + python
just lint         # shellcheck, ruff, mypy, gitleaks
just test         # python unit tests
just harness-up   # spin up local samba + fake-pi
just harness-smoke
just ci           # everything, as run in GitHub Actions
```

`just ci` is the single command that must pass before you push.

## Branch & commit conventions

- **Trunk:** `main`.
- **Topic branches** off `main`:
  - `fix/<short-slug>` — bug / regression
  - `feat/<short-slug>` — new feature
  - `chore/<short-slug>` — infra, deps, docs
  - `refactor/<short-slug>` — no behaviour change
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/). One logical change per commit.
- **Small PRs.** Each topic branch should be independently reviewable; if a refactor and a feature are tangled, split them.


## Testing rules

- **Never test against a live Pi (`dashvault.local`) in CI.** Use the harness.
- New shell scripts: pass `shellcheck --severity=warning` and add coverage using the repository's existing pytest subprocess pattern.
- New Python code: use type hints, pass scoped `ruff check`/`ruff format --check`/`mypy`, and add pytest coverage.
- Touching `archiveloop`: exercise the changed path with a focused test and run the fake-Pi harness when Docker is available.
- Frontend changes: run `pnpm typecheck`, `pnpm lint`, and `pnpm build`; regenerate `routeTree.gen.ts` through Vite rather than editing it manually.
- SD-card benchmarks: use `/dev/zero` and follow [the write-testing guide](docs/sd-card-write-testing.md); never race active recording or archiving.

## Secrets

`*.conf` is gitignored by default. Never commit real tokens, Wi-Fi passwords, or Tesla API credentials. `gitleaks` runs in CI and in pre-commit — if it flags something, fix the commit, don't bypass.

## AI-assisted development

Claude/other coding agents are welcome contributors. Read the root [`AGENTS.md`](AGENTS.md) for repository, safety, and validation rules. Conventions that keep agent work mergeable:

- Each agent session works on one topic branch.
- Agent must run `just ci` green before declaring a task done.
- PR description states what was delegated to the agent and what was reviewed by a human.
