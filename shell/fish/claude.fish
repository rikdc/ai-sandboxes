source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function __ai_sandbox_impl_claude --description 'Run Claude Code in a hardened Microsandbox VM'
    set -l image ai-sandboxes-claude:local
    if not type -q msb
        echo 'claude: msb is not installed or is not on PATH' >&2
        return 127
    end
    set -l shared_state_args (__ai_sandbox_prepare_shared_state claude "$image"); or return $status
    __ai_sandbox_run_claude (status filename) "$image" (count $shared_state_args) $shared_state_args $argv
end
