#!/usr/bin/env bash
set -o pipefail

version=${1:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}
fingerprint=${2:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}
destination=${3:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}

die() {
  printf 'install-binary: %s\n' "$*" >&2
  exit 1
}

# Fail with a clear message before mktemp does, including for a stray file or a
# literally-empty (but set) third argument that the ${3:?} expansion cannot
# catch. These checks also guarantee the prune and the staging dir below can
# only ever touch a real directory.
[[ -d $destination ]] || die "destination $destination is not a directory"
[[ -w $destination ]] || die "destination $destination is not writable"

# A crash (SIGKILL, power loss) between mktemp and the EXIT trap leaves a hidden
# staging dir behind; the randomized name means repeats accumulate. Prune stale
# leftovers from earlier interrupted runs so this run starts clean.
find "$destination" -maxdepth 1 -name '.claude-install.*' -type d -mmin +60 -exec rm -rf {} + 2>/dev/null

keyring=""
workdir=""
staging=""
keyring=$(mktemp -d) || die 'could not create a scratch keyring directory'
workdir=$(mktemp -d) || die 'could not create a scratch work directory'
# The finished binary must land in $destination atomically: /tmp is often a
# separate filesystem, so a file staged there would make the final move a copy
# rather than a rename. Stage inside $destination itself instead so the last
# `mv` is a same-filesystem atomic swap.
staging=$(mktemp -d "$destination/.claude-install.XXXXXX") || die "could not create a staging directory inside $destination"
trap 'rm -rf "$keyring" "$workdir" "$staging"' EXIT
export GNUPGHOME="$keyring"

curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o "$workdir/claude-code.asc" \
  || die 'could not download claude-code signing key'
gpg --batch --dearmor -o "$workdir/claude-code.gpg" "$workdir/claude-code.asc" \
  || die 'could not import claude-code signing key'
# Require the downloaded key file to contain exactly the pinned fingerprint
# and nothing else: scoping --verify to this file only helps if the file
# cannot also smuggle in an additional, attacker-controlled key.
test "$(gpg --show-keys --with-colons "$workdir/claude-code.gpg" | awk -F: '$1 == "fpr" { print $10 }' | sort -u)" = "$fingerprint" \
  || die "downloaded signing key does not match pinned fingerprint $fingerprint"

curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/manifest.json" -o "$workdir/manifest.json" \
  || die "could not download manifest.json for $version"
curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/manifest.json.sig" -o "$workdir/manifest.json.sig" \
  || die "could not download manifest.json.sig for $version"
gpg --batch --no-default-keyring --keyring "$workdir/claude-code.gpg" --verify "$workdir/manifest.json.sig" "$workdir/manifest.json" \
  || die 'manifest.json signature verification failed'

jq -e --arg version "$version" '.version == $version' "$workdir/manifest.json" >/dev/null \
  || die "manifest.json version does not match requested $version"
expected_checksum=$(jq -er '.platforms["linux-arm64"].checksum | select(test("^[[:xdigit:]]{64}$"))' "$workdir/manifest.json") \
  || die 'manifest.json has no valid linux-arm64 checksum'

curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/linux-arm64/claude" -o "$staging/claude" \
  || die "could not download claude binary for $version"
# sha256sum --check requires exactly two spaces between the digest and the path;
# a single space silently skips verification.
echo "${expected_checksum}  $staging/claude" | sha256sum --check --status \
  || die 'downloaded claude binary does not match manifest checksum'
chmod 0755 "$staging/claude" || die 'could not set mode on installed claude binary'
mv -f "$staging/claude" "$destination/claude" || die "could not install claude to $destination"
test "$("$destination/claude" --version | awk '{print $1}')" = "$version" \
  || die "installed claude --version does not match requested $version"
