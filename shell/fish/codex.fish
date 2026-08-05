source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function codex --description 'Run OpenAI Codex in an ephemeral Microsandbox VM'
    if not type -q msb
        echo 'codex: msb is not installed or not on PATH' >&2
        return 127
    end

    set -l workspace_quota (__ai_sandbox_workspace_quota (status filename) codex); or return $status

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
        msb volume create codex-home; or return $status
    end

    # Microsandbox directory volumes are writable by node; only the root disk is quota-backed.
    command msb run --tty --pull never --user node --net public --root-disk "$workspace_quota" \
        --mount-dir "$workspace:$guest_workspace:rw" \
        --mount-named codex-home:/home/node:rw \
        --workdir "$guest_workspace" ai-sandboxes-codex:local -- codex $argv
    return $status
end
