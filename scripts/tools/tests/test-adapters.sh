#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1
repo=$(pwd)

# These tests exercise the adapter and validation shell scripts hermetically:
# no network, no docker, no real vendor archives. They pair with the docker-
# based happy-path test in scripts/session/tests/test-session-tools.sh, which
# only runs when a real base image is available and therefore skips in most
# environments.
for cmd in jq tar zip unzip sha256sum; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "skip: $cmd not available on this host" >&2; exit 0; }
done

work=$(mktemp -d) || exit 1
shim=$(mktemp -d) || exit 1
trap 'rm -rf "$work" "$shim"' EXIT

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

prefix_root="$work/prefix"
mkdir -p "$prefix_root" || exit 1

run_https_tar() {
  local tool_id=$4
  # Every test starts from a clean prefix; the adapter's "refuse to overwrite
  # existing prefix" guard is exercised by the destination-collision test on
  # $destination, not by leftover prefix state between test cases.
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

runtime_null=$(mktemp) || exit 1
echo '{"shared_state":null}' >"$runtime_null"

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
go_sha=$(sha256sum "$work/go.tar.gz" | awk '{print $1}')

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

# ----- test: happy path installs both exposed symlinks and the prefix --------
dest=$(mktemp -d)
run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-good.json" golang "$dest" \
  || fail "happy path failed"
test -L "$dest/go" || fail "go symlink missing"
test -L "$dest/gofmt" || fail "gofmt symlink missing"
test -x "$prefix_root/golang/bin/go" || fail "prefix bin/go missing"
pass "https-tar happy path installs prefix and exposed bins"

# ----- test: checksum mismatch is rejected -----------------------------------
cat >"$work/sel-bad-sha.json" <<'EOF'
{"tools":[{"id":"golang","version":"1.0.0","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}
EOF
dest=$(mktemp -d)
if run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-bad-sha.json" golang "$dest" 2>"$work/err"; then
  fail "checksum mismatch was accepted"
fi
grep -q 'checksum mismatch' "$work/err" || fail "checksum error message missing"
pass "https-tar rejects checksum mismatch"

# ----- test: destination collision is refused --------------------------------
dest=$(mktemp -d)
touch "$dest/go"
if run_https_tar "$work/go.tar.gz" "$work/catalog.json" "$work/sel-good.json" golang "$dest" 2>"$work/err"; then
  fail "destination collision was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "collision error message missing"
pass "https-tar refuses existing destination"

# ----- test: missing archive member is rejected ------------------------------
mkdir -p "$work/fx-nogo/other"
touch "$work/fx-nogo/other/thing"
tar -czf "$work/nogo.tar.gz" -C "$work/fx-nogo" other
nogo_sha=$(sha256sum "$work/nogo.tar.gz" | awk '{print $1}')
cat >"$work/sel-nogo.json" <<EOF
{"tools":[{"id":"golang","version":"1.0.0","sha256":"$nogo_sha"}]}
EOF
dest=$(mktemp -d)
if run_https_tar "$work/nogo.tar.gz" "$work/catalog.json" "$work/sel-nogo.json" golang "$dest" 2>"$work/err"; then
  fail "missing archive member was accepted"
fi
pass "https-tar rejects missing archive member"

# ----- test: exposed binary missing from archive is rejected -----------------
mkdir -p "$work/fx-nofmt/go/bin"
cat >"$work/fx-nofmt/go/bin/go" <<'EOF'
#!/bin/sh
EOF
chmod +x "$work/fx-nofmt/go/bin/go"
tar -czf "$work/nofmt.tar.gz" -C "$work/fx-nofmt" go
nofmt_sha=$(sha256sum "$work/nofmt.tar.gz" | awk '{print $1}')
cat >"$work/sel-nofmt.json" <<EOF
{"tools":[{"id":"golang","version":"1.0.0","sha256":"$nofmt_sha"}]}
EOF
dest=$(mktemp -d)
if run_https_tar "$work/nofmt.tar.gz" "$work/catalog.json" "$work/sel-nofmt.json" golang "$dest" 2>"$work/err"; then
  fail "missing exposed binary was accepted"
fi
grep -q 'exposes gofmt' "$work/err" || fail "expose error message missing"
pass "https-tar rejects archive missing an exposed binary"

# ----- test: unexpanded url template token is rejected -----------------------
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
# render-time check would fail this in validate-selection, but the adapter
# also has to refuse an unexpanded token on its own.
dest=$(mktemp -d)
if run_https_tar "$work/go.tar.gz" "$work/badurl-catalog.json" "$work/sel-good.json" golang "$dest" 2>"$work/err"; then
  fail "unexpanded template token was accepted"
fi
grep -q 'unexpanded template token' "$work/err" || fail "template token error message missing"
pass "https-tar rejects unexpanded template token"

# ----- test: validate-selection rejects cross-tool binary collision ----------
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

# ----- test: validate-selection rejects an expose list missing the binary ----
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

# ----- test: validate-selection rejects duplicated expose entries ------------
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

# ----- awscli-zip: installer failure and missing layout ----------------------
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
aws_fail_sha=$(sha256sum "$work/aws-fail.zip" | awk '{print $1}')
cat >"$work/aws-sel-fail.json" <<EOF
{"tools":[{"id":"awscli","version":"1.0.0","sha256":"$aws_fail_sha"}]}
EOF
dest=$(mktemp -d)
if run_awscli_zip "$work/aws-fail.zip" "$work/aws-catalog.json" "$work/aws-sel-fail.json" awscli "$dest" 2>"$work/err"; then
  fail "awscli installer failure was accepted"
fi
grep -q 'aws v2 installer failed' "$work/err" || fail "installer failure error message missing"
pass "awscli-zip surfaces vendor installer failure"

mkdir -p "$work/awszip-noinstall/other"
touch "$work/awszip-noinstall/other/thing"
(cd "$work/awszip-noinstall" && zip -qr "$work/aws-noinstall.zip" other)
aws_ni_sha=$(sha256sum "$work/aws-noinstall.zip" | awk '{print $1}')
cat >"$work/aws-sel-noinstall.json" <<EOF
{"tools":[{"id":"awscli","version":"1.0.0","sha256":"$aws_ni_sha"}]}
EOF
dest=$(mktemp -d)
if run_awscli_zip "$work/aws-noinstall.zip" "$work/aws-catalog.json" "$work/aws-sel-noinstall.json" awscli "$dest" 2>"$work/err"; then
  fail "missing aws/install was accepted"
fi
grep -q 'missing aws/install' "$work/err" || fail "missing aws/install error message missing"
pass "awscli-zip rejects archive missing aws/install"

# ----- awscli-zip: destination and install-dir collisions --------------------
dest=$(mktemp -d)
touch "$dest/aws"
if run_awscli_zip "$work/aws-fail.zip" "$work/aws-catalog.json" "$work/aws-sel-fail.json" awscli "$dest" 2>"$work/err"; then
  fail "awscli destination collision was accepted"
fi
grep -q 'refusing to install' "$work/err" || fail "awscli destination collision error message missing"
pass "awscli-zip refuses existing destination"

echo ok
