source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function __ai_sandbox_impl_claude --description 'Run Claude Code in a hardened Microsandbox VM'
    set -l shared_state_args (__ai_sandbox_prepare_shared_state claude ai-sandboxes-claude:local); or return $status
    __ai_sandbox_run_claude (status filename) ai-sandboxes-claude:local (count $shared_state_args) $shared_state_args $argv
end
