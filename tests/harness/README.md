# Integration Harness

Lets you test teslausb scripts **without flashing a Raspberry Pi**.

## What it is

Two containers wired together via `docker-compose.yml`:

- **samba** — a dperson/samba container acting as a fake Synology (user `tesla`, password `tesla`, share `dashcam`). This is what `archiveloop` archives into over CIFS.
- **fake-pi** — a Debian bookworm container with the same package set as a Pi (bash, python, cifs-utils, rsync, smbclient). Your `run/` and `setup/` directories are bind-mounted read-only, so code changes in your editor are instantly testable inside the container.

The fake-pi runs with `TESLAUSB_DRY_RUN=1` by default. Scripts should honour this when adding real side effects.

## Quick start

```bash
just harness-up        # build + start containers
just harness-smoke     # run the smoke test (also used in CI)
just harness-shell     # drop into a shell on fake-pi for manual testing
just harness-down      # stop + wipe volumes
```

## Limitations

- No USB gadget emulation — `enable_gadget.sh` / `disable_gadget.sh` can't be end-to-end tested here; keep their logic in small testable shell functions.
- Systemd isn't PID 1 — use bash-only scripts for the harness, not `systemctl start ...`. Use `bash -n` and function-level unit tests instead.
- CIFS mount inside Docker requires `SYS_ADMIN` (already set); your host may need kernel modules loaded.
