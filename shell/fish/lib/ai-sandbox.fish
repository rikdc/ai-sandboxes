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

function __ai_sandbox_launch --argument-names launcher_file agent image home_volume
    if not type -q msb
        echo "$agent: msb is not installed or not on PATH" >&2
        return 127
    end

    set -l workspace_quota (__ai_sandbox_workspace_quota "$launcher_file" "$agent"); or return $status
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
    if not msb volume list --quiet | string match -qx "$home_volume"
        msb volume create "$home_volume"; or return $status
    end

    command msb run --tty --pull never --user node --net public --root-disk "$workspace_quota" \
        --mount-dir "$workspace:$guest_workspace:rw" \
        --mount-named "$home_volume:/home/node:rw" \
        --workdir "$guest_workspace" "$image" -- "$agent" $argv
    return $status
end
