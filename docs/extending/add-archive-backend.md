# Adding a New Archive Backend

Archive backends are self-contained script bundles in `run/<name>_archive/`. The main loop in `run/archiveloop` calls them through a fixed interface; it does not care whether they speak SMB, NFS, rsync, rclone, S3, or anything else.

The five built-in backends (`cifs`, `nfs`, `rsync`, `rclone`, `none`) are good reference material. This guide explains the contract and walks through adding a minimal new backend.

---

## How dispatch works

`ARCHIVE_SYSTEM=<name>` in `teslausb_setup_variables.conf` selects the backend.

During Pi setup, `setup/pi/configure.sh` calls:

```bash
archive_module="$( get_archive_module )"   # returns "run/<name>_archive"
install_archive_scripts /root/bin "$archive_module"
```

`install_archive_scripts` copies each required script from `run/<name>_archive/` into `/root/bin/` on the Pi (flat — no subdirectory). `archiveloop` then calls them as `/root/bin/archive-clips.sh`, `/root/bin/connect-archive.sh`, etc.

---

## Required scripts

Every backend must provide these five scripts. All must start with:

```bash
#!/bin/bash -eu
set -o pipefail
```

### `archive-is-reachable.sh`

**Signature:** called with `$1 = $ARCHIVE_SERVER` (the host, IP, or URL set by the user)

**Contract:** exit 0 if the archive is reachable, exit 1 if not. Called in a polling loop — must be fast (< 10s).

A missing tool here exits 127, which is indistinguishable from "unreachable":
the loop then waits forever with an `idle` status. Prefer bash built-ins, and
declare anything else in `run/lib/dependencies.txt` (see below).

```bash
# cifs example: check TCP port 445 with bash's /dev/tcp (no nc needed)
timeout 5 bash -c 'exec 3<>"/dev/tcp/$0/$1"' "$1" 445 > /dev/null 2>&1

# rclone example: hit the configured remote
rclone lsd "$RCLONE_REMOTE:" --contimeout 5s > /dev/null 2>&1

# http example
curl -sf --max-time 5 "$ARCHIVE_SERVER/health" > /dev/null
```

### `connect-archive.sh`

**Signature:** no arguments. Uses env vars.

**Contract:** mount or establish the connection to the archive. Exit 0 on success, exit non-zero on failure. `archiveloop` calls this before `archive-clips.sh` and skips the archive cycle if it fails.

```bash
# cifs example
mount "$ARCHIVE_MOUNT"

# rclone: nothing to mount — just verify connectivity
rclone lsd "$RCLONE_REMOTE:" --contimeout 10s > /dev/null 2>&1
```

### `disconnect-archive.sh`

**Signature:** no arguments. Uses env vars.

**Contract:** unmount or tear down the connection. Always succeeds (use `|| true` on errors).

```bash
# cifs example
umount "$ARCHIVE_MOUNT" || true
```

### `archive-clips.sh`

**Signature:**
```
archive-clips.sh <src_dir> <filelist> [<triggerdir> <triggerlist>]
```

| Argument | Description |
|----------|-------------|
| `$1` — `src_dir` | Path to the overlayfs merge point; source files are relative to this |
| `$2` — `filelist` | Path to a text file; one relative path per line (the clips to copy) |
| `$3` — `triggerdir` | Directory containing trigger files (optional; used by some backends) |
| `$4` — `triggerlist` | Path to a text file listing trigger file paths (optional) |

**Contract:** copy every file listed in `$filelist` from `$src_dir` to the archive. Exit 0 if all files were copied successfully. Exit non-zero if any copy failed (archiveloop will log an error and skip the post-archive cleanup).

Files that are successfully copied must be **removed from the source** (`--remove-source-files` in rsync, or explicit `rm` after copy). `archiveloop` tracks which files have been archived by checking whether the source symlink still exists after this script exits.

```bash
# Minimal example using rsync:
rsync -avhRL --remove-source-files \
  --files-from="$2" \
  "$1/" \
  "$ARCHIVE_MOUNT"
```

### `verify-and-configure-archive.sh`

**Signature:** no arguments. Uses env vars. Runs once during Pi setup (not at runtime, and **not** when the backend is switched from the web UI).

**Contract:** validate that the user's config is sane and write any persistent config (e.g. `/etc/fstab` entries, credential files). Call `log_progress "STOP: <reason>"` and `exit 1` if validation fails.

```bash
function log_progress () {
  if declare -F setup_progress > /dev/null; then
    setup_progress "verify-and-configure-archive: $*"
  else
    echo "verify-and-configure-archive: $*"
  fi
}

# Example: check a required env var
if [ -z "${MY_BACKEND_ENDPOINT:+x}" ]; then
  log_progress "STOP: MY_BACKEND_ENDPOINT is not set"
  exit 1
fi

# Write credentials
echo "token=${MY_BACKEND_TOKEN}" > /root/.my_backend_creds
chmod 600 /root/.my_backend_creds
```

### Runtime dependencies: `run/lib/dependencies.txt`

Every non-Essential tool your runtime scripts call (`archive-is-reachable.sh`,
`connect-archive.sh`, `archive-clips.sh`, ...) must be listed under your
backend's name:

```
mybackend   my-required-tool    my-required-package
mybackend   alt-a|alt-b         package-for-alt-a
mybackend   vendor-cli          -                    install it from https://example.com
```

`run/check-deps.sh` reads this file. It installs missing packages during setup,
after an OTA update, and when the web UI switches to your backend (the switch
is refused if something cannot be installed). archiveloop checks it, without
installing, at startup and before each archive cycle, and shows missing tools
as an `error: Missing ...` archive state. `tests/python/test_check_deps.py`
fails when a backend script uses a known non-Essential tool that is not
declared; add your tools to `BACKEND_TOOLS` there too.

### `copy-music.sh` (optional)

Only needed if your backend supports copying music files from the Pi's music disk. If absent, the music copy step is skipped automatically (archiveloop checks `[ -d "${MUSIC_ARCHIVE_MOUNT:-}" ]` before calling it).

---

## Step-by-step: adding an S3 backend

This example uses `rclone` (already installed if `ARCHIVE_SYSTEM=rclone` is in use) to copy clips to any S3-compatible storage.

### 1. Create the directory

```
run/s3_archive/
  archive-is-reachable.sh
  connect-archive.sh
  disconnect-archive.sh
  archive-clips.sh
  verify-and-configure-archive.sh
```

### 2. `archive-is-reachable.sh`

```bash
#!/bin/bash -eu
set -o pipefail
# $1 = ARCHIVE_SERVER (we ignore it; rclone determines reachability itself)
rclone lsd "${S3_RCLONE_REMOTE}:" --contimeout 8s > /dev/null 2>&1
```

### 3. `connect-archive.sh`

```bash
#!/bin/bash -eu
set -o pipefail
# S3 is connectionless — just verify access
rclone lsd "${S3_RCLONE_REMOTE}:${S3_BUCKET}" --contimeout 10s > /dev/null 2>&1
```

### 4. `disconnect-archive.sh`

```bash
#!/bin/bash -eu
set -o pipefail
# Nothing to tear down
true
```

### 5. `archive-clips.sh`

```bash
#!/bin/bash -eu
set -o pipefail

src_dir="$1"
filelist="$2"

# rclone copyto handles individual files efficiently.
# We iterate the filelist and remove each file after a successful copy.
while IFS= read -r relpath; do
  dest="${S3_RCLONE_REMOTE}:${S3_BUCKET}/TeslaCam/${relpath}"
  if rclone copyto "${src_dir}/${relpath}" "$dest" --s3-acl private; then
    rm -f "${src_dir}/${relpath}"
  else
    echo "Failed to copy: $relpath" >&2
    exit 1
  fi
done < "$filelist"
```

### 6. `verify-and-configure-archive.sh`

```bash
#!/bin/bash -eu
set -o pipefail

function log_progress () {
  if declare -F setup_progress > /dev/null; then
    setup_progress "verify-and-configure-archive: $*"
  else echo "verify-and-configure-archive: $*"; fi
}

for var in S3_RCLONE_REMOTE S3_BUCKET; do
  if [ -z "${!var:+x}" ]; then
    log_progress "STOP: $var is not set in teslausb_setup_variables.conf"
    exit 1
  fi
done

log_progress "Installing rclone..."
apt-get -y install rclone

log_progress "Verifying rclone can reach ${S3_RCLONE_REMOTE}:${S3_BUCKET}..."
if ! rclone lsd "${S3_RCLONE_REMOTE}:${S3_BUCKET}" --contimeout 15s > /dev/null 2>&1; then
  log_progress "STOP: Cannot reach ${S3_RCLONE_REMOTE}:${S3_BUCKET}. Check rclone config and credentials."
  exit 1
fi

log_progress "S3 backend verified."
```

### 7. Register in `setup/pi/configure.sh`

Add `s3` to both functions:

```bash
# In check_archive_configs():
s3)
  if [ -z "${S3_RCLONE_REMOTE:+x}" ] || [ -z "${S3_BUCKET:+x}" ]; then
    log_progress "STOP: S3 archive requires S3_RCLONE_REMOTE and S3_BUCKET"
    exit 1
  fi
  ;;

# In get_archive_module():
s3)
  echo "run/s3_archive"
  ;;
```

Also set `ARCHIVE_SERVER` for `s3` in `run/archiveloop` (the section that derives `ARCHIVE_SERVER` from `ARCHIVE_SYSTEM`):

```bash
s3)
  export ARCHIVE_SERVER="8.8.8.8"  # reachability check is done by archive-is-reachable.sh
  ;;
```

### 8. User config (`teslausb_setup_variables.conf`)

```bash
ARCHIVE_SYSTEM=s3
S3_RCLONE_REMOTE=my-s3          # name of your rclone remote
S3_BUCKET=my-tesla-clips
```

---

## Testing without a Pi

Use the Docker harness with `ARCHIVE_SYSTEM` overridden:

```bash
# bring up the fake-pi container
just harness-up

# shell in and set up your backend
just harness-shell
# inside the container:
export ARCHIVE_SYSTEM=s3
export S3_RCLONE_REMOTE=...
bash /root/bin/verify-and-configure-archive.sh
bash /root/bin/connect-archive.sh
```

For a pure unit test of `archive-clips.sh`:

```bash
# create a fake filelist and source tree
mkdir -p /tmp/test_src/TeslaCam/SentryClips/2026-04-01
echo "TeslaCam/SentryClips/2026-04-01/front.mp4" > /tmp/filelist.txt
touch /tmp/test_src/TeslaCam/SentryClips/2026-04-01/front.mp4

export S3_RCLONE_REMOTE=... S3_BUCKET=...
bash run/s3_archive/archive-clips.sh /tmp/test_src /tmp/filelist.txt
```

---

## Env vars available to all backend scripts

All env vars from `/root/teslausb_setup_variables.conf` are sourced by `archiveloop` before any backend script is called. The following are always set:

| Var | Description |
|-----|-------------|
| `ARCHIVE_SERVER` | Host or IP (derived by archiveloop from `ARCHIVE_SYSTEM`) |
| `ARCHIVE_MOUNT` | Mount point for the archive (e.g. `/mnt/archive`), if applicable |
| `CAM_DISK` | Path to the cam disk image (`/backingfiles/cam_disk.bin`) |
| `MUTABLE_DIR` | Path to persistent mutable storage (`/mutable`) |
| `LOG_FILE` | Path to the archiveloop log |

Use `log()` (exported by archiveloop) for all diagnostic output — it timestamps and writes to `$LOG_FILE`.

---

## Checklist

- [ ] Five required scripts present in `run/<name>_archive/`
- [ ] All scripts: `#!/bin/bash -eu` + `set -o pipefail`
- [ ] `archive-clips.sh` removes source files after a successful copy
- [ ] `verify-and-configure-archive.sh` installs deps, validates config, writes credentials
- [ ] `get_archive_module()` case added in `setup/pi/configure.sh`
- [ ] `check_archive_configs()` case added in `setup/pi/configure.sh`
- [ ] `ARCHIVE_SERVER` derivation added in `run/archiveloop`
- [ ] User-facing env vars documented in `doc/SetupRClone.md` (or a new `doc/Setup<Name>.md`)
- [ ] `shellcheck --severity=warning` returns zero warnings on all new scripts
- [ ] `just ci` is green
