source (dirname (realpath (status filename)))/lib/ai-sandbox.fish

function codex --description 'Run OpenAI Codex in an ephemeral Microsandbox VM'
    __ai_sandbox_launch (status filename) codex ai-sandboxes-codex:local codex-home "$argv"
end
