#!/usr/bin/env bats


bats_require_minimum_version 1.5.0
load helpers

setup() {
    setup_test_env
    source_mesh_functions
    cmd_register --session A --harness claude --cwd /tmp/a >/dev/null
    cmd_register --session P --harness pi --cwd /tmp/p >/dev/null
    _set_alias A alpha
    _set_alias P pusher
}

teardown() {
    teardown_test_env
}


# ── push mode ────────────────────────────────────────────────────────

@test "pi-deliver push returns pending mail" {
    cmd_send --from A --to pusher --message "wake up"
    run cmd_pi_deliver --session P --mode push
    assert_contains "$output" "wake up"
}

@test "pi-deliver push stamps the pi-push mode" {
    cmd_send --from A --to pusher --message "x"
    cmd_pi_deliver --session P --mode push >/dev/null
    assert_eq "$(msql "SELECT delivered_via FROM messages WHERE id=1;")" "pi:push"
}

@test "pi-deliver push emits nothing with an empty mailbox" {
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
}

@test "pi-deliver push is at-most-once" {
    cmd_send --from A --to pusher --message "x"
    cmd_pi_deliver --session P --mode push >/dev/null
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
}

@test "pi-deliver push carries the untrusted-peer envelope" {
    cmd_send --from A --to pusher --message "do a thing"
    run cmd_pi_deliver --session P --mode push
    assert_contains "$output" "untrusted input"
}

# ── push mode: budget ────────────────────────────────────────────────

@test "pi-deliver push increments the block streak" {
    cmd_send --from A --to pusher --message "x"
    cmd_pi_deliver --session P --mode push >/dev/null
    assert_eq "$(_block_streak P)" "1"
}

# Pi's watcher is the only path that can wake an idle agent, so it is also the
# only one that can run away. The budget must stop it and hold the mail.
@test "pi-deliver push stops at the streak cap and holds the mail" {
    MAX_BLOCKS=2
    msql "UPDATE agents SET block_streak=2 WHERE session_id='P';"
    cmd_send --from A --to pusher --message "held"
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
    assert_eq "$(pending_for P)" "1"
}

@test "pi-deliver push works one below the streak cap" {
    MAX_BLOCKS=2
    msql "UPDATE agents SET block_streak=1 WHERE session_id='P';"
    cmd_send --from A --to pusher --message "ok"
    run cmd_pi_deliver --session P --mode push
    assert_contains "$output" "ok"
}

# ── before-start mode ────────────────────────────────────────────────

@test "pi-deliver before-start returns pending mail" {
    cmd_send --from A --to pusher --message "queued"
    run cmd_pi_deliver --session P --mode before-start
    assert_contains "$output" "queued"
}

@test "pi-deliver before-start stamps its own mode" {
    cmd_send --from A --to pusher --message "x"
    cmd_pi_deliver --session P --mode before-start >/dev/null
    assert_eq "$(msql "SELECT delivered_via FROM messages WHERE id=1;")" "pi:before-start"
}

# Regression: before_agent_start also fires for the turns mesh itself triggers
# via sendUserMessage. Resetting the streak there cleared the budget after every
# push, so the cap could never fire and a Pi ping-pong would run forever.
# Observed live: a real push delivery left block_streak at 0.
@test "pi-deliver before-start does NOT reset the block streak" {
    msql "UPDATE agents SET block_streak=3 WHERE session_id='P';"
    cmd_pi_deliver --session P --mode before-start >/dev/null
    assert_eq "$(_block_streak P)" "3"
}

@test "a push followed by before-start keeps the budget charged" {
    cmd_send --from A --to pusher --message "one"
    cmd_pi_deliver --session P --mode push >/dev/null
    cmd_pi_deliver --session P --mode before-start >/dev/null
    assert_eq "$(_block_streak P)" "1"
}

# Only real typing clears the budget, which is what pi.on("input") reports.
@test "reset-streak clears the budget" {
    msql "UPDATE agents SET block_streak=3 WHERE session_id='P';"
    cmd_reset_streak --session P
    assert_eq "$(_block_streak P)" "0"
}

# Models the runaway shape: mail keeps arriving and the watcher keeps firing,
# with no human touching the keyboard. Only the push path is exercised because
# once the cap engages mesh stops triggering turns, so before_agent_start does
# not fire again either.
@test "a Pi push run terminates at the cap without human input" {
    MAX_BLOCKS=2
    local i
    for i in 1 2 3 4 5; do
        cmd_send --from A --to pusher --message "m$i" --thread "t$i" >/dev/null
        cmd_pi_deliver --session P --mode push >/dev/null
    done
    assert_eq "$(_block_streak P)" "2"
    assert_eq "$(pending_for P)" "3"
}

# before-start deliberately ignores the cap so held mail is never starved. That
# is safe only because it fires on a turn that is already happening.
@test "before-start drains cap-held mail when a turn does start" {
    MAX_BLOCKS=1
    msql "UPDATE agents SET block_streak=1 WHERE session_id='P';"
    cmd_send --from A --to pusher --message "held1" --thread t1
    cmd_send --from A --to pusher --message "held2" --thread t2
    cmd_pi_deliver --session P --mode push >/dev/null
    assert_eq "$(pending_for P)" "2"
    run cmd_pi_deliver --session P --mode before-start
    assert_contains "$output" "held1"
    assert_contains "$output" "held2"
    assert_eq "$(pending_for P)" "0"
}

@test "typing after the cap lets delivery resume" {
    MAX_BLOCKS=1
    msql "UPDATE agents SET block_streak=1 WHERE session_id='P';"
    cmd_send --from A --to pusher --message "blocked"
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
    cmd_reset_streak --session P
    run cmd_pi_deliver --session P --mode push
    assert_contains "$output" "blocked"
}

# The streak cap must not be able to starve an agent: mail held by the cap has
# to arrive on the next prompt.
@test "mail held by the push cap arrives on the next before-start" {
    MAX_BLOCKS=1
    msql "UPDATE agents SET block_streak=1 WHERE session_id='P';"
    cmd_send --from A --to pusher --message "deferred"
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
    run cmd_pi_deliver --session P --mode before-start
    assert_contains "$output" "deferred"
}

@test "before-start ignores the streak cap" {
    MAX_BLOCKS=1
    msql "UPDATE agents SET block_streak=9 WHERE session_id='P';"
    cmd_send --from A --to pusher --message "still delivered"
    run cmd_pi_deliver --session P --mode before-start
    assert_contains "$output" "still delivered"
}

@test "extension resets the budget from the input event" {
    grep -q 'pi.on("input"' "$PROJECT_ROOT/pi-extension/index.ts"
    grep -q 'reset-streak' "$PROJECT_ROOT/pi-extension/index.ts"
}

# ── mode gating ──────────────────────────────────────────────────────

@test "pi-deliver push is silent when pi delivery is before-start" {
    PI_DELIVERY=before-start
    cmd_send --from A --to pusher --message "no wake"
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
    assert_eq "$(pending_for P)" "1"
}

@test "pi-deliver before-start still works when push is disabled" {
    PI_DELIVERY=before-start
    cmd_send --from A --to pusher --message "on prompt"
    run cmd_pi_deliver --session P --mode before-start
    assert_contains "$output" "on prompt"
}

@test "pi-deliver is silent in both modes when pi delivery is off" {
    PI_DELIVERY=off
    cmd_send --from A --to pusher --message "x"
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
    run cmd_pi_deliver --session P --mode before-start
    assert_empty "$output"
    assert_eq "$(pending_for P)" "1"
}

@test "pi-deliver is silent when the whole mesh is disabled" {
    cmd_send --from A --to pusher --message "x"
    ENABLED=off
    run cmd_pi_deliver --session P --mode push
    assert_empty "$output"
    assert_eq "$(pending_for P)" "1"
}

@test "pi-deliver rejects an unknown mode" {
    run cmd_pi_deliver --session P --mode sideways
    assert_fail
}

@test "pi-deliver requires a session" {
    run cmd_pi_deliver --mode push
    assert_fail
}

# ── notify flag contract with the extension ──────────────────────────

# The extension computes the flag name in TypeScript. If the two sanitisers
# disagree the watcher silently never fires, so pin the mapping.
@test "notify flag name sanitises a pi session path the same way" {
    run _notify_flag "/Users/x/.pi/sessions/session-abc.jsonl"
    assert_match "$output" '*_Users_x_.pi_sessions_session-abc.jsonl.flag'
}

@test "notify flag keeps dot dash and underscore" {
    run _notify_flag "a.b-c_d"
    assert_match "$output" '*/a.b-c_d.flag'
}

@test "notify flag replaces slashes and colons" {
    run _notify_flag "work:1.2/x"
    assert_match "$output" '*/work_1.2_x.flag'
}

@test "send touches the flag the pi watcher waits on" {
    cmd_send --from A --to pusher --message "x"
    assert_file "$NOTIFY_DIR/P.flag"
}

@test "deregister removes the notify flag" {
    cmd_send --from A --to pusher --message "x"
    cmd_deregister --session P
    refute_file "$NOTIFY_DIR/P.flag"
}

# ── extension source sanity ──────────────────────────────────────────

# Neither typebox nor the pi package resolves from ~/.pi/agent/extensions, so a
# runtime import of either would break loading. Only type-only imports and
# node: builtins are safe.
@test "extension imports nothing that cannot resolve at runtime" {
    local f="$PROJECT_ROOT/pi-extension/index.ts"
    assert_file "$f"
    run grep -nE '^import [^t]' "$f"
    local line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        [[ "$line" == *'"node:'* ]] || return 1
    done <<<"$output"
}

@test "extension imports the pi package types only" {
    grep -q 'import type .* from "@earendil-works/pi-coding-agent"' "$PROJECT_ROOT/pi-extension/index.ts"
    refute grep -qE '^import \{[^}]*\} from "@earendil-works' "$PROJECT_ROOT/pi-extension/index.ts"
}

@test "extension delegates policy to mesh.sh rather than reimplementing it" {
    local f="$PROJECT_ROOT/pi-extension/index.ts"
    grep -q 'pi-deliver' "$f"
    # No cap or mode arithmetic in TypeScript
    refute grep -qE 'MAX_BLOCKS|max_hops|block_streak' "$f"
}
