#!/usr/bin/env bash
set -o pipefail

runtime=${1:?usage: runtime-values.sh RUNTIME}

jq -r 'if .shared_state == null then empty else [.shared_state.id, .shared_state.quota] | @tsv end' "$runtime"
