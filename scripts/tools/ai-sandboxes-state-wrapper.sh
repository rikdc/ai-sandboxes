#!/bin/sh

fail() {
  echo "$1: invalid shared-state wrapper configuration" >&2
  exit 1
}

binary=$(basename "$0")

case "$binary" in [a-z]*) ;; *) fail "$binary" ;; esac
case "$binary" in *[!a-z0-9-]*) fail "$binary" ;; esac

state_file="/usr/local/libexec/$binary.state"
if ! test -r "$state_file"; then
  fail "$binary"
fi
{
  IFS= read -r state_dir
  IFS= read -r state_env
  IFS= read -r state_db
  # shellcheck disable=SC2034  # extra's value is unused; a successful read means a stray trailing line exists.
  if IFS= read -r extra; then
    fail "$binary"
  fi
} < "$state_file"

case "$state_dir" in [a-z]*) ;; *) fail "$binary" ;; esac
case "$state_dir" in *[!a-z0-9-]*) fail "$binary" ;; esac
case "$state_env" in [A-Z]*) ;; *) fail "$binary" ;; esac
case "$state_env" in *[!A-Z0-9_]*) fail "$binary" ;; esac
case "$state_db" in [a-z]*) ;; *) fail "$binary" ;; esac
case "$state_db" in *[!a-z0-9._-]*) fail "$binary" ;; esac

if [ "${1:-}" = "--version" ]; then
  exec "/usr/local/libexec/$binary" "$@"
fi
if [ ! -d /var/lib/agent-state ] || [ ! -w /var/lib/agent-state ]; then
  echo "$binary: shared state is unavailable; launch through its ai-sandboxes Fish function" >&2
  exit 1
fi
if ! mkdir -p "/var/lib/agent-state/$state_dir"; then
  echo "$binary: cannot create its shared-state directory" >&2
  exit 1
fi
export "$state_env=/var/lib/agent-state/$state_dir/$state_db"
exec "/usr/local/libexec/$binary" "$@"
