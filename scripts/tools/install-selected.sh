#!/usr/bin/env bash
set -euo pipefail

phase=${1:?usage: install-selected.sh PHASE CATALOG SELECTION}
catalog=${2:?usage: install-selected.sh PHASE CATALOG SELECTION}
selection=${3:?usage: install-selected.sh PHASE CATALOG SELECTION}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

while IFS= read -r id; do
  entry=$(jq -ce --arg id "$id" '.tools[] | select(.id == $id)' "$catalog")
  adapter=$(jq -er '.adapter' <<<"$entry")
  case "$phase:$adapter" in
    runtime:github-release-tar)
      if jq -e '.state_wrapper != null' <<<"$entry" >/dev/null; then
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/libexec
        IFS=$'\t' read -r binary state_dir state_env state_db < <(jq -er '[.binary, .state_wrapper.directory, .state_wrapper.environment, .state_wrapper.database] | @tsv' <<<"$entry")
        cat > "/usr/local/bin/$binary" <<EOF
#!/bin/sh
set -eu
if [ "\${1:-}" = "--version" ]; then
  exec /usr/local/libexec/$binary "\$@"
fi
if [ ! -d /var/lib/agent-state ] || [ ! -w /var/lib/agent-state ]; then
  echo '$binary: shared state is unavailable; launch through its ai-sandboxes Fish function' >&2
  exit 1
fi
if ! mkdir -p /var/lib/agent-state/$state_dir; then
  echo '$binary: cannot create its shared-state directory' >&2
  exit 1
fi
export $state_env=/var/lib/agent-state/$state_dir/$state_db
exec /usr/local/libexec/$binary "\$@"
EOF
        chmod 0755 "/usr/local/bin/$binary"
      else
        "$script_dir/install-github-release-tar.sh" "$catalog" "$selection" "$id" /usr/local/bin
      fi ;;
    *)
      echo "unsupported installation phase or adapter: $phase:$adapter" >&2
      exit 2 ;;
  esac
done < <(jq -r '.tools[].id' "$selection")
