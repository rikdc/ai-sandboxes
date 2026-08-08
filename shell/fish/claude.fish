function claude --description 'Run Claude Code in a hardened Microsandbox VM'
    set -l image 'ai-sandboxes-claude:local'
    set -l profile_volume 'claude-home-hardened'
    set -l egress_file "$HOME/.config/microvms/claude-egress"
    set -l workspace_quota '10G'
    set -l net_rules \
        --net-rule 'allow@host:udp:53' \
        --net-rule 'allow@host:tcp:53'

    if not type -q msb
        echo 'claude: msb is not installed or is not on PATH' >&2
        return 127
    end

    if not test -f "$egress_file"
        echo "claude: missing egress allowlist: $egress_file" >&2
        echo 'claude: copy config/claude-egress.example there and review its hosts' >&2
        return 1
    end

    while read -l egress_host
        set egress_host (string trim -- "$egress_host")
        if test -z "$egress_host"; or string match -q '#*' -- "$egress_host"
            continue
        end

        # The allowlist contains hostnames only: one HTTPS destination per line.
        if not string match -rq '^(\*\.)?[A-Za-z0-9][A-Za-z0-9.-]*$' -- "$egress_host"
            echo "claude: invalid hostname in $egress_file: $egress_host" >&2
            return 1
        end
        set -a net_rules --net-rule "allow@$egress_host:tcp:443"
    end < "$egress_file"

    set -l host_workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test $status -ne 0
        set host_workspace (pwd -P)
    end
    set host_workspace (realpath "$host_workspace")

    set -l home_path (realpath "$HOME")
    if test -z "$host_workspace"; or test "$host_workspace" = /; or test "$host_workspace" = "$home_path"
        echo 'claude: refusing to mount an empty path, /, or the complete home directory' >&2
        return 2
    end

    set -l project_name (basename "$host_workspace" | string replace --all --regex '[^A-Za-z0-9._-]' '-')
    set -l project_hash (printf '%s' "$host_workspace" | git hash-object --stdin | string sub --length 12)
    set -l guest_workspace "/workspace/$project_name-$project_hash"

    command msb run \
        --tty \
        --pull never \
        --user node \
        --cpus 4 \
        --memory 8G \
        --security restricted \
        --no-net \
        --dns-nameserver 1.1.1.1 \
        --dns-query-timeout-ms 5000 \
        --trust-host-cas \
        $net_rules \
        --mount-dir "$host_workspace:$guest_workspace:rw,quota=$workspace_quota" \
        --mount-named "$profile_volume:/home/node:kind=dir,quota=4G" \
        --workdir "$guest_workspace" \
        "$image" \
        -- env \
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
            ENABLE_CLAUDEAI_MCP_SERVERS=false \
            claude $argv
end
