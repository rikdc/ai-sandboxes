#!/usr/bin/env bash
set -euo pipefail

phase=${1:?usage: install-selected.sh PHASE CATALOG SELECTION}
catalog=${2:?usage: install-selected.sh PHASE CATALOG SELECTION}
selection=${3:?usage: install-selected.sh PHASE CATALOG SELECTION}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

install_state_wrapper() {
  cat > /usr/local/libexec/ai-sandboxes-state-wrapper <<'EOF'
#!/bin/sh
set -eu

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
  chmod 0755 /usr/local/libexec/ai-sandboxes-state-wrapper
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
  entry=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$catalog")
  adapter=$(jq -er '.adapter' <<<"$entry")
  case "$phase:$adapter" in
    runtime:github-release-tar)
      if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/libexec
        IFS=$'\t' read -r binary state_dir state_env state_db < <(jq -er '[.binary, .state_wrapper.directory, .state_wrapper.environment, .state_wrapper.database] | @tsv' <<<"$entry")
        validate_state_wrapper_fields "$binary" "$state_dir" "$state_env" "$state_db" \
          || { echo "invalid state wrapper fields for $id" >&2; exit 2; }
        install_state_wrapper
        printf '%s\n' "$state_dir" "$state_env" "$state_db" > "/usr/local/libexec/$binary.state"
        chmod 0644 "/usr/local/libexec/$binary.state"
        ln -sf /usr/local/libexec/ai-sandboxes-state-wrapper "/usr/local/bin/$binary"
      else
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/bin
      fi ;;
    *)
      echo "unsupported installation phase or adapter: $phase:$adapter" >&2
      exit 2 ;;
  esac
done < <(jq -r '.tools[].id' "$selection")
