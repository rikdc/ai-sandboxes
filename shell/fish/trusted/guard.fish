# Installed verbatim by scripts/install-fish-functions to a location outside
# any ai-sandboxes checkout (see that script). Do not source this file
# directly from the checkout: the installed copy, not this source template,
# is the trust boundary that the generated wrappers in
# ~/.config/fish/functions/ check before sourcing any checkout-provided code.
function __ai_sandbox_trusted_refuse_overlap --argument-names agent repo_root
    set -l workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test -z "$workspace"
        set workspace (pwd -P)
    end
    set workspace (realpath "$workspace" 2>/dev/null)
    if test -z "$workspace"
        return 0
    end
    if test "$workspace" = "$repo_root"; or string match -q -- "$repo_root/*" "$workspace/"; or string match -q -- "$workspace/*" "$repo_root/"
        echo "$agent: refusing to run: the workspace ($workspace) overlaps the ai-sandboxes checkout that provides this launcher ($repo_root)" >&2
        echo "$agent: a guest agent with write access to the mounted workspace could tamper with host-trusted launcher code for a later invocation to run with full host access" >&2
        echo "$agent: run $agent from a different project, or use a separately installed copy of ai-sandboxes" >&2
        return 1
    end
    return 0
end
