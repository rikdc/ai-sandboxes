#!/usr/bin/env bash
set -o pipefail

version=${1:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}
fingerprint=${2:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}
destination=${3:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}

die() {
  printf 'install-binary: %s\n' "$*" >&2
  exit 1
}

keyring=$(mktemp -d) || die 'could not create a scratch keyring directory'
workdir=$(mktemp -d) || die 'could not create a scratch work directory'
trap 'rm -rf "$keyring" "$workdir"' EXIT
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

curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/linux-arm64/claude" -o "$workdir/claude" \
  || die "could not download claude binary for $version"
echo "${expected_checksum}  $workdir/claude" | sha256sum --check --status \
  || die 'downloaded claude binary does not match manifest checksum'
install -m 0755 "$workdir/claude" "$destination/claude" || die "could not install claude to $destination"
test "$("$destination/claude" --version | awk '{print $1}')" = "$version" \
  || die "installed claude --version does not match requested $version"
