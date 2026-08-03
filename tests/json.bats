#!/usr/bin/env bats

# The JSON surface and the exit-code table are a contract an agent programs
# against, so they are tested through the real binary rather than through the
# sourced functions: an agent invokes the script, and the dispatcher's flag scan
# and the process exit status are both part of what it sees.

bats_require_minimum_version 1.5.0
load helpers

setup() {
    setup_test_env
    "$MESH_BIN" register --session A --harness claude --alias alpha >/dev/null
    "$MESH_BIN" register --session B --harness codex  --alias bravo >/dev/null
}

teardown() {
    teardown_test_env
}

# jq -e fails on false/null, so this asserts the field is present and true.
assert_json_ok() {
    printf '%s' "$1" | jq -e '.ok == true' >/dev/null \
        || _afail "not an ok JSON result: $1"
}

json_field() { printf '%s' "$1" | jq -r "$2"; }

# ── every documented result is one JSON object ───────────────────────

@test "send --json returns the message id as a number" {
    run "$MESH_BIN" send --from A --to bravo --message hi --thread t1 --json
    assert_ok
    assert_json_ok "$output"
    assert_eq "$(json_field "$output" '.message_id')" "1"
    assert_eq "$(json_field "$output" '.message_id | type')" "number"
    assert_eq "$(json_field "$output" '.to')" "bravo"
    assert_eq "$(json_field "$output" '.thread')" "t1"
}

@test "reply --json reports the hop it landed on" {
    "$MESH_BIN" send --from A --to bravo --message hi --thread t1 >/dev/null
    run "$MESH_BIN" reply --from B --to-message 1 --message yo --json
    assert_ok
    assert_json_ok "$output"
    assert_eq "$(json_field "$output" '.hops')" "1"
    assert_eq "$(json_field "$output" '.to')" "alpha"
}

@test "broadcast --json counts its recipients" {
    run "$MESH_BIN" broadcast --from A --message everyone --json
    assert_ok
    assert_eq "$(json_field "$output" '.recipients')" "1"
    assert_eq "$(json_field "$output" '.recipients | type')" "number"
}

@test "mark-read --json counts what it consumed" {
    "$MESH_BIN" send --from A --to bravo --message one >/dev/null
    "$MESH_BIN" send --from A --to bravo --message two >/dev/null
    run "$MESH_BIN" mark-read --as bravo --json
    assert_ok
    assert_eq "$(json_field "$output" '.count')" "2"
}

@test "mark-read --json on an empty inbox is still an ok object" {
    run "$MESH_BIN" mark-read --as bravo --json
    assert_ok
    assert_json_ok "$output"
    assert_eq "$(json_field "$output" '.count')" "0"
}

@test "register --json names the agent it registered" {
    run "$MESH_BIN" register --session C --harness gemini --alias charlie --json
    assert_ok
    assert_eq "$(json_field "$output" '.session_id')" "C"
    assert_eq "$(json_field "$output" '.alias')" "charlie"
    assert_eq "$(json_field "$output" '.harness')" "gemini"
}

@test "register without --json stays silent" {
    run "$MESH_BIN" register --session C --harness gemini --alias charlie
    assert_ok
    assert_empty "$output"
}

@test "channel create --json returns the id it created" {
    run "$MESH_BIN" channel create ops --json
    assert_ok
    assert_eq "$(json_field "$output" '.channel')" "ops"
    assert_eq "$(json_field "$output" '.channel_id | type')" "number"
}

@test "channel join and leave --json name the channel and the member" {
    "$MESH_BIN" channel create ops >/dev/null
    run "$MESH_BIN" channel join ops --as bravo --json
    assert_ok
    assert_eq "$(json_field "$output" '.session_id')" "B"
    assert_eq "$(json_field "$output" '.channel')" "ops"
    run "$MESH_BIN" channel leave ops --as bravo --json
    assert_ok
    assert_eq "$(json_field "$output" '.session_id')" "B"
}

@test "channel members --json is an array, not an object" {
    "$MESH_BIN" channel create ops >/dev/null
    "$MESH_BIN" channel join ops --as bravo >/dev/null
    run "$MESH_BIN" channel members ops --json
    assert_ok
    assert_eq "$(json_field "$output" 'type')" "array"
    assert_eq "$(json_field "$output" 'map(.session_id) | index("B") != null')" "true"
}

@test "dm --json returns the same channel send --to would use" {
    run "$MESH_BIN" dm bravo --json
    assert_ok
    local cid
    cid=$(json_field "$output" '.channel_id')
    "$MESH_BIN" send --from human --to bravo --message hi >/dev/null
    assert_eq "$(msql "SELECT channel_id FROM messages WHERE id=1;")" "$cid"
}

@test "name --json reports the alias it set" {
    export TMUX_PANE=%9
    "$MESH_BIN" register --session D --harness claude --pane %9 >/dev/null
    run "$MESH_BIN" name delta --json
    assert_ok
    assert_eq "$(json_field "$output" '.alias')" "delta"
    assert_eq "$(json_field "$output" '.session_id')" "D"
}

@test "transcript --json carries the recorded path" {
    "$MESH_BIN" set-transcript --session A /tmp/a.jsonl >/dev/null
    run "$MESH_BIN" transcript alpha --json
    assert_ok
    assert_eq "$(json_field "$output" '.transcript_path')" "/tmp/a.jsonl"
}

# ── the exit-code table ──────────────────────────────────────────────

@test "exit 1: an unknown flag is a usage error" {
    run "$MESH_BIN" send --from A --to bravo --message hi --nosuchflag --json
    assert_status 1
    assert_eq "$(json_field "$output" '.code')" "1"
    assert_eq "$(json_field "$output" '.ok')" "false"
}

@test "exit 2: an ambiguous reference is not a guess" {
    "$MESH_BIN" register --session zzz111 --harness claude >/dev/null
    "$MESH_BIN" register --session zzz222 --harness claude >/dev/null
    run "$MESH_BIN" send --from A --to zzz --message hi --json
    assert_status 2
    assert_eq "$(json_field "$output" '.code')" "2"
    assert_contains "$output" "ambiguous"
}

@test "exit 3: an unknown recipient is not found, not a usage error" {
    run "$MESH_BIN" send --from A --to nobody --message hi --json
    assert_status 3
    assert_eq "$(json_field "$output" '.code')" "3"
    assert_eq "$(count_messages)" "0"
}

@test "exit 3: replying to a message that does not exist" {
    run "$MESH_BIN" reply --from B --to-message 999 --message hi --json
    assert_status 3
    assert_eq "$(json_field "$output" '.code')" "3"
}

@test "exit 3: a channel that does not exist" {
    run "$MESH_BIN" channel join nosuch --json
    assert_status 3
    run "$MESH_BIN" channel archive nosuch --json
    assert_status 3
    assert_eq "$(json_field "$output" '.code')" "3"
}

@test "exit 4: the hop cap refuses, and says so in its code" {
    run "$MESH_BIN" send --from A --to bravo --message hi --hops 9 --json
    assert_status 4
    assert_eq "$(json_field "$output" '.code')" "4"
    assert_eq "$(count_messages)" "0"
}

@test "exit 4: the kill switch refuses" {
    plant_config "ENABLED='off'"
    run "$MESH_BIN" send --from A --to bravo --message hi --json
    assert_status 4
    assert_eq "$(json_field "$output" '.code')" "4"
}

@test "exit 4: the fan-out cap refuses rather than truncating" {
    plant_config "MAX_BROADCAST='1'"
    "$MESH_BIN" register --session C --harness claude --alias charlie >/dev/null
    run "$MESH_BIN" broadcast --from A --message hi --json
    assert_status 4
    assert_eq "$(json_field "$output" '.code')" "4"
    assert_eq "$(count_messages)" "0"
}

@test "exit 4: posting to a channel you are not in" {
    "$MESH_BIN" channel create ops >/dev/null
    "$MESH_BIN" channel join ops --as bravo >/dev/null
    run "$MESH_BIN" send --from A --channel ops --message hi --json
    assert_status 4
    assert_eq "$(json_field "$output" '.code')" "4"
}

@test "exit 5: a channel name already taken is a conflict, not a usage error" {
    "$MESH_BIN" channel create ops >/dev/null
    run "$MESH_BIN" channel create ops --json
    assert_status 5
    assert_eq "$(json_field "$output" '.code')" "5"
    assert_eq "$(msql "SELECT COUNT(*) FROM channels WHERE name='ops';")" "1"
}

@test "exit 5: an alias already held is a conflict" {
    run "$MESH_BIN" alias B alpha --json
    assert_status 5
    assert_eq "$(json_field "$output" '.code')" "5"
}

# ── the two streams stay separate ────────────────────────────────────

@test "a JSON error goes to stderr, leaving stdout empty" {
    run bash -c "'$MESH_BIN' send --from A --to nobody --message hi --json 2>/dev/null"
    assert_status 3
    assert_empty "$output"
}

@test "a JSON result goes to stdout, leaving stderr empty" {
    run bash -c "'$MESH_BIN' send --from A --to bravo --message hi --json 2>&1 >/dev/null"
    assert_ok
    assert_empty "$output"
}

@test "without --json the output is a sentence and stdout carries it" {
    run "$MESH_BIN" send --from A --to bravo --message hi
    assert_ok
    assert_contains "$output" "queued message 1 to bravo"
    refute_contains "$output" '"ok"'
}

@test "an error raised before the flag is parsed still answers in JSON" {
    # --hops is validated inside cmd_send's parser, so this exercises the
    # dispatcher's scan rather than the per-command one.
    run "$MESH_BIN" --json send --from A --to bravo --message hi --hops abc
    assert_status 1
    assert_eq "$(json_field "$output" '.code')" "1"
}
