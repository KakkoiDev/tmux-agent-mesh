#!/usr/bin/env bats

# export/import is the network layer: two mailboxes converge by set union
# because a message's identity is a hash of its content rather than a row id.
# These run the real binary against two data directories, which is the only way
# to prove the two halves are independent databases and not one shared handle.

bats_require_minimum_version 1.5.0
load helpers

setup() {
    setup_test_env
    # A second, independent mailbox next to the first.
    export DIR_B="$TEST_TMPDIR/b"
    mkdir -p "$DIR_B"
    mesh_b init >/dev/null

    "$MESH_BIN" register --session A --harness claude --alias alpha >/dev/null
    "$MESH_BIN" register --session B --harness codex  --alias bravo >/dev/null
}

teardown() {
    teardown_test_env
}

# The second mailbox, addressed only through the environment so nothing about
# the first leaks into it.
mesh_b() {
    MESH_DIR="$DIR_B" MESH_DB="$DIR_B/mesh.db" \
    MESH_NOTIFY_DIR="$DIR_B/notify" MESH_DELIVERY_LOG="$DIR_B/delivery.log" \
        "$MESH_BIN" "$@"
}

msql_b() { printf '.timeout 100\n%s\n' "$*" | sqlite3 "$DIR_B/mesh.db"; }

# ── export ───────────────────────────────────────────────────────────

@test "export emits one JSON object per line, starting with its version" {
    "$MESH_BIN" send --from A --to bravo --message hi --thread t1 >/dev/null
    run "$MESH_BIN" export
    assert_ok
    assert_eq "$(printf '%s' "$output" | head -1 | jq -r .type)" "meta"
    assert_eq "$(printf '%s' "$output" | head -1 | jq -r .version)" "1"
    # Every line parses on its own, which is what makes this streamable.
    assert_eq "$(printf '%s\n' "$output" | jq -c . | wc -l | tr -d ' ')" \
              "$(printf '%s\n' "$output" | wc -l | tr -d ' ')"
}

@test "export carries the channel and thread each message needs" {
    "$MESH_BIN" send --from A --to bravo --message hi --thread t1 >/dev/null
    run "$MESH_BIN" export
    assert_ok
    assert_eq "$(printf '%s\n' "$output" | jq -r 'select(.type=="channel") | .name')" "dm:A:B"
    assert_eq "$(printf '%s\n' "$output" | jq -r 'select(.type=="thread") | .name')" "t1"
}

@test "export --channel leaves the other channels out entirely" {
    "$MESH_BIN" channel create ops >/dev/null
    "$MESH_BIN" channel join ops --as bravo >/dev/null
    "$MESH_BIN" send --from A --to bravo --message dm-only >/dev/null
    "$MESH_BIN" send --from human --channel ops --message room-only >/dev/null
    run "$MESH_BIN" export --channel ops
    assert_ok
    assert_contains "$output" "room-only"
    refute_contains "$output" "dm-only"
    refute_contains "$output" "dm:A:B"
}

@test "export --since takes an epoch, which sqlite's unixepoch cannot parse" {
    "$MESH_BIN" send --from A --to bravo --message old >/dev/null
    msql "UPDATE messages SET created_at = 1000;"
    run "$MESH_BIN" export --since 500
    assert_ok
    assert_contains "$output" "old"
    run "$MESH_BIN" export --since 2000
    assert_ok
    refute_contains "$output" "old"
}

# ── import ───────────────────────────────────────────────────────────

@test "a message exported from one mailbox is readable in the other" {
    "$MESH_BIN" send --from A --to bravo --message "carried over" --thread audit >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    run mesh_b import "$TEST_TMPDIR/dump.ndjson"
    assert_ok
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "1"
    run mesh_b thread audit
    assert_ok
    assert_contains "$output" "carried over"
}

@test "importing the same export twice leaves one row" {
    "$MESH_BIN" send --from A --to bravo --message once >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    run mesh_b import "$TEST_TMPDIR/dump.ndjson"
    assert_ok
    assert_contains "$output" "0 of 1"
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "1"
}

@test "import reads stdin, which is what makes ssh the transport" {
    "$MESH_BIN" send --from A --to bravo --message "over the wire" >/dev/null
    run bash -c "'$MESH_BIN' export | MESH_DIR='$DIR_B' MESH_DB='$DIR_B/mesh.db' \
        MESH_NOTIFY_DIR='$DIR_B/notify' '$MESH_BIN' import"
    assert_ok
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "1"
}

@test "a body with embedded newlines survives the round trip byte for byte" {
    "$MESH_BIN" send --from A --to bravo --message "line one
line two	tabbed
'quoted'" >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    assert_eq "$(msql_b "SELECT body FROM messages;")" "$(msql "SELECT body FROM messages;")"
}

@test "a reply keeps pointing at its parent after the trip" {
    "$MESH_BIN" send --from A --to bravo --message question --thread q >/dev/null
    "$MESH_BIN" reply --from B --to-message 1 --message answer >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    # By uid on both sides, because the row ids are local to each database.
    local parent_uid
    parent_uid=$(msql "SELECT uid FROM messages WHERE body='question';")
    assert_eq "$(msql_b "SELECT p.uid FROM messages m JOIN messages p ON p.id=m.reply_to_id
                          WHERE m.body='answer';")" "$parent_uid"
}

@test "the two mailboxes are separate: importing does not write back to the source" {
    mesh_b register --session C --harness claude --alias charlie >/dev/null
    mesh_b send --from C --to human --message "only in B" >/dev/null
    assert_eq "$(count_messages)" "0"
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "1"
}

@test "import creates no memberships, so imported mail is history not pending" {
    "$MESH_BIN" send --from A --to bravo --message "not yours" >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    assert_eq "$(msql_b "SELECT COUNT(*) FROM channel_members
                          WHERE channel_id IN (SELECT id FROM channels WHERE kind='dm');")" "0"
    run mesh_b inbox --as human
    assert_ok
    refute_contains "$output" "not yours"
}

# The membership sweep in cleanup drops rows for sessions that are not
# registered here, which for an imported DM is every member. Dropping the
# channel then cascades its messages away, so an import followed by a cleanup
# used to lose everything it had just brought in.
@test "cleanup does not eat an imported conversation" {
    "$MESH_BIN" send --from A --to bravo --message "keep me" >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    mesh_b cleanup --forced >/dev/null
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "1"
}

@test "an imported DM renders with the channel name rather than no recipient" {
    "$MESH_BIN" send --from A --to bravo --message "orphaned" --thread t1 >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    run mesh_b search orphaned
    assert_ok
    assert_contains "$output" "dm:A:B"
}

# ── refusals ─────────────────────────────────────────────────────────

@test "import refuses a record whose content does not match its address" {
    "$MESH_BIN" send --from A --to bravo --message original >/dev/null
    "$MESH_BIN" export | sed 's/original/tampered/' > "$TEST_TMPDIR/bad.ndjson"
    run mesh_b import "$TEST_TMPDIR/bad.ndjson"
    assert_status 1
    assert_contains "$output" "content address does not match"
    # Nothing written: the check runs before the transaction.
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "0"
}

@test "import refuses an export from a newer version" {
    "$MESH_BIN" send --from A --to bravo --message hi >/dev/null
    "$MESH_BIN" export | jq -c 'if .type=="meta" then .version=2 else . end' \
        > "$TEST_TMPDIR/v2.ndjson"
    run mesh_b import "$TEST_TMPDIR/v2.ndjson"
    assert_status 1
    assert_contains "$output" "version 2"
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "0"
}

# hops, expect_reply and created_at are outside the content address, so a record
# can be edited there and still hash correctly. Rendering the SQL through a pipe
# hid jq's failure: sqlite3 rolled back and import exited 0 claiming every
# message was already present.
@test "import refuses a record whose numbers are not numbers" {
    "$MESH_BIN" send --from A --to bravo --message hi >/dev/null
    "$MESH_BIN" export | jq -c 'if .type=="message" then .hops="abc" else . end' \
        > "$TEST_TMPDIR/bad-hops.ndjson"
    run mesh_b import "$TEST_TMPDIR/bad-hops.ndjson"
    assert_status 1
    assert_contains "$output" "malformed"
    assert_eq "$(msql_b "SELECT COUNT(*) FROM messages;")" "0"
}

@test "import refuses input that is not newline-delimited JSON" {
    printf 'this is not json\n' > "$TEST_TMPDIR/junk"
    run mesh_b import "$TEST_TMPDIR/junk"
    assert_status 1
    assert_contains "$output" "newline-delimited JSON"
}

@test "import on a file that does not exist is not found, not a usage error" {
    run mesh_b import "$TEST_TMPDIR/nope.ndjson"
    assert_status 3
}

@test "import into a mailbox that was never created says so" {
    "$MESH_BIN" send --from A --to bravo --message hi >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    run env MESH_DIR="$TEST_TMPDIR/never" MESH_DB="$TEST_TMPDIR/never/mesh.db" \
        MESH_NOTIFY_DIR="$TEST_TMPDIR/never/notify" \
        "$MESH_BIN" import "$TEST_TMPDIR/dump.ndjson"
    assert_status 3
    assert_contains "$output" "no mailbox"
}

@test "import --json reports what it added and what it skipped" {
    "$MESH_BIN" send --from A --to bravo --message hi >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    run mesh_b import "$TEST_TMPDIR/dump.ndjson" --json
    assert_ok
    assert_eq "$(printf '%s' "$output" | jq -r '.messages')" "1"
    assert_eq "$(printf '%s' "$output" | jq -r '.duplicates')" "0"
    run mesh_b import "$TEST_TMPDIR/dump.ndjson" --json
    assert_ok
    assert_eq "$(printf '%s' "$output" | jq -r '.messages')" "0"
    assert_eq "$(printf '%s' "$output" | jq -r '.duplicates')" "1"
}

# ── idle cost ────────────────────────────────────────────────────────

@test "nothing is resident: no mesh process survives a full round trip" {
    "$MESH_BIN" send --from A --to bravo --message hi >/dev/null
    "$MESH_BIN" export > "$TEST_TMPDIR/dump.ndjson"
    mesh_b import "$TEST_TMPDIR/dump.ndjson" >/dev/null
    # Children of this test only. A machine-wide `ps | grep mesh.sh` also counts
    # the mesh running in every other pane on the box, so it proves nothing
    # about this code and goes red whenever someone else sends a message.
    # $$ is the bats runner rather than this subshell, hence the exec/PPID trick.
    local self
    self=$(exec sh -c 'echo $PPID')
    assert_eq "$(pgrep -P "$self" 2>/dev/null | wc -l | tr -d ' ')" "0"
}
