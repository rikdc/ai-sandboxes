function codex --description 'Run OpenAI Codex in an ephemeral Microsandbox VM'
    if not type -q msb
        echo 'codex: msb is not installed or not on PATH' >&2
        return 127
    end

    set -l versions_file (dirname (dirname (dirname (realpath (status filename)))))/versions.env
    if not test -r "$versions_file"
        echo "codex: cannot read $versions_file" >&2
        return 2
    end
    set -l home_quota (command awk -F= '$1 == "HOME_VOLUME_QUOTA" { print $2; exit }' "$versions_file")
    set -l workspace_quota (command awk -F= '$1 == "WORKSPACE_QUOTA" { print $2; exit }' "$versions_file")
    if not string match -rq '^[1-9][0-9]*[KMGT]$' -- "$home_quota"; or not string match -rq '^[1-9][0-9]*[KMGT]$' -- "$workspace_quota"
        echo 'codex: HOME_VOLUME_QUOTA and WORKSPACE_QUOTA must be positive K, M, G, or T sizes' >&2
        return 2
    end

    set -l workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test -z "$workspace"
        set workspace (pwd -P)
    end
    set workspace (realpath "$workspace")
    set -l home_path (realpath "$HOME")
    if test -z "$workspace"; or test "$workspace" = /; or test "$workspace" = "$home_path"
        echo 'codex: refusing to mount an empty path, /, or the complete home directory' >&2
        return 2
    end

    set -l slug (basename "$workspace" | string replace -ra '[^A-Za-z0-9._-]' '-')
    set -l short_hash (printf '%s' "$workspace" | shasum -a 256 | string split ' ' | head -n 1 | string sub -l 12)
    set -l guest_workspace "/workspace/$slug-$short_hash"
    if not msb volume list --quiet | string match -qx codex-home
        msb volume create --kind disk --size "$home_quota" codex-home; or return $status
    end

    # The named home has home_quota; Microsandbox has no per-host-bind quota.
    command msb run --tty --pull never --user node --net public --root-disk "$workspace_quota" \
        --mount-dir "$workspace:$guest_workspace:rw" \
        --mount-named codex-home:/home/node:rw \
        --workdir "$guest_workspace" ai-sandboxes-codex:local -- codex $argv
    return $status
end
