#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

if ! docker image inspect ai-sandboxes-claude:local >/dev/null 2>&1; then
  echo 'skip: ai-sandboxes-claude:local not built (run ./scripts/build)' >&2
  exit 0
fi

rtk_tag=''
icm_tag=''
golang_tag=''
awscli_tag=''
cleanup() {
  if test -n "$rtk_tag"; then
    docker image rm -f "$rtk_tag" >/dev/null 2>&1 || true
  fi
  if test -n "$icm_tag"; then
    docker image rm -f "$icm_tag" >/dev/null 2>&1 || true
  fi
  if test -n "$golang_tag"; then
    docker image rm -f "$golang_tag" >/dev/null 2>&1 || true
  fi
  if test -n "$awscli_tag"; then
    docker image rm -f "$awscli_tag" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# rtk has no state_wrapper: install-selected.sh installs it straight to
# /usr/local/bin, runnable directly by node.
rtk_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/rtk-only.json | jq -er '.image') || exit 1

docker run --rm --user node "$rtk_tag" rtk --version >/dev/null \
  || { echo 'curated tool rtk does not run as node' >&2; exit 1; }
docker run --rm --user node "$rtk_tag" sh -c '! touch /usr/local/bin/.write-test 2>/dev/null' \
  || { echo '/usr/local/bin is writable by node' >&2; exit 1; }
docker run --rm --user node "$rtk_tag" sh -c '! touch /usr/local/libexec/.write-test 2>/dev/null' \
  || { echo '/usr/local/libexec is writable by node' >&2; exit 1; }

recorded_binary=$(docker run --rm --user node "$rtk_tag" sh -c "jq -r '.packages.tools[] | select(.id==\"rtk\") | .binary' /opt/session-profile/resolved.json") || exit 1
test "$recorded_binary" = rtk \
  || { echo "resolved.json did not record rtk's catalog binary name (got: $recorded_binary)" >&2; exit 1; }
recorded_catalog_sha=$(docker run --rm --user node "$rtk_tag" jq -r '.tool_catalog_sha256' /opt/session-profile/resolved.json) || exit 1
actual_catalog_sha=$(shasum -a 256 config/tool-catalog.json | awk '{print $1}') || exit 1
test "$recorded_catalog_sha" = "$actual_catalog_sha" \
  || { echo "resolved.json's tool_catalog_sha256 does not match config/tool-catalog.json" >&2; exit 1; }

# icm has a state_wrapper: install-selected.sh installs the real binary under
# /usr/local/libexec and a launcher under /usr/local/bin that requires
# /var/lib/agent-state before ever exec'ing the real binary -- except for
# `--version`, which the wrapper deliberately bypasses (see the heredoc in
# scripts/tools/install-selected.sh). Neither check below depends on icm's
# actual CLI beyond that documented wrapper contract.
icm_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/icm-with-shared-state.json | jq -er '.image') || exit 1

docker run --rm --user node "$icm_tag" test -f /usr/local/libexec/icm \
  || { echo 'icm real binary is missing from /usr/local/libexec' >&2; exit 1; }
docker run --rm --user node "$icm_tag" icm --version >/dev/null \
  || { echo 'icm --version (wrapper bypass) did not run' >&2; exit 1; }
if docker run --rm --user node "$icm_tag" icm >/dev/null 2>&1; then
  echo 'icm ran with no shared-state mount; expected it to fail safely' >&2
  exit 1
fi
docker run --rm --user node "$icm_tag" sh -c '! touch /usr/local/bin/.write-test 2>/dev/null' \
  || { echo '/usr/local/bin is writable by node (icm image)' >&2; exit 1; }
docker run --rm --user node "$icm_tag" sh -c '! touch /usr/local/libexec/.write-test 2>/dev/null' \
  || { echo '/usr/local/libexec is writable by node (icm image)' >&2; exit 1; }

recorded_shared_state=$(docker run --rm --user node "$icm_tag" jq -c '.shared_state' /opt/session-profile/resolved.json) || exit 1
test "$recorded_shared_state" = '{"id":"session-tools-verify","quota":"2G"}' \
  || { echo "resolved.json did not record the requested shared_state (got: $recorded_shared_state)" >&2; exit 1; }

# golang is a https-tar adapter whose archive_member is a whole directory (the
# go toolchain prefix). The adapter installs the directory under
# /usr/local/libexec/ai-sandboxes-tools/golang and symlinks its bin/ executables into /usr/local/bin;
# go must run as node and the full GOROOT tree must still be present for it to
# build (a lone go binary would only report --version).
golang_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/golang-only.json | jq -er '.image') || exit 1

docker run --rm --user node "$golang_tag" go version >/dev/null \
  || { echo 'curated tool golang does not run as node' >&2; exit 1; }
docker run --rm --user node "$golang_tag" sh -c 'test "$(go env GOROOT)" = /usr/local/libexec/ai-sandboxes-tools/golang' \
  || { echo 'golang GOROOT does not resolve to the installed toolchain prefix' >&2; exit 1; }
# NOTE: the GOROOT assertion above relies on Go's runtime relocating GOROOT by
# walking up from /proc/self/exe (via the /usr/local/bin/go symlink) to find
# its pkg/tool tree. If a future Go release changes that heuristic this check
# can fail even though the adapter is fine; treat a failure here as "verify the
# heuristic" before blaming the install path.
docker run --rm --user node "$golang_tag" sh -c 'test -x /usr/local/libexec/ai-sandboxes-tools/golang/pkg/tool/linux_arm64/compile' \
  || { echo 'golang toolchain is incomplete (missing pkg/tool/compile)' >&2; exit 1; }
# `go version` and the presence of pkg/tool/compile only prove the archive
# extracted; drive a real build so a broken toolchain (missing stdlib,
# broken linker, wrong GOROOT resolution) actually fails the test.
docker run --rm --user node "$golang_tag" sh -c '
  set -e
  home=$(mktemp -d)
  export HOME=$home GOCACHE=$home/cache GOPATH=$home/go
  cd "$home"
  cat > hello.go <<EOF
package main
import "fmt"
func main() { fmt.Println("ok") }
EOF
  go build -o hello hello.go
  ./hello | grep -qx ok
' || { echo 'golang cannot compile a hello-world program' >&2; exit 1; }
docker run --rm --user node "$golang_tag" sh -c '! touch /usr/local/bin/.write-test 2>/dev/null' \
  || { echo '/usr/local/bin is writable by node (golang image)' >&2; exit 1; }
docker run --rm --user node "$golang_tag" sh -c '! touch /usr/local/libexec/.write-test 2>/dev/null' \
  || { echo '/usr/local/libexec is writable by node (golang image)' >&2; exit 1; }

# awscli is an awscli-zip adapter: the archive carries its own installer which
# lays out /usr/local/aws-cli/v2/<version> and symlinks aws (and the completer)
# into /usr/local/bin. It must run as node.
awscli_tag=$(CLAUDE_MSB_BUILD_EGRESS=1 scripts/session/resolve-image.sh scripts/session/fixtures/valid/awscli-only.json | jq -er '.image') || exit 1

docker run --rm --user node "$awscli_tag" aws --version >/dev/null \
  || { echo 'curated tool awscli does not run as node' >&2; exit 1; }
docker run --rm --user node "$awscli_tag" test -f /usr/local/aws-cli/v2/current/dist/aws \
  || { echo 'awscli v2 prefix is missing' >&2; exit 1; }
docker run --rm --user node "$awscli_tag" test -L /usr/local/bin/aws \
  || { echo '/usr/local/bin/aws is not the installer symlink' >&2; exit 1; }
docker run --rm --user node "$awscli_tag" sh -c '! touch /usr/local/bin/.write-test 2>/dev/null' \
  || { echo '/usr/local/bin is writable by node (awscli image)' >&2; exit 1; }

echo ok
