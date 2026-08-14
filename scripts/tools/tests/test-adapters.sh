#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1
repo=$(pwd)

# These tests exercise the adapter and validation shell scripts hermetically:
# no network, no docker, no real vendor archives. They pair with the docker-
# based happy-path test in scripts/session/tests/test-session-tools.sh, which
# only runs when a real base image is available and therefore skips in most
# environments.
for cmd in jq tar zip unzip; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "skip: $cmd not available on this host" >&2; exit 0; }
done

work=$(mktemp -d) || exit 1
shim=$(mktemp -d) || exit 1
trap 'rm -rf "$work" "$shim"' EXIT

# Every dest/runtime/fixture directory lives beneath $work so cleanup is
# comprehensive; a scatter of mktemp calls outside $work slowly turns /tmp
# into an archaeological record.
# Each dest lives inside its own case directory so tests that care about
# the destination's sibling paths (awscli-zip derives install_dir from
# dirname(destination)) get a fresh sibling every time.
mk_dest() {
  local parent bin
  parent=$(mktemp -d "$work/case.XXXXXX") || exit 1
  bin=$parent/bin
  mkdir -p "$bin" || exit 1
  printf '%s' "$bin"
}

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "  ok: $*"; }

# A PATH shim for curl so the adapters can be driven with a locally-crafted
# archive without violating their https:// url check. The shim never touches
# the network -- it copies FIXTURE_ARCHIVE to whatever -o path the adapter
# passes.
cat >"$shim/curl" <<'EOF'
#!/usr/bin/env bash
out=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    -o*) out=${1#-o}; shift ;;
    -*) shift ;;
    *) shift ;;
  esac
done
test -n "$out" || { echo "shim curl: -o missing" >&2; exit 1; }
test -f "$FIXTURE_ARCHIVE" || { echo "shim curl: FIXTURE_ARCHIVE not set or missing" >&2; exit 1; }
cp "$FIXTURE_ARCHIVE" "$out"
EOF
chmod +x "$shim/curl"

# The adapters call sha256sum, which is Linux-standard but absent from a
# stock macOS install. Provide a shim that falls back to `shasum -a 256` so
# the suite actually runs on the primary macOS development host rather than
# silently skipping.
if ! command -v sha256sum >/dev/null 2>&1; then
  if command -v shasum >/dev/null 2>&1; then
    cat >"$shim/sha256sum" <<'EOF'
#!/usr/bin/env bash
# Just enough of sha256sum for the adapters: `sha256sum FILE` and the
# `echo "<sum>  <file>" | sha256sum -c -` verification form.
if [ "${1:-}" = "-c" ]; then
  read -r line || exit 1
  expected=${line%%  *}
  file=${line#*  }
  actual=$(shasum -a 256 -- "$file" | awk '{print $1}') || exit 1
  if [ "$expected" = "$actual" ]; then
    printf '%s: OK\n' "$file"
  else
    printf '%s: FAILED\n' "$file" >&2
    exit 1
  fi
else
  shasum -a 256 -- "$@"
fi
EOF
    chmod +x "$shim/sha256sum"
  else
    echo "skip: neither sha256sum nor shasum available on this host" >&2
    exit 0
  fi
fi

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$1" | awk '{print $1}'
  else
    shasum -a 256 -- "$1" | awk '{print $1}'
  fi
}

prefix_root="$work/prefix"
mkdir -p "$prefix_root" || exit 1

runtime_null="$work/runtime-null.json"
echo '{"shared_state":null}' >"$runtime_null"

run_https_tar() {
  local tool_id=$4
  # Every test starts from a clean prefix; the prefix-overwrite guard is
  # exercised by the destination-collision test on $destination, not by
  # leftover state between test cases.
  rm -rf "$prefix_root/$tool_id"
  FIXTURE_ARCHIVE=$1 PATH="$shim:$PATH" \
    AI_SANDBOXES_TOOLS_PREFIX_ROOT="$prefix_root" \
    bash "$repo/scripts/tools/install-https-tar.sh" \
    "$2" "$3" "$4" "$5"
}

run_awscli_zip() {
  FIXTURE_ARCHIVE=$1 PATH="$shim:$PATH" \
    bash "$repo/scripts/tools/install-awscli-zip.sh" \
    "$2" "$3" "$4" "$5"
}

run_github_release_tar() {
  FIXTURE_ARCHIVE=$1 PATH="$shim:$PATH" \
    bash "$repo/scripts/tools/install-github-release-tar.sh" \
    "$2" "$3" "$4" "$5"
}

# ----- path_is_absent helper: dangling symlink must count as present ---------
# This is the whole reason the collision guards were converted from
# `test ! -e` to path_is_absent(): the naive check succeeds for a dangling
# symlink and would let the adapter silently replace it.
(
  . "$repo/scripts/tools/lib.sh"
  probe="$work/probe-nothing"
  path_is_absent "$probe" || exit 1
  touch "$work/probe-real"
  ! path_is_absent "$work/probe-real" || exit 1
  ln -s "$work/does-not-exist" "$work/probe-dangling"
  ! path_is_absent "$work/probe-dangling" || exit 1
) || fail "path_is_absent misbehaves on a dangling symlink"
pass "path_is_absent treats a dangling symlink as present"

# ----- fixture: a valid "go"-shaped directory archive with two exposed bins --
mkdir -p "$work/fx/go/bin" "$work/fx/go/pkg/tool/linux_arm64"
cat >"$work/fx/go/bin/go" <<'EOF'
#!/bin/sh
echo go
EOF
cat >"$work/fx/go/bin/gofmt" <<'EOF'
#!/bin/sh
echo gofmt
EOF
chmod +x "$work/fx/go/bin/go" "$work/fx/go/bin/gofmt"
touch "$work/fx/go/pkg/tool/linux_arm64/compile"
chmod +x "$work/fx/go/pkg/tool/linux_arm64/compile"
tar -czf "$work/go.tar.gz" -C "$work/fx" go
go_sha=$(sha256_of "$work/go.tar.gz")

cat >"$work/catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {
      "id": "golang",
      "adapter": "https-tar",
      "url_template": "https://example.test/go-{{version}}.tar.gz",
      "archive_member": "go",
      "binary": "go",
      "expose": ["go", "gofmt"]
    }
  ]
}
EOF
cat >"$work/sel-good.json" <<EOF
{"tools":[{"id":"golang","version":"1.0.0","sha256":"$go_sha"}]}
EOF

# ----- https-tar: happy path installs both exposed symlinks and the prefix ---
dest=$(mk_dest)
run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-good.json" golang "$dest" \
  || fail "happy path failed"
test -L "$dest/go" || fail "go symlink missing"
test -L "$dest/gofmt" || fail "gofmt symlink missing"
test -x "$prefix_root/golang/bin/go" || fail "prefix bin/go missing"
pass "https-tar happy path installs prefix and exposed bins"

# ----- https-tar: checksum mismatch is rejected ------------------------------
cat >"$work/sel-bad-sha.json" <<'EOF'
{"tools":[{"id":"golang","version":"1.0.0","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}
EOF
dest=$(mk_dest)
if run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-bad-sha.json" golang "$dest" 2>"$work/err"; then
  fail "checksum mismatch was accepted"
fi
grep -q 'checksum mismatch' "$work/err" || fail "checksum error message missing"
pass "https-tar rejects checksum mismatch"

# ----- https-tar: existing destination file is refused -----------------------
dest=$(mk_dest)
touch "$dest/go"
if run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-good.json" golang "$dest" 2>"$work/err"; then
  fail "destination collision was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "collision error message missing"
pass "https-tar refuses existing destination (regular file)"

# ----- https-tar: dangling symlink at destination is also refused ------------
# This is the failure mode `test ! -e` alone would miss: the previous check
# would report "absent" for a symlink whose target does not exist, and the
# installer would silently replace it.
dest=$(mk_dest)
ln -s "$work/does-not-exist" "$dest/go"
if run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-good.json" golang "$dest" 2>"$work/err"; then
  fail "dangling-symlink destination was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "dangling-symlink error message missing"
pass "https-tar refuses existing destination (dangling symlink)"

# ----- https-tar: missing archive member is rejected -------------------------
mkdir -p "$work/fx-nogo/other"
touch "$work/fx-nogo/other/thing"
tar -czf "$work/nogo.tar.gz" -C "$work/fx-nogo" other
nogo_sha=$(sha256_of "$work/nogo.tar.gz")
cat >"$work/sel-nogo.json" <<EOF
{"tools":[{"id":"golang","version":"1.0.0","sha256":"$nogo_sha"}]}
EOF
dest=$(mk_dest)
if run_https_tar "$work/nogo.tar.gz" "$work/catalog.json" "$work/sel-nogo.json" golang "$dest" 2>"$work/err"; then
  fail "missing archive member was accepted"
fi
pass "https-tar rejects missing archive member"

# ----- https-tar: exposed binary missing from archive is rejected ------------
mkdir -p "$work/fx-nofmt/go/bin"
cat >"$work/fx-nofmt/go/bin/go" <<'EOF'
#!/bin/sh
EOF
chmod +x "$work/fx-nofmt/go/bin/go"
tar -czf "$work/nofmt.tar.gz" -C "$work/fx-nofmt" go
nofmt_sha=$(sha256_of "$work/nofmt.tar.gz")
cat >"$work/sel-nofmt.json" <<EOF
{"tools":[{"id":"golang","version":"1.0.0","sha256":"$nofmt_sha"}]}
EOF
dest=$(mk_dest)
if run_https_tar "$work/nofmt.tar.gz" "$work/catalog.json" "$work/sel-nofmt.json" golang "$dest" 2>"$work/err"; then
  fail "missing exposed binary was accepted"
fi
grep -q 'exposes gofmt' "$work/err" || fail "expose error message missing"
pass "https-tar rejects archive missing an exposed binary"

# ----- https-tar: unexpanded url template token is rejected ------------------
cat >"$work/badurl-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {
      "id": "golang",
      "adapter": "https-tar",
      "url_template": "https://example.test/go-{{unknown}}.tar.gz",
      "archive_member": "go",
      "binary": "go"
    }
  ]
}
EOF
dest=$(mk_dest)
if run_https_tar "$work/go.tar.gz" "$work/badurl-catalog.json" "$work/sel-good.json" golang "$dest" 2>"$work/err"; then
  fail "unexpanded template token was accepted"
fi
grep -q 'unexpanded template token' "$work/err" || fail "template token error message missing"
pass "https-tar rejects unexpanded template token"

# ----- github-release-tar: destination collision (regular file + dangling) ---
# The adapter downloads from github.com/<repo>/releases/download/... rather
# than a catalog url_template, so we still just point the curl shim at a
# canned archive; the fixture content is a single "hello" binary.
mkdir -p "$work/fx-gh"
printf '#!/bin/sh\necho hello\n' >"$work/fx-gh/hello"
chmod +x "$work/fx-gh/hello"
tar -czf "$work/hello.tar.gz" -C "$work/fx-gh" hello
gh_sha=$(sha256_of "$work/hello.tar.gz")
cat >"$work/gh-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {
      "id": "hello",
      "adapter": "github-release-tar",
      "repository": "o/r",
      "asset": "hello.tar.gz",
      "archive_member": "hello",
      "binary": "hello"
    }
  ]
}
EOF
cat >"$work/gh-sel.json" <<EOF
{"tools":[{"id":"hello","version":"v1","sha256":"$gh_sha"}]}
EOF

dest=$(mk_dest)
run_github_release_tar "$work/hello.tar.gz" "$work/gh-catalog.json" "$work/gh-sel.json" hello "$dest" \
  || fail "github-release-tar happy path failed"
test -x "$dest/hello" || fail "github-release-tar did not install hello"
pass "github-release-tar happy path installs the binary"

dest=$(mk_dest)
touch "$dest/hello"
if run_github_release_tar "$work/hello.tar.gz" "$work/gh-catalog.json" "$work/gh-sel.json" hello "$dest" 2>"$work/err"; then
  fail "github-release-tar destination collision was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "github-release-tar collision error message missing"
pass "github-release-tar refuses existing destination (regular file)"

dest=$(mk_dest)
ln -s "$work/does-not-exist" "$dest/hello"
if run_github_release_tar "$work/hello.tar.gz" "$work/gh-catalog.json" "$work/gh-sel.json" hello "$dest" 2>"$work/err"; then
  fail "github-release-tar dangling-symlink destination was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "github-release-tar dangling-symlink error message missing"
pass "github-release-tar refuses existing destination (dangling symlink)"

# ----- validate-selection: cross-tool binary collision -----------------------
cat >"$work/coll-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {"id":"a","adapter":"github-release-tar","repository":"o/r","asset":"a.tar.gz","archive_member":"tool","binary":"tool"},
    {"id":"b","adapter":"github-release-tar","repository":"o/s","asset":"b.tar.gz","archive_member":"tool","binary":"tool"}
  ]
}
EOF
cat >"$work/coll-sel.json" <<'EOF'
{"tools":[
  {"id":"a","version":"v1","sha256":"0000000000000000000000000000000000000000000000000000000000000000"},
  {"id":"b","version":"v1","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}
]}
EOF
if bash "$repo/scripts/tools/validate-selection.sh" "$work/coll-catalog.json" "$work/coll-sel.json" "$runtime_null" 2>"$work/err"; then
  fail "cross-tool binary collision was accepted"
fi
grep -q 'binary name collision' "$work/err" || fail "collision error message missing"
pass "validate-selection rejects cross-tool binary collision"

# ----- validate-selection: expose list missing the primary binary ------------
cat >"$work/badexpose-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {"id":"golang","adapter":"https-tar","url_template":"https://x.test/{{version}}.tar.gz","archive_member":"go","binary":"go","expose":["gofmt"]}
  ]
}
EOF
cat >"$work/dummy-sel.json" <<'EOF'
{"tools":[{"id":"golang","version":"1","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}
EOF
if bash "$repo/scripts/tools/validate-selection.sh" "$work/badexpose-catalog.json" "$work/dummy-sel.json" "$runtime_null" 2>"$work/err"; then
  fail "expose without primary binary was accepted"
fi
pass "validate-selection rejects expose list missing the primary binary"

# ----- validate-selection: duplicated expose entries -------------------------
cat >"$work/dupexpose-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {"id":"golang","adapter":"https-tar","url_template":"https://x.test/{{version}}.tar.gz","archive_member":"go","binary":"go","expose":["go","go"]}
  ]
}
EOF
if bash "$repo/scripts/tools/validate-selection.sh" "$work/dupexpose-catalog.json" "$work/dummy-sel.json" "$runtime_null" 2>"$work/err"; then
  fail "duplicated expose entries were accepted"
fi
pass "validate-selection rejects duplicated expose entries"

# ----- awscli-zip fixtures ---------------------------------------------------
cat >"$work/aws-catalog.json" <<'EOF'
{
  "schema_version": 1,
  "tools": [
    {"id":"awscli","adapter":"awscli-zip","url_template":"https://x.test/aws-{{version}}.zip","binary":"aws"}
  ]
}
EOF

mkdir -p "$work/awszip-fail/aws"
cat >"$work/awszip-fail/aws/install" <<'EOF'
#!/bin/sh
echo "bogus installer failing" >&2
exit 1
EOF
chmod +x "$work/awszip-fail/aws/install"
(cd "$work/awszip-fail" && zip -qr "$work/aws-fail.zip" aws)
aws_fail_sha=$(sha256_of "$work/aws-fail.zip")
cat >"$work/aws-sel-fail.json" <<EOF
{"tools":[{"id":"awscli","version":"1.0.0","sha256":"$aws_fail_sha"}]}
EOF

# A zip whose installer writes a $install_dir tree and a $bin_dir/aws
# symlink, so we can distinguish the "installer failure" and "collision"
# cases without ever running real awscli.
mkdir -p "$work/awszip-ok/aws"
cat >"$work/awszip-ok/aws/install" <<'EOF'
#!/bin/sh
set -e
while [ $# -gt 0 ]; do
  case "$1" in
    --install-dir) install_dir=$2; shift 2 ;;
    --bin-dir) bin_dir=$2; shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "$install_dir/v2/current/dist"
printf '#!/bin/sh\necho fake-aws\n' >"$install_dir/v2/current/dist/aws"
chmod +x "$install_dir/v2/current/dist/aws"
mkdir -p "$bin_dir"
ln -s "$install_dir/v2/current/dist/aws" "$bin_dir/aws"
EOF
chmod +x "$work/awszip-ok/aws/install"
(cd "$work/awszip-ok" && zip -qr "$work/aws-ok.zip" aws)
aws_ok_sha=$(sha256_of "$work/aws-ok.zip")
cat >"$work/aws-sel-ok.json" <<EOF
{"tools":[{"id":"awscli","version":"1.0.0","sha256":"$aws_ok_sha"}]}
EOF

# ----- awscli-zip: happy path installs binary + install-dir ------------------
dest=$(mk_dest)
run_awscli_zip "$work/aws-ok.zip" "$work/aws-catalog.json" "$work/aws-sel-ok.json" awscli "$dest" \
  || fail "awscli-zip happy path failed"
test -L "$dest/aws" || fail "awscli-zip did not install /bin/aws symlink"
test -x "$(dirname "$dest")/aws-cli/v2/current/dist/aws" \
  || fail "awscli-zip did not populate the install-dir"
pass "awscli-zip happy path installs binary and install-dir"

# ----- awscli-zip: vendor installer failure surfaces -------------------------
dest=$(mk_dest)
if run_awscli_zip "$work/aws-fail.zip" "$work/aws-catalog.json" "$work/aws-sel-fail.json" awscli "$dest" 2>"$work/err"; then
  fail "awscli installer failure was accepted"
fi
grep -q 'aws v2 installer failed' "$work/err" || fail "installer failure error message missing"
pass "awscli-zip surfaces vendor installer failure"

# ----- awscli-zip: missing aws/install --------------------------------------
mkdir -p "$work/awszip-noinstall/other"
touch "$work/awszip-noinstall/other/thing"
(cd "$work/awszip-noinstall" && zip -qr "$work/aws-noinstall.zip" other)
aws_ni_sha=$(sha256_of "$work/aws-noinstall.zip")
cat >"$work/aws-sel-noinstall.json" <<EOF
{"tools":[{"id":"awscli","version":"1.0.0","sha256":"$aws_ni_sha"}]}
EOF
dest=$(mk_dest)
if run_awscli_zip "$work/aws-noinstall.zip" "$work/aws-catalog.json" "$work/aws-sel-noinstall.json" awscli "$dest" 2>"$work/err"; then
  fail "missing aws/install was accepted"
fi
grep -q 'missing aws/install' "$work/err" || fail "missing aws/install error message missing"
pass "awscli-zip rejects archive missing aws/install"

# ----- awscli-zip: /bin/aws collision (regular file + dangling symlink) ------
dest=$(mk_dest)
touch "$dest/aws"
if run_awscli_zip "$work/aws-ok.zip" "$work/aws-catalog.json" "$work/aws-sel-ok.json" awscli "$dest" 2>"$work/err"; then
  fail "awscli destination collision was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "awscli destination collision error message missing"
pass "awscli-zip refuses existing destination (regular file)"

dest=$(mk_dest)
ln -s "$work/does-not-exist" "$dest/aws"
if run_awscli_zip "$work/aws-ok.zip" "$work/aws-catalog.json" "$work/aws-sel-ok.json" awscli "$dest" 2>"$work/err"; then
  fail "awscli dangling-symlink destination was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "awscli dangling-symlink error message missing"
pass "awscli-zip refuses existing destination (dangling symlink)"

# ----- awscli-zip: existing aws-cli install-dir is refused -------------------
# The adapter derives install_dir = $(dirname destination)/aws-cli, so
# populating that sibling directory ahead of time drives the guard.
dest=$(mk_dest)
mkdir -p "$(dirname "$dest")/aws-cli"
if run_awscli_zip "$work/aws-ok.zip" "$work/aws-catalog.json" "$work/aws-sel-ok.json" awscli "$dest" 2>"$work/err"; then
  fail "awscli existing install-dir was accepted"
fi
grep -q 'existing aws-cli prefix' "$work/err" || fail "awscli install-dir error message missing"
pass "awscli-zip refuses existing install-dir prefix"

echo ok
