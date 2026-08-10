source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude-session --description 'Run Claude Code in a session image built from an explicit profile'
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

    # resolve-image.sh and its helpers run as host-side bash with real Docker
    # authority, before the guest VM ever starts. If the mounted workspace
    # overlaps this checkout, a guest with write access to that mount could
    # tamper with those scripts for a later host-side invocation to trust.
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
    # non-session images, should not) verify that image's identity. Verify it
    # here instead of trusting a possibly stale or tampered msb-side image.
    set -l expected_cache_key (string replace -r '^ai-sandboxes-claude-session:sha-' '' -- "$resolved_image")
    set -l metadata (msb image inspect --format json "$resolved_image"); or return $status
    set -l image_flags (string match --regex --groups-only '"io\.ai-sandboxes\.session-image"[[:space:]]*:[[:space:]]*"([^"]*)"' -- "$metadata")
    set -l cache_keys (string match --regex --groups-only '"io\.ai-sandboxes\.session-cache-key"[[:space:]]*:[[:space:]]*"([^"]*)"' -- "$metadata")
    if test (count $image_flags) -ne 1; or test "$image_flags[1]" != 1; or test (count $cache_keys) -ne 1; or test "$cache_keys[1]" != "$expected_cache_key"
        echo "claude-session: msb image $resolved_image does not carry the expected session-image labels; remove it (msb image remove $resolved_image) before retrying" >&2
        return 1
    end

    __ai_sandbox_run_claude "$launcher_file" "$resolved_image" $claude_args
end
