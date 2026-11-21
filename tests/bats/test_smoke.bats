#!/usr/bin/env bats
# Bats entry-point — placeholder. Real bats suites come with Phase 2/3 fixes.

@test "archiveloop script exists and parses" {
    run bash -n run/archiveloop
    [ "$status" -eq 0 ]
}

@test "make_snapshot.sh parses" {
    run bash -n run/make_snapshot.sh
    [ "$status" -eq 0 ]
}
