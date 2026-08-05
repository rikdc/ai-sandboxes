source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function claude --description 'Run Claude Code in an ephemeral Microsandbox VM'
    __ai_sandbox_launch (status filename) claude ai-sandboxes-claude:local claude-home $argv
end
