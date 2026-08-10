#!/usr/bin/env bash
set -euo pipefail

version=${1:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}
fingerprint=${2:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}
destination=${3:?usage: install-binary.sh VERSION FINGERPRINT DESTINATION}

keyring=$(mktemp -d)
workdir=$(mktemp -d)
trap 'rm -rf "$keyring" "$workdir"' EXIT
export GNUPGHOME="$keyring"

curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o "$workdir/claude-code.asc"
gpg --batch --dearmor -o "$workdir/claude-code.gpg" "$workdir/claude-code.asc"
# Require the downloaded key file to contain exactly the pinned fingerprint
# and nothing else: scoping --verify to this file only helps if the file
# cannot also smuggle in an additional, attacker-controlled key.
test "$(gpg --show-keys --with-colons "$workdir/claude-code.gpg" | awk -F: '$1 == "fpr" { print $10 }' | sort -u)" = "$fingerprint"

curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/manifest.json" -o "$workdir/manifest.json"
curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/manifest.json.sig" -o "$workdir/manifest.json.sig"
gpg --batch --no-default-keyring --keyring "$workdir/claude-code.gpg" --verify "$workdir/manifest.json.sig" "$workdir/manifest.json"

jq -e --arg version "$version" '.version == $version' "$workdir/manifest.json" >/dev/null
expected_checksum=$(jq -er '.platforms["linux-arm64"].checksum | select(test("^[[:xdigit:]]{64}$"))' "$workdir/manifest.json")

curl -fsSL "https://downloads.claude.ai/claude-code-releases/${version}/linux-arm64/claude" -o "$workdir/claude"
echo "${expected_checksum}  $workdir/claude" | sha256sum --check --status
install -m 0755 "$workdir/claude" "$destination/claude"
test "$("$destination/claude" --version | awk '{print $1}')" = "$version"
