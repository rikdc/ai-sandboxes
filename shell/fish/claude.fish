# Installed by ai-sandboxes' scripts/install-fish-functions. This wrapper is a
# copy, not a symlink, and lives outside the ai-sandboxes checkout on purpose:
# it is the trust boundary that runs before any checkout-provided code, so a
# guest agent with write access to a mounted ai-sandboxes checkout cannot
# tamper with it. Re-run scripts/install-fish-functions after updating
# ai-sandboxes to refresh it.
function claude --description 'ai-sandboxes: claude (installed wrapper)'
    source '/Users/rdchome/.config/ai-sandboxes/trusted/guard.fish'
    __ai_sandbox_trusted_refuse_overlap claude '/Users/rdchome/repositories/ai-sandboxes' '/Users/rdchome/.config/fish/functions' '/Users/rdchome/.config/ai-sandboxes/trusted'; or return $status
    source '/Users/rdchome/repositories/ai-sandboxes/shell/fish/lib/ai-sandbox.fish'
    source '/Users/rdchome/repositories/ai-sandboxes/shell/fish/claude.fish'
    __ai_sandbox_impl_claude $argv
end
