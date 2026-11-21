#!/usr/bin/env bats
# Tests for CGI path traversal hardening.
#
# cgi_common.sh uses declare -g and printf -v (bash 4+). On macOS where
# /bin/bash is 3.2, run with:  BATS_SHELL=/opt/homebrew/bin/bash bats ...
# On the Pi (Debian bookworm) /bin/bash is 5.x so this is not needed.
#
# These tests source cgi_common.sh directly and validate that:
# - Path traversal attempts are rejected
# - Null bytes in query parameters are rejected
# - Legitimate paths are accepted
# - URL-encoded traversal attempts are caught

# Path to the library under test
CGI_LIB="teslausb-www/html/cgi-bin/lib/cgi_common.sh"

setup() {
    # Create a fake document root for testing.
    # Canonicalise the path to handle macOS /var -> /private/var symlinks,
    # which would otherwise cause grealpath output to mismatch.
    local raw_root
    raw_root=$(mktemp -d)
    if command -v grealpath &>/dev/null; then
        FAKE_ROOT=$(grealpath -m "$raw_root")
    else
        FAKE_ROOT=$(realpath -m "$raw_root")
    fi
    export FAKE_ROOT
    export DOCUMENT_ROOT="$FAKE_ROOT"
    mkdir -p "$FAKE_ROOT/TeslaCam/SentryClips"
    mkdir -p "$FAKE_ROOT/TeslaCam/SavedClips"
    echo "test clip" > "$FAKE_ROOT/TeslaCam/SentryClips/2025-01-01_12-00-00.mp4"
}

teardown() {
    rm -rf "$FAKE_ROOT"
}

# --- cgi_safe_path tests ---

@test "cgi_safe_path: accepts a valid subdirectory" {
    source "$CGI_LIB"
    result=$(cgi_safe_path "$FAKE_ROOT" "TeslaCam/SentryClips")
    [ "$result" = "$FAKE_ROOT/TeslaCam/SentryClips" ]
}

@test "cgi_safe_path: accepts document root itself" {
    source "$CGI_LIB"
    result=$(cgi_safe_path "$FAKE_ROOT" ".")
    [ "$result" = "$FAKE_ROOT" ]
}

@test "cgi_safe_path: rejects ../../../etc/passwd traversal" {
    source "$CGI_LIB"
    run cgi_safe_path "$FAKE_ROOT" "../../../etc/passwd"
    [ "$status" -eq 0 ]  # exit 0 from cgi_error
    [[ "$output" == *"Path escapes document root"* ]]
}

@test "cgi_safe_path: rejects .. as path" {
    source "$CGI_LIB"
    run cgi_safe_path "$FAKE_ROOT" ".."
    [ "$status" -eq 0 ]
    [[ "$output" == *"Path escapes document root"* ]]
}

@test "cgi_safe_path: rejects TeslaCam/../../etc/shadow" {
    source "$CGI_LIB"
    run cgi_safe_path "$FAKE_ROOT" "TeslaCam/../../etc/shadow"
    [ "$status" -eq 0 ]
    [[ "$output" == *"Path escapes document root"* ]]
}

@test "cgi_safe_path: accepts non-existent path under root (realpath -m)" {
    source "$CGI_LIB"
    result=$(cgi_safe_path "$FAKE_ROOT" "TeslaCam/NewDir/subdir")
    [ "$result" = "$FAKE_ROOT/TeslaCam/NewDir/subdir" ]
}

# --- cgi_parse_query tests ---

@test "cgi_parse_query: decodes simple query string" {
    source "$CGI_LIB"
    export QUERY_STRING="TeslaCam&SentryClips"
    cgi_parse_query
    [ "${urlargs[0]}" = "TeslaCam" ]
    [ "${urlargs[1]}" = "SentryClips" ]
}

@test "cgi_parse_query: decodes plus as space" {
    source "$CGI_LIB"
    export QUERY_STRING="Saved+Clips"
    cgi_parse_query
    [ "${urlargs[0]}" = "Saved Clips" ]
}

@test "cgi_parse_query: rejects null byte %00" {
    source "$CGI_LIB"
    export QUERY_STRING="TeslaCam%00../../etc/passwd"
    run cgi_parse_query
    [ "$status" -eq 0 ]
    [[ "$output" == *"Null byte"* ]]
}

# --- cgi_safe_cd tests ---

@test "cgi_safe_cd: changes to valid directory" {
    source "$CGI_LIB"
    cgi_safe_cd "TeslaCam/SentryClips"
    [ "$(pwd)" = "$FAKE_ROOT/TeslaCam/SentryClips" ]
}

@test "cgi_safe_cd: rejects traversal via .." {
    source "$CGI_LIB"
    run cgi_safe_cd "../../../../../../../../../.."
    [[ "$output" == *"Path escapes document root"* ]]
}

@test "cgi_safe_cd: rejects non-existent directory" {
    source "$CGI_LIB"
    run cgi_safe_cd "NoSuchDir"
    [[ "$output" == *"not found"* ]] || [[ "$output" == *"Not found"* ]]
}

# --- Integration-style tests (script parse checks) ---

@test "download.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/download.sh
    [ "$status" -eq 0 ]
}

@test "ls.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/ls.sh
    [ "$status" -eq 0 ]
}

@test "rm.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/rm.sh
    [ "$status" -eq 0 ]
}

@test "cp.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/cp.sh
    [ "$status" -eq 0 ]
}

@test "mv.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/mv.sh
    [ "$status" -eq 0 ]
}

@test "mkdir.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/mkdir.sh
    [ "$status" -eq 0 ]
}

@test "downloadzip.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/downloadzip.sh
    [ "$status" -eq 0 ]
}

@test "upload.sh parses without error" {
    run bash -n teslausb-www/html/cgi-bin/upload.sh
    [ "$status" -eq 0 ]
}
