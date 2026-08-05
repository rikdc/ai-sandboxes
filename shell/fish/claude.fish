source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude --description 'Run Claude Code in an ephemeral Microsandbox VM'
    if not type -q msb
        echo 'claude: msb is not installed or not on PATH' >&2
        return 127
    end

    set -l quotas (__ai_sandbox_quotas (status filename) claude); or return $status
    set -l home_quota $quotas[1]
    set -l workspace_quota $quotas[2]

    set -l workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test -z "$workspace"
        set workspace (pwd -P)
    end
    set workspace (realpath "$workspace")
    set -l home_path (realpath "$HOME")
    if test -z "$workspace"; or test "$workspace" = /; or test "$workspace" = "$home_path"
        echo 'claude: refusing to mount an empty path, /, or the complete home directory' >&2
        return 2
    end

    set -l slug (basename "$workspace" | string replace -ra '[^A-Za-z0-9._-]' '-')
    set -l short_hash (printf '%s' "$workspace" | shasum -a 256 | string split ' ' | head -n 1 | string sub -l 12)
    set -l guest_workspace "/workspace/$slug-$short_hash"
    if not msb volume list --quiet | string match -qx claude-home
        msb volume create --kind disk --size "$home_quota" claude-home; or return $status
    end

    # The named home has home_quota; Microsandbox has no per-host-bind quota.
    command msb run --tty --pull never --user node --net public --root-disk "$workspace_quota" \
        --mount-dir "$workspace:$guest_workspace:rw" \
        --mount-named claude-home:/home/node:rw \
        --workdir "$guest_workspace" ai-sandboxes-claude:local -- claude $argv
    return $status
end
