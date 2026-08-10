#!/usr/bin/env bash
set -o pipefail

phase=${1:?usage: install-selected.sh PHASE CATALOG SELECTION}
catalog=${2:?usage: install-selected.sh PHASE CATALOG SELECTION}
selection=${3:?usage: install-selected.sh PHASE CATALOG SELECTION}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd) || exit 1

install_state_wrapper() {
  install -Dm 0755 "$script_dir/ai-sandboxes-state-wrapper.sh" /usr/local/libexec/ai-sandboxes-state-wrapper
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
  entry=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$catalog") \
    || { echo "unknown tool id: $id" >&2; exit 2; }
  adapter=$(jq -er '.adapter' <<<"$entry") \
    || { echo "missing adapter for tool id: $id" >&2; exit 2; }
  case "$phase:$adapter" in
    runtime:github-release-tar)
      if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/libexec || exit 1
        IFS=$'\t' read -r binary state_dir state_env state_db < <(jq -er '[.binary, .state_wrapper.directory, .state_wrapper.environment, .state_wrapper.database] | @tsv' <<<"$entry") \
          || { echo "failed to read state wrapper fields for $id" >&2; exit 2; }
        validate_state_wrapper_fields "$binary" "$state_dir" "$state_env" "$state_db" \
          || { echo "invalid state wrapper fields for $id" >&2; exit 2; }
        install_state_wrapper || exit 1
        printf '%s\n' "$state_dir" "$state_env" "$state_db" > "/usr/local/libexec/$binary.state" || exit 1
        chmod 0644 "/usr/local/libexec/$binary.state" || exit 1
        ln -sf /usr/local/libexec/ai-sandboxes-state-wrapper "/usr/local/bin/$binary" || exit 1
      else
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/bin || exit 1
      fi ;;
    *)
      echo "unsupported installation phase or adapter: $phase:$adapter" >&2
      exit 2 ;;
  esac
done < <(jq -r '.tools[].id' "$selection")
