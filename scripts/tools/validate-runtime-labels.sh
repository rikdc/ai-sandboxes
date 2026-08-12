#!/usr/bin/env bash
set -o pipefail

runtime=${1:?usage: validate-runtime-labels.sh RUNTIME SHARED_STATE_ID SHARED_STATE_QUOTA}
state_id=${2?usage: validate-runtime-labels.sh RUNTIME SHARED_STATE_ID SHARED_STATE_QUOTA}
state_quota=${3?usage: validate-runtime-labels.sh RUNTIME SHARED_STATE_ID SHARED_STATE_QUOTA}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd) || exit 1

runtime_values=$("$script_dir/runtime-values.sh" "$runtime") || exit 1
if test -z "$runtime_values"; then
  test -z "$state_id" || exit 1
  test -z "$state_quota" || exit 1
else
  IFS=$'\t' read -r expected_state_id expected_state_quota <<<"$runtime_values"
  test "$state_id" = "$expected_state_id" || exit 1
  test "$state_quota" = "$expected_state_quota" || exit 1
fi
