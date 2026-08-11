# Installed verbatim by scripts/install-fish-functions to a location outside
# any ai-sandboxes checkout (see that script). Do not source this file
# directly from the checkout: the installed copy, not this source template,
# is the trust boundary that the generated wrappers in
# ~/.config/fish/functions/ check before sourcing any checkout-provided code.
#
# Takes the agent name followed by every protected root to check the
# workspace against: the ai-sandboxes checkout, but also the wrapper and
# guard's own installed directories. Protecting only the checkout would leave
# a gap, since --argument-names does not shift $argv, so $argv[2..-1] (not a
# second named parameter) is how the caller passes a variable-length list.
function __ai_sandbox_trusted_refuse_overlap --argument-names agent
    set -l protected_roots $argv[2..-1]
    set -l workspace (command git rev-parse --show-toplevel 2>/dev/null)
    if test -z "$workspace"
        set workspace (pwd -P)
    end
    set workspace (realpath "$workspace" 2>/dev/null)
    if test -z "$workspace"
        return 0
    end
    for root in $protected_roots
        if test "$workspace" = "$root"; or string match -q -- "$root/*" "$workspace/"; or string match -q -- "$workspace/*" "$root/"
            echo "$agent: refusing to run: the workspace ($workspace) overlaps a protected ai-sandboxes path ($root)" >&2
            echo "$agent: a guest agent with write access to the mounted workspace could tamper with host-trusted launcher or wrapper code for a later invocation to run with full host access" >&2
            echo "$agent: run $agent from a different project" >&2
            return 1
        end
    end
    return 0
end
