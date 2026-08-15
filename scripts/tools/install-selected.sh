#!/usr/bin/env bash
set -o pipefail

phase=${1:?usage: install-selected.sh PHASE CATALOG SELECTION}
catalog=${2:?usage: install-selected.sh PHASE CATALOG SELECTION}
selection=${3:?usage: install-selected.sh PHASE CATALOG SELECTION}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd) || exit 1

# shellcheck source=scripts/tools/lib.sh
. "$script_dir/lib.sh" || exit 1

install_state_wrapper() {
  cat > /usr/local/libexec/ai-sandboxes-state-wrapper <<'EOF' || return 1
#!/bin/sh

fail() {
  echo "$1: invalid shared-state wrapper configuration" >&2
  exit 1
}

binary=$(basename "$0")
state_file="/usr/local/libexec/$binary.state"
if ! test -r "$state_file"; then
  fail "$binary"
fi
{
  IFS= read -r state_dir
  IFS= read -r state_env
  IFS= read -r state_db
  IFS= read -r extra && fail "$binary" || :
} < "$state_file"

case "$binary" in [a-z]*) ;; *) fail "$binary" ;; esac
case "$binary" in *[!a-z0-9-]*) fail "$binary" ;; esac
case "$state_dir" in [a-z]*) ;; *) fail "$binary" ;; esac
case "$state_dir" in *[!a-z0-9-]*) fail "$binary" ;; esac
case "$state_env" in [A-Z]*) ;; *) fail "$binary" ;; esac
case "$state_env" in *[!A-Z0-9_]*) fail "$binary" ;; esac
case "$state_db" in [a-z]*) ;; *) fail "$binary" ;; esac
case "$state_db" in *[!a-z0-9._-]*) fail "$binary" ;; esac

if [ "${1:-}" = "--version" ]; then
  exec "/usr/local/libexec/$binary" "$@"
  echo "$binary: failed to exec /usr/local/libexec/$binary" >&2
  exit 1
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
EOF
  chmod 0755 /usr/local/libexec/ai-sandboxes-state-wrapper || return 1
}

validate_state_wrapper_fields() {
  local binary=$1 state_dir=$2 state_env=$3 state_db=$4
  case "$binary" in [a-z]*) ;; *) return 1 ;; esac
  case "$binary" in *[!a-z0-9-]*) return 1 ;; esac
  case "$state_dir" in [a-z]*) ;; *) return 1 ;; esac
  case "$state_dir" in *[!a-z0-9-]*) return 1 ;; esac
  case "$state_env" in [A-Z]*) ;; *) return 1 ;; esac
  case "$state_env" in *[!A-Z0-9_]*) return 1 ;; esac
  case "$state_db" in [a-z]*) ;; *) return 1 ;; esac
  case "$state_db" in *[!a-z0-9._-]*) return 1 ;; esac
}

while IFS= read -r id; do
  entry=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$catalog") || exit 2
  adapter=$(jq -er '.adapter' <<<"$entry") || exit 2
  case "$phase:$adapter" in
    runtime:github-release-tar)
      if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/libexec || exit 1
        IFS=$'\t' read -r binary state_dir state_env state_db < <(jq -er '[.binary, .state_wrapper.directory, .state_wrapper.environment, .state_wrapper.database] | @tsv' <<<"$entry") \
          || exit 2
        validate_state_wrapper_fields "$binary" "$state_dir" "$state_env" "$state_db" \
          || { echo "invalid state wrapper fields for $id" >&2; exit 2; }
        install_state_wrapper || exit 1
        printf '%s\n' "$state_dir" "$state_env" "$state_db" > "/usr/local/libexec/$binary.state" || exit 1
        chmod 0644 "/usr/local/libexec/$binary.state" || exit 1
        path_is_absent "/usr/local/bin/$binary" || { echo "refusing to overwrite /usr/local/bin/$binary with state-wrapper launcher for $id" >&2; exit 1; }
        ln -s /usr/local/libexec/ai-sandboxes-state-wrapper "/usr/local/bin/$binary" || exit 1
      else
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/bin || exit 1
      fi ;;
    runtime:https-tar|runtime:awscli-zip)
      # A state-wrapper installation keeps the real binary in /usr/local/libexec
      # and a launcher in /usr/local/bin, but neither of these adapters installs
      # a single movable binary: https-tar may install a whole toolchain tree
      # (see install-https-tar.sh) and awscli-zip carries an installer plus a
      # dist/ tree, so neither layout is wrapper-compatible. Validation rejects
      # a state_wrapper on these entries; install straight to /usr/local/bin.
      "$script_dir/install-$adapter.sh" "$catalog" "$selection" "$id" /usr/local/bin || exit 1
      ;;
    *)
      echo "unsupported installation phase or adapter: $phase:$adapter" >&2
      exit 2 ;;
  esac
done < <(jq -r '.tools[].id' "$selection")
