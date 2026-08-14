# ai-sandbox is the control plane: it resolves this invocation into one typed
# RuntimePlan and launches it, so this function is only a pass-through. The
# trust boundary is the wrapper installed by scripts/install-fish-functions,
# which checks protected-path overlap before running anything from the
# checkout. Defense in depth: ai-sandbox itself refuses workspaces that
# overlap protected paths (including this checkout) before launching.
function __ai_sandbox_impl_claude_session --description 'Run Claude Code in a session image built from an explicit profile'
    command ai-sandbox run claude $argv
end
