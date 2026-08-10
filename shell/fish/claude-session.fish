source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude-session --description 'Run Claude Code in a session image built from an explicit profile'
    if test (count $argv) -lt 2; or test "$argv[1]" != --profile
        echo 'claude-session: usage: claude-session --profile PATH_OR_NAME [claude arguments...]' >&2
        return 2
    end

    set -l repo_root (dirname (dirname (dirname (realpath (status filename)))))
    set -l profile_value $argv[2]
    set -l claude_args $argv[3..-1]

    set -l profile_path $profile_value
    if not string match -q '*/*' -- "$profile_value"
        set profile_path "$HOME/.config/microvms/profiles/$profile_value.json"
    end

    if not test -f "$profile_path"
        echo "claude-session: profile not found: $profile_path" >&2
        return 1
    end

    if not type -q msb
        echo 'claude-session: msb is not installed or is not on PATH' >&2
        return 127
    end

    set -l resolved_image ("$repo_root/scripts/session/resolve-image.sh" "$profile_path"); or return $status
    "$repo_root/scripts/session/load-image.sh" "$resolved_image"; or return $status

    __ai_sandbox_run_claude "$resolved_image" $claude_args
end
