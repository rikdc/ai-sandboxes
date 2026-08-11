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
        # A protected root is a plain path baked in at install time, not
        # necessarily canonical: if ~/.config (or any ancestor of it) is
        # itself a symlink, as common dotfiles layouts do, comparing raw
        # strings would miss a workspace that reaches the same directory
        # through its resolved target instead. Canonicalize here, at check
        # time, and fail closed if a protected root cannot be resolved at
        # all rather than silently skipping the check for it.
        set -l resolved_root (realpath "$root" 2>/dev/null)
        if test -z "$resolved_root"
            echo "$agent: refusing to run: could not resolve protected path $root" >&2
            return 1
        end
        if test "$workspace" = "$resolved_root"; or string match -q -- "$resolved_root/*" "$workspace/"; or string match -q -- "$workspace/*" "$resolved_root/"
            echo "$agent: refusing to run: the workspace ($workspace) overlaps a protected ai-sandboxes path ($resolved_root)" >&2
            echo "$agent: a guest agent with write access to the mounted workspace could tamper with host-trusted launcher or wrapper code for a later invocation to run with full host access" >&2
            echo "$agent: run $agent from a different project" >&2
            return 1
        end
    end
    return 0
end
