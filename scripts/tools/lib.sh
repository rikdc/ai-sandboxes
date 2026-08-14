# Shared helpers for the tool-install adapters.
#
# path_is_absent: true iff $1 refers to nothing on disk, including no
# dangling symlink. `test ! -e` alone succeeds for a symlink whose target
# is missing, which would let an installer silently replace the link.
# Every collision guard in the adapters must use this instead of `test ! -e`.
path_is_absent() {
  test ! -e "$1" && test ! -L "$1"
}
