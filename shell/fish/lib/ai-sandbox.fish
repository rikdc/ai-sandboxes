# Defense in depth only: this file is itself part of the checkout, so a guest
# with write access to a mounted checkout could edit this check out entirely.
# The real trust boundary is the wrapper installed by
# scripts/install-fish-functions (see shell/fish/trusted/guard.fish), which
# runs the same check *before* ever sourcing this file. This copy still
# matters for direct or pre-wrapper invocations of the checkout's own
# implementation functions.
function __ai_sandbox_refuse_workspace_overlap --argument-names agent launcher_file workspace
    set -l launcher_root (dirname (dirname (dirname (realpath "$launcher_file"))))
    if test "$workspace" = "$launcher_root"; or string match -q -- "$launcher_root/*" "$workspace/"; or string match -q -- "$workspace/*" "$launcher_root/"
        echo "$agent: refusing to run: the workspace ($workspace) overlaps the ai-sandboxes checkout that provides this launcher ($launcher_root)" >&2
        echo "$agent: a guest agent with write access to the mounted workspace could modify these host-trusted scripts, which would then run with full host access on a later invocation" >&2
        echo "$agent: run $agent from a different project, or install ai-sandboxes to a location you never mount as a workspace" >&2
        return 1
    end
    return 0
end

function __ai_sandbox_workspace_quota --argument-names launcher_file agent
    set -l versions_file (dirname (dirname (dirname (realpath "$launcher_file"))))/versions.env
    if not test -r "$versions_file"
        echo "$agent: cannot read $versions_file" >&2
        return 2
    end

    set -l workspace_quota (command awk -F= '$1 == "WORKSPACE_QUOTA" { print $2; exit }' "$versions_file")
    if not string match -rq '^[1-9][0-9]*[KMGT]$' -- "$workspace_quota"
        echo "$agent: WORKSPACE_QUOTA must be a positive K, M, G, or T size" >&2
        return 2
    end

    printf '%s\n' "$workspace_quota"
end

function __ai_sandbox_shared_state_mount_args --argument-names agent image
    set -l cache_index (contains --index -- "$image" $__ai_sandbox_shared_state_images)
    if test -n "$cache_index"
        set -l cached_state $__ai_sandbox_shared_state_values[$cache_index]
        if test -z "$cached_state"
            return 0
        end
        set -l cached_values (string split '|' -- "$cached_state")
        printf '%s\n' --mount-named "agent-state-$cached_values[1]-v1:/var/lib/agent-state:kind=dir,quota=$cached_values[2]"
        return 0
    end

    set -l metadata (msb image inspect --format json "$image"); or return $status
    set -l state_ids (string match --regex --groups-only '"io\.ai-sandboxes\.shared-state\.id"[[:space:]]*:[[:space:]]*"([^"]*)"' -- "$metadata")
    set -l state_quotas (string match --regex --groups-only '"io\.ai-sandboxes\.shared-state\.quota"[[:space:]]*:[[:space:]]*"([^"]*)"' -- "$metadata")
    if test (count $state_ids) -eq 0; and test (count $state_quotas) -eq 0
        set -ga __ai_sandbox_shared_state_images "$image"
        set -ga __ai_sandbox_shared_state_values ''
        return 0
    end
    if test (count $state_ids) -ne 1; or test (count $state_quotas) -ne 1
        echo "$agent: image has inconsistent shared-state labels" >&2
        return 2
    end
    set -l state_id $state_ids[1]
    set -l state_quota $state_quotas[1]
    if test -z "$state_id"; and test -z "$state_quota"
        # Images built without an opted-in shared-state capability still carry
        # the label keys (set from empty ARGs), just with empty values. That is
        # equivalent to the labels being absent entirely.
        set -ga __ai_sandbox_shared_state_images "$image"
        set -ga __ai_sandbox_shared_state_values ''
        return 0
    end
    if test -z "$state_id"; or test -z "$state_quota"
        echo "$agent: image has inconsistent shared-state labels" >&2
        return 2
    end
    if not string match -rq '^[a-z0-9][a-z0-9-]{0,62}$' -- "$state_id"
        echo "$agent: image has an invalid shared-state id" >&2
        return 2
    end
    if not string match -rq '^[1-9][0-9]*[KMGT]$' -- "$state_quota"
        echo "$agent: image has an invalid shared-state quota" >&2
        return 2
    end

    set -ga __ai_sandbox_shared_state_images "$image"
    set -ga __ai_sandbox_shared_state_values "$state_id|$state_quota"
    printf '%s\n' --mount-named "agent-state-$state_id-v1:/var/lib/agent-state:kind=dir,quota=$state_quota"
end

function __ai_sandbox_prepare_shared_state --argument-names agent image
    set -l mount_args (__ai_sandbox_shared_state_mount_args "$agent" "$image"); or return $status
    __ai_sandbox_initialize_shared_state "$agent" "$image" $mount_args; or return $status
    if test (count $mount_args) -gt 0
        printf '%s\n' $mount_args
    end
end

function __ai_sandbox_initialize_shared_state --argument-names agent image
    set -l mount_args $argv[3..-1]
    if test (count $mount_args) -eq 0
        return 0
    end
    if test (count $mount_args) -ne 2; or test "$mount_args[1]" != --mount-named
        echo "$agent: invalid shared-state mount arguments" >&2
        return 2
    end

    set -l state_volume (string split -m 1 : -- "$mount_args[2]")[1]
    if test -z "$state_volume"
        echo "$agent: invalid shared-state volume" >&2
        return 2
    end
    # Microsandbox assigns the quota when the volume is first mounted. Cache the
    # setup per Fish session so existing and legacy volumes are repaired once
    # without an extra VM boot for every agent invocation.
    if contains -- "$state_volume" $__ai_sandbox_initialized_shared_state_volumes
        return 0
    end

    command msb run --pull never --no-tty --no-net --security restricted --user root $mount_args "$image" -- install -d -o node -g node -m 0700 /var/lib/agent-state; or begin
        set -l init_status $status
        echo "$agent: could not initialize shared state" >&2
        return $init_status
    end
    set -ga __ai_sandbox_initialized_shared_state_volumes "$state_volume"
end

function __ai_sandbox_launch --argument-names launcher_file agent image home_volume
    if not type -q msb
        echo "$agent: msb is not installed or not on PATH" >&2
        return 127
    end

    set -l workspace_quota (__ai_sandbox_workspace_quota "$launcher_file" "$agent"); or return $status
    set -l shared_state_args (__ai_sandbox_prepare_shared_state "$agent" "$image"); or return $status
    set -l workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test -z "$workspace"
        set workspace (pwd -P)
    end
    set workspace (realpath "$workspace")
    set -l home_path (realpath "$HOME")
    if test -z "$workspace"; or test "$workspace" = /; or test "$workspace" = "$home_path"
        echo "$agent: refusing to mount an empty path, /, or the complete home directory" >&2
        return 2
    end
    __ai_sandbox_refuse_workspace_overlap "$agent" "$launcher_file" "$workspace"; or return $status

    set -l slug (basename "$workspace" | string replace -ra '[^A-Za-z0-9._-]' '-')
    set -l short_hash (printf '%s' "$workspace" | shasum -a 256 | string split ' ' | head -n 1 | string sub -l 12)
    set -l guest_workspace "/workspace/$slug-$short_hash"
    set -l existing_volumes (msb volume list --quiet); or return $status
    if not contains -- "$home_volume" $existing_volumes
        msb volume create "$home_volume"; or return $status
    end
    command msb run --tty --pull never --user node --net public --root-disk "$workspace_quota" \
        --mount-dir "$workspace:$guest_workspace:rw" \
        --mount-named "$home_volume:/home/node:rw" \
        $shared_state_args \
        --workdir "$guest_workspace" "$image" -- "$agent" "$argv"
    return $status
end

function __ai_sandbox_run_claude --argument-names launcher_file image
    set -l claude_argv $argv[3..-1]
    set -l profile_volume 'claude-home-hardened'
    set -l egress_file "$HOME/.config/microvms/claude-egress"
    set -l workspace_quota '10G'
    set -l root_disk '10G'
    # Let Microsandbox's gateway DNS follow the host resolver.  An external
    # resolver is not reachable through every public-network gateway.
    set -l network_args \
        --no-net \
        --net-rule 'allow@host:udp:53' \
        --net-rule 'allow@host:tcp:53'

    if not type -q msb
        echo 'claude: msb is not installed or is not on PATH' >&2
        return 127
    end

    if set -q CLAUDE_MSB_PUBLIC_EGRESS; and test "$CLAUDE_MSB_PUBLIC_EGRESS" = 1
        set network_args --net public
    else
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
            set -a network_args --net-rule "allow@$egress_host:tcp:443"
        end < "$egress_file"
    end

    set -l shared_state_args (__ai_sandbox_prepare_shared_state claude "$image"); or return $status

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
    __ai_sandbox_refuse_workspace_overlap claude "$launcher_file" "$host_workspace"; or return $status

    set -l project_name (basename "$host_workspace" | string replace --all --regex '[^A-Za-z0-9._-]' '-')
    set -l project_hash (printf '%s' "$host_workspace" | git hash-object --stdin | string sub --length 12)
    set -l guest_workspace "/workspace/$project_name-$project_hash"

    command msb run \
        --tty \
        --pull never \
        --user node \
        --cpus 4 \
        --memory 8G \
        --root-disk "$root_disk" \
        --security restricted \
        $network_args \
        --mount-dir "$host_workspace:$guest_workspace:rw,quota=$workspace_quota" \
        --mount-named "$profile_volume:/home/node:kind=dir,quota=4G" \
        $shared_state_args \
        --workdir "$guest_workspace" \
        "$image" \
        -- env \
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
            ENABLE_CLAUDEAI_MCP_SERVERS=false \
            claude $claude_argv
end
