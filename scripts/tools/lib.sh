# shellcheck shell=bash
# Shared helpers for the tool-install adapters. Sourced by every
# scripts/tools/install-*.sh, so no shebang -- the `shell=bash` directive
# above is for shellcheck's benefit only.
#
# path_is_absent: true iff $1 refers to nothing on disk, including no
# dangling symlink. `test ! -e` alone succeeds for a symlink whose target
# is missing, which would let an installer silently replace the link.
# Every collision guard in the adapters must use this instead of `test ! -e`.
path_is_absent() {
  test ! -e "$1" && test ! -L "$1"
}

# KNOWN_ADAPTERS is the authoritative list of tool-install adapters. Three
# sites act on the adapter string (validate-selection.sh, install-selected.sh,
# render-dockerfile.sh); the value flows into an `install-$adapter.sh` path,
# so any site that dispatches on it must reject unknown values. Adding a new
# adapter means updating this list plus the per-adapter branches in those
# three scripts; scripts/tools/tests/test-adapters.sh enforces the invariant.
KNOWN_ADAPTERS=(github-release-tar https-tar awscli-zip)

is_known_adapter() {
  local candidate=$1 known
  for known in "${KNOWN_ADAPTERS[@]}"; do
    test "$candidate" = "$known" && return 0
  done
  return 1
}
