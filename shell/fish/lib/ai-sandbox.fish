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
    printf '%s\n' $mount_args
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
