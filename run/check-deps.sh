#!/bin/bash -eu
# Check (and optionally install) the external tools listed in lib/dependencies.txt.
#
#   check-deps.sh [--install] [feature ...]
#
# With no features, checks "core", the active ARCHIVE_SYSTEM and, when
# TELEGRAM_ENABLED=true, "telegram". Settings come from the environment, or
# from the setup variables file when ARCHIVE_SYSTEM is not set.
#
# Prints one line per missing dependency:  <feature>: <check> (apt: <package>)
# Exit status: 0 = all present, 1 = something is still missing, 2 = usage.
#
# --install runs apt-get for missing packages. The caller must already have
# the root filesystem writable (setup, OTA and reconfigure-archive.sh do).
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MANIFEST="${TESLAUSB_DEPS_MANIFEST:-$SCRIPT_DIR/lib/dependencies.txt}"
CONF="${TESLAUSB_SETUP_CONF:-/root/teslausb_setup_variables.conf}"
# archiveloop and the backend run with a reduced PATH; the sbin directories
# hold mount.cifs, mount.nfs and fsck.vfat.
PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin"

install=false
features=()
for arg in "$@"
do
  case "$arg" in
    --install) install=true ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "check-deps: unknown option $arg" >&2; exit 2 ;;
    *) features+=("$arg") ;;
  esac
done

if [ ! -r "$MANIFEST" ]
then
  echo "check-deps: manifest $MANIFEST not found" >&2
  exit 2
fi

if [ "${#features[@]}" -eq 0 ]
then
  if [ -z "${ARCHIVE_SYSTEM:-}" ] && [ -r "$CONF" ]
  then
    # shellcheck disable=SC1090
    source "$CONF"
  fi
  features=(core "${ARCHIVE_SYSTEM:-none}")
  if [ "${TELEGRAM_ENABLED:-false}" = "true" ]
  then
    features+=(telegram)
  fi
fi

function wanted () {
  local f
  for f in "${features[@]}"
  do
    [ "$f" = "$1" ] && return 0
  done
  return 1
}

function present () {
  local alt
  local -a alts
  IFS='|' read -r -a alts <<< "$1"
  for alt in "${alts[@]}"
  do
    case "$alt" in
      py:*)
        python3 -c "import ${alt#py:}" > /dev/null 2>&1 && return 0
        ;;
      *)
        command -v "$alt" > /dev/null 2>&1 && return 0
        ;;
    esac
  done
  return 1
}

# Fills the global arrays missing_lines and missing_packages.
function scan () {
  missing_lines=()
  missing_packages=()
  local feature check package hint
  while read -r feature check package hint
  do
    case "$feature" in ''|'#'*) continue ;; esac
    wanted "$feature" || continue
    present "$check" && continue
    if [ "$package" = "-" ]
    then
      missing_lines+=("$feature: $check (not available from apt${hint:+; $hint})")
    else
      missing_lines+=("$feature: $check (apt: $package${hint:+; $hint})")
      case " ${missing_packages[*]:-} " in
        *" $package "*) ;;
        *) missing_packages+=("$package") ;;
      esac
    fi
  done < "$MANIFEST"
}

scan

if [ "$install" = true ] && [ "${#missing_packages[@]}" -gt 0 ]
then
  echo "check-deps: installing ${missing_packages[*]}"
  export DEBIAN_FRONTEND=noninteractive
  if ! apt-get install -y --no-install-recommends "${missing_packages[@]}"
  then
    # A stale package index is the usual cause; refresh once and retry.
    apt-get update && apt-get install -y --no-install-recommends "${missing_packages[@]}" || true
  fi
  scan
fi

if [ "${#missing_lines[@]}" -eq 0 ]
then
  exit 0
fi
printf '%s\n' "${missing_lines[@]}"
exit 1
