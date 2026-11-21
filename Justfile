# teslausb local dev commands
# Install `just` (https://github.com/casey/just) then run `just <target>`.

set shell := ["bash", "-euo", "pipefail", "-c"]

# Show all available commands
default:
    @just --list

# One-shot: install all local dev dependencies (uv, pre-commit, hooks)
bootstrap:
    command -v uv >/dev/null || curl -LsSf https://astral.sh/uv/install.sh | sh
    uv sync --all-groups
    uv run pre-commit install

# Run the full CI pipeline locally
ci: lint test test-backend build-ui test-ui harness-smoke

# Lint everything (shell + python + yaml + secrets)
lint: lint-shell lint-python lint-secrets

lint-shell:
    ./check.sh
    shellcheck --severity=warning --exclude=SC1091 \
        $(git ls-files 'run/*' 'setup/**/*' 'tools/**/*' | \
          xargs -I{} sh -c 'head -1 "{}" 2>/dev/null | grep -q "^#\!.*\(bash\|sh\)" && echo "{}"' | \
          grep -vE '^(pi-gen-sources/|teslausb-www/)') || true

lint-python:
    uv run ruff check . || true
    uv run ruff format --check . || true
    uv run mypy run tests/python || true

lint-secrets:
    gitleaks detect --no-git --config .gitleaks.toml --verbose

# Regression guard: ensure nginx config does not ship with 'auth_basic off'
lint-nginx-auth:
    @if grep -r "auth_basic off" teslausb-www/; then \
      echo "" >&2; \
      echo "FAIL: 'auth_basic off' found in teslausb-www/" >&2; \
      exit 1; \
    fi

# Auto-format code
fmt:
    uv run ruff check --fix .
    uv run ruff format .
    shfmt -w -i 4 -ci -sr $(git ls-files 'run/*' 'setup/**/*' 'tools/**/*' | \
        xargs -I{} sh -c 'head -1 "{}" 2>/dev/null | grep -q "^#\!.*\(bash\|sh\)" && echo "{}"')

# Run Python unit tests
test:
    uv run pytest

# Build the new web UI
build-ui:
    cd teslausb-www/new && pnpm install --frozen-lockfile && pnpm typecheck && pnpm lint && pnpm build

# Browser regressions (install Chromium once with pnpm exec playwright install chromium)
test-ui:
    cd teslausb-www/new && pnpm test:browser

# Vet + test the unified Go backend
test-backend:
    cd teslausb-www/go-backend && go vet ./... && go test -race -count=1 ./...

# Cross-compile the backend binary for Pi (linux/arm64)
build-backend:
    cd teslausb-www/go-backend && GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o ../../dist/teslausb-backend ./...

# Bring up the integration harness (samba + fake-pi)
harness-up:
    docker compose -f tests/harness/docker-compose.yml up -d --build

harness-down:
    docker compose -f tests/harness/docker-compose.yml down -v

harness-smoke:
    tests/harness/run-smoke.sh

# Shell into the fake-pi for manual testing
harness-shell:
    docker compose -f tests/harness/docker-compose.yml exec fake-pi bash

