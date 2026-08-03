# tmux-agent-mesh bash completion
#
# Source this file:
#   source <(tmux-agent-mesh completion bash)
# Or install:
#   cp completion.bash /usr/local/etc/bash_completion.d/tmux-agent-mesh

_tmux_agent_mesh_completion() {
    local cur prev words cword
    _init_completion || return

    # Top-level subcommands.
    local cmds="init register deregister name alias roster send broadcast reply
                inbox mark-read history thread recv watch drain ping info
                channel dm search dispatch claim-dispatch menu goto status-bar
                refresh cleanup hook doctor selftest set-transcript transcript
                export import"

    if [[ $cword -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$cmds" -- "$cur"))
        return
    fi

    local cmd="${words[1]}"

    case "$cmd" in
        # history, info, ping and channel used to be listed here too, which made
        # their own arms below unreachable: the first matching pattern wins, so
        # `channel create <TAB>` never saw the channel arm at all.
        roster|inbox|recv)
            case "$prev" in
                --as|--from|--to)
                    # Complete agent names.
                    local agents
                    agents=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                        "SELECT COALESCE(alias, session_id) FROM agents;" 2>/dev/null || true)
                    COMPREPLY=($(compgen -W "$agents" -- "$cur"))
                    return
                    ;;
                --channel)
                    local channels
                    channels=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                        "SELECT name FROM channels WHERE archived_at IS NULL;" 2>/dev/null || true)
                    COMPREPLY=($(compgen -W "$channels" -- "$cur"))
                    return
                    ;;
            esac
            COMPREPLY=($(compgen -W "--json --as --limit" -- "$cur"))
            return
            ;;
        send)
            case "$prev" in
                --to)
                    local agents
                    agents=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                        "SELECT COALESCE(alias, session_id) FROM agents;" 2>/dev/null || true)
                    COMPREPLY=($(compgen -W "$agents" -- "$cur"))
                    return
                    ;;
            esac
            COMPREPLY=($(compgen -W "--to --channel --message --expect-reply --thread --from --remote --json" -- "$cur"))
            return
            ;;
        reply)
            COMPREPLY=($(compgen -W "--to-message --message --from --json" -- "$cur"))
            return
            ;;
        broadcast)
            COMPREPLY=($(compgen -W "--message --project --harness --from --json" -- "$cur"))
            return
            ;;
        drain)
            COMPREPLY=($(compgen -W "--session --via --json" -- "$cur"))
            return
            ;;
        mark-read)
            COMPREPLY=($(compgen -W "--as --message-id --json" -- "$cur"))
            return
            ;;
        history)
            case "$prev" in
                --as|--from)
                    local agents
                    agents=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                        "SELECT COALESCE(alias, session_id) FROM agents;" 2>/dev/null || true)
                    COMPREPLY=($(compgen -W "$agents" -- "$cur"))
                    return
                    ;;
                --channel)
                    local channels
                    channels=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                        "SELECT name FROM channels WHERE archived_at IS NULL;" 2>/dev/null || true)
                    COMPREPLY=($(compgen -W "$channels" -- "$cur"))
                    return
                    ;;
            esac
            COMPREPLY=($(compgen -W "--as --channel --thread --from --since --limit --json" -- "$cur"))
            return
            ;;
        dispatch)
            COMPREPLY=($(compgen -W "--task --harness --alias --worktree --cwd --from --env --window --json" -- "$cur"))
            return
            ;;
        channel)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "create join leave list members rule archive" -- "$cur"))
                return
            fi
            local sub="${words[2]}"
            case "$sub" in
                join|leave)
                    case "$prev" in
                        --as)
                            local agents
                            agents=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                                "SELECT COALESCE(alias, session_id) FROM agents;" 2>/dev/null || true)
                            COMPREPLY=($(compgen -W "$agents" -- "$cur"))
                            return
                            ;;
                    esac
                    COMPREPLY=($(compgen -W "--as --json" -- "$cur"))
                    ;;
                create)
                    COMPREPLY=($(compgen -W "--private --description --json" -- "$cur"))
                    ;;
                rule)
                    COMPREPLY=($(compgen -W "--harness --model --remove list" -- "$cur"))
                    ;;
                list|members|archive)
                    COMPREPLY=($(compgen -W "--json" -- "$cur"))
                    ;;
            esac
            return
            ;;
        dm)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "--json" -- "$cur"))
                return
            fi
            local agents
            agents=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                "SELECT COALESCE(alias, session_id) FROM agents;" 2>/dev/null || true)
            COMPREPLY=($(compgen -W "$agents" -- "$cur"))
            return
            ;;
        search)
            COMPREPLY=($(compgen -W "--channel --from --since --limit --json" -- "$cur"))
            return
            ;;
        export)
            case "$prev" in
                --channel)
                    local channels
                    channels=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                        "SELECT name FROM channels WHERE archived_at IS NULL;" 2>/dev/null || true)
                    COMPREPLY=($(compgen -W "$channels" -- "$cur"))
                    return
                    ;;
            esac
            COMPREPLY=($(compgen -W "--since --channel" -- "$cur"))
            return
            ;;
        import)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "--json" -- "$cur"))
            else
                COMPREPLY=($(compgen -f -- "$cur"))
            fi
            return
            ;;
        info|ping)
            local agents
            agents=$(sqlite3 "$HOME/.tmux-agent-mesh/mesh.db" \
                "SELECT COALESCE(alias, session_id) FROM agents;" 2>/dev/null || true)
            COMPREPLY=($(compgen -W "$agents --json" -- "$cur"))
            return
            ;;
    esac

    COMPREPLY=($(compgen -W "$cmds" -- "$cur"))
}

complete -F _tmux_agent_mesh_completion tmux-agent-mesh
