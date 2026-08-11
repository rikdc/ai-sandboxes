#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo_root" || exit 1

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

# A change to the marketplace installer or the seed-merge helper should also
# bust the session-image cache, for the same reason the renderer itself is
# hashed: all three are trusted inputs to what a cached tag's content
# actually is. Hash each file individually and then hash that listing
# (rather than concatenating file contents directly) so a change shifting
# bytes across a file boundary can't produce a collision.
renderer_hash=$(shasum -a 256 scripts/session/render-dockerfile.sh scripts/marketplaces/install-claude.sh scripts/session/merge-plugin-seed.sh scripts/session/install-apt-packages.sh scripts/session/install-npm-packages.sh \
  | shasum -a 256 | awk '{print $1}')

cache_key=$(printf '%s\n%s\n%s\n%s\n%s\n%s\n' "$base_digest" "$canonical" "$platform" "$schema_version" "$launcher_version" "$renderer_hash" \
  | shasum -a 256 | awk '{print $1}')
tag="ai-sandboxes-claude-session:sha-$cache_key"

# A tag is just a mutable pointer: something other than this script could have
# written an unrelated (or stale, pre-labeling-scheme) image under this exact
# predictable name. Require the labels this script itself writes on build to
# match the computed cache key before trusting a tag as a real cache hit, per
# the documented cache-identity design in docs/session-images.md. Fail closed
# on mismatch rather than silently rebuilding over or reusing untrusted content.
image_labels_match() {
  local check_tag=$1
  local image_flag session_key
  image_flag=$(docker image inspect --format '{{ index .Config.Labels "io.ai-sandboxes.session-image" }}' "$check_tag" 2>/dev/null) || return 1
  session_key=$(docker image inspect --format '{{ index .Config.Labels "io.ai-sandboxes.session-cache-key" }}' "$check_tag" 2>/dev/null) || return 1
  test "$image_flag" = 1 && test "$session_key" = "$cache_key"
}

if docker image inspect "$tag" >/dev/null 2>&1; then
  image_labels_match "$tag" \
    || die "existing image $tag does not carry the expected session-image labels; remove it manually (docker image rm $tag) before retrying"
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
  image_labels_match "$tag" \
    || die "existing image $tag does not carry the expected session-image labels; remove it manually (docker image rm $tag) before retrying"
  printf '%s\n' "$tag"
  exit 0
fi

test "${CLAUDE_MSB_BUILD_EGRESS:-}" = 1 \
  || die 'cache miss requires CLAUDE_MSB_BUILD_EGRESS=1 to build (see docs/session-images.md)'

context_dir=$(mktemp -d)

# A registry-style name@digest reference isn't enough to pin FROM to this
# exact local image: BuildKit resolves it via manifest-digest lookup, which
# for a purely local (never pushed) image means a doomed registry pull rather
# than a local-store match, as CI confirmed. Instead, create a private tag
# that points at the base image's current content right now, verify it still
# matches the digest the cache key was computed from (closing, though not
# fully eliminating, the window for a concurrent `./scripts/build` to retag
# the base first), and build FROM that private tag. A private tag name is
# resolved purely against the local store, regardless of buildx driver.
pinned_base="ai-sandboxes-claude-session-base:$cache_key"
docker tag "$base_image" "$pinned_base" \
  || die "failed to pin base image $base_image for build"
test "$(docker image inspect --format '{{.Id}}' "$pinned_base" 2>/dev/null)" = "$base_digest" \
  || die "base image $base_image changed while resolving; retry"

trap 'rmdir "$lock_dir" 2>/dev/null || true; rm -rf "$context_dir"; docker image rm "$pinned_base" >/dev/null 2>&1 || true' EXIT

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

scripts/session/render-dockerfile.sh "$context_dir" "$pinned_base" "$canonical" \
  || die "failed to render Dockerfile for context $context_dir"

# The active buildx builder (e.g. CI's docker/setup-buildx-action instance) may
# use the docker-container driver, whose BuildKit runs isolated from the host
# engine's image store and cannot resolve our locally built base image as a
# FROM reference. A builder using the docker driver shares the engine's image
# store directly, so pin this build to that one instead. Only one docker-driver
# builder can exist per host, already auto-registered for the current context,
# so find it rather than trying to create a new one.
local_builder=$(docker buildx ls | awk '$1 !~ /^\\_/ && $2 == "docker" { sub(/\*$/, "", $1); print $1; exit }')
test -n "$local_builder" || die 'no buildx builder using the docker driver found'

docker buildx build --builder "$local_builder" --load \
  --platform "$platform" \
  --tag "$tag" \
  --label io.ai-sandboxes.session-image=1 \
  --label io.ai-sandboxes.session-cache-key="$cache_key" \
  "$context_dir" >&2 \
  || die "docker build failed for tag $tag"

printf '%s\n' "$tag"
