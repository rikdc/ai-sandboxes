source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function __ai_sandbox_impl_claude --description 'Run Claude Code in a hardened Microsandbox VM'
    set -l launcher_file (status filename)
    set -l image ai-sandboxes-claude:local
    __ai_sandbox_claude_egress_args claude >/dev/null; or return $status
    set -l shared_state_args (__ai_sandbox_prepare_shared_state claude "$image"); or return $status
    __ai_sandbox_run_claude "$launcher_file" "$image" (count $shared_state_args) $shared_state_args $argv
end
