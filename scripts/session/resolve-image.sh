#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo_root"

profile_path=${1:?usage: resolve-image.sh PROFILE_PATH}
platform=linux/arm64
schema_version=1
launcher_version=1
base_image=ai-sandboxes-claude:local

die() {
  printf 'resolve-image: %s\n' "$*" >&2
  exit 1
}

canonical=$(scripts/session/validate-profile.sh "$profile_path") || exit 1

base_digest=$(docker image inspect --format '{{.Id}}' "$base_image" 2>/dev/null) \
  || die "base image not found: $base_image (run ./scripts/build first)"

cache_key=$(printf '%s\n%s\n%s\n%s\n%s\n' "$base_digest" "$canonical" "$platform" "$schema_version" "$launcher_version" \
  | shasum -a 256 | awk '{print $1}')
tag="ai-sandboxes-claude-session:sha-$cache_key"

if docker image inspect "$tag" >/dev/null 2>&1; then
  printf '%s\n' "$tag"
  exit 0
fi

lock_dir="${TMPDIR:-/tmp}/ai-sandboxes-session-lock-$cache_key"
attempt=0
until mkdir "$lock_dir" 2>/dev/null; do
  attempt=$((attempt + 1))
  test "$attempt" -lt 3600 || die 'timed out waiting for another build of this session image (waited 30 minutes)'
  sleep 0.5
done
trap 'rmdir "$lock_dir" 2>/dev/null || true' EXIT

# Re-check now that the lock is held: another process may have finished
# building this exact key while we were waiting.
if docker image inspect "$tag" >/dev/null 2>&1; then
  printf '%s\n' "$tag"
  exit 0
fi

test "${CLAUDE_MSB_BUILD_EGRESS:-}" = 1 \
  || die 'cache miss requires CLAUDE_MSB_BUILD_EGRESS=1 to build (see docs/session-images.md)'

context_dir=$(mktemp -d)
trap 'rmdir "$lock_dir" 2>/dev/null || true; rm -rf "$context_dir"' EXIT

built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
jq -n \
  --argjson request "$canonical" \
  --arg base_image "$base_image" \
  --arg base_digest "$base_digest" \
  --arg platform "$platform" \
  --argjson schema_version "$schema_version" \
  --argjson launcher_version "$launcher_version" \
  --arg built_at "$built_at" \
  --arg cache_key "$cache_key" \
  '{
    canonical_request: $request,
    base_image: $base_image,
    base_digest: $base_digest,
    platform: $platform,
    schema_version: $schema_version,
    launcher_version: $launcher_version,
    packages: {
      apt: ($request.apt // []),
      npm: ($request.npm // []),
      python: (($request.python // {}).packages // [])
    },
    claude_marketplaces: ($request.claude_marketplaces // []),
    built_at: $built_at,
    cache_key: $cache_key
  }' >"$context_dir/resolved.json" \
  || die "failed to generate resolved.json metadata"

scripts/session/render-dockerfile.sh "$context_dir" \
  || die "failed to render Dockerfile for context $context_dir"

docker build \
  --platform "$platform" \
  --tag "$tag" \
  --label io.ai-sandboxes.session-image=1 \
  --label io.ai-sandboxes.session-cache-key="$cache_key" \
  "$context_dir" >&2 \
  || die "docker build failed for tag $tag"

printf '%s\n' "$tag"
