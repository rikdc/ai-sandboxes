source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function __ai_sandbox_impl_claude_session --description 'Run Claude Code in a session image built from an explicit profile'
    if test (count $argv) -lt 2; or test "$argv[1]" != --profile
        echo 'claude-session: usage: claude-session --profile PATH_OR_NAME [claude arguments...]' >&2
        return 2
    end

    set -l launcher_file (status filename)
    set -l repo_root (dirname (dirname (dirname (realpath "$launcher_file"))))
    set -l profile_value $argv[2]
    set -l claude_args $argv[3..-1]

    set -l profile_candidate $profile_value
    if not string match -q '*/*' -- "$profile_value"
        set profile_candidate "$HOME/.config/microvms/profiles/$profile_value.json"
    end

    set -l profile_path (realpath "$profile_candidate" 2>/dev/null)
    if test -z "$profile_path"; or not test -f "$profile_path"
        echo "claude-session: profile not found: $profile_candidate" >&2
        return 1
    end

    if not type -q msb
        echo 'claude-session: msb is not installed or is not on PATH' >&2
        return 127
    end

    # Defense in depth: the real trust boundary is the installed wrapper from
    # scripts/install-fish-functions, which runs this same check *before*
    # sourcing this file at all (see shell/fish/trusted/guard.fish). A check
    # inside a file that a guest with write access to this checkout could also
    # edit cannot be the primary control, but this still catches direct or
    # pre-wrapper invocations of the checkout's own implementation.
    set -l workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test -z "$workspace"
        set workspace (pwd -P)
    end
    set workspace (realpath "$workspace")
    __ai_sandbox_refuse_workspace_overlap claude-session "$launcher_file" "$workspace"; or return $status

    set -l resolved_image ("$repo_root/scripts/session/resolve-image.sh" "$profile_path"); or return $status
    "$repo_root/scripts/session/load-image.sh" "$resolved_image"; or return $status

    # load-image.sh only skips loading when msb already has *a* image under
    # this exact tag; it does not (and, being a generic loader reused for
    # non-session images, should not) verify that image's identity. `msb load`
    # currently drops OCI labels, so compare the OCI config digest it does
    # retain with Docker's image ID instead. This detects a stale or tampered
    # msb-side image without trusting the mutable tag alone.
    set -l expected_config_digest (docker image inspect --format '{{.Id}}' "$resolved_image"); or begin
        echo "claude-session: cannot inspect Docker image $resolved_image" >&2
        return 1
    end
    set -l metadata (msb image inspect --format json "$resolved_image"); or return $status
    set -l msb_config_digest (printf '%s\n' "$metadata" | jq -er '.config.digest'); or begin
        echo "claude-session: cannot read the config digest of msb image $resolved_image" >&2
        return 1
    end
    if test "$msb_config_digest" != "$expected_config_digest"
        echo "claude-session: msb image $resolved_image does not match Docker image $resolved_image; remove it (msb image remove $resolved_image) before retrying" >&2
        return 1
    end

    __ai_sandbox_run_claude "$launcher_file" "$resolved_image" $claude_args
end
