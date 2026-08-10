source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude --description 'Run Claude Code in a hardened Microsandbox VM'
    __ai_sandbox_run_claude (status filename) ai-sandboxes-claude:local $argv
end
