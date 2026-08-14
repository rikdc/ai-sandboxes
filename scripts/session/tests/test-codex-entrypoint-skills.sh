#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

fake_seed=$(mktemp -d) || exit 1
fake_home=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_home"' EXIT

# --- helpers ---
setup_seed() {
  mkdir -p "$fake_seed/$1" || exit 1
  printf '%s\n' "SKILL.md for $1" >"$fake_seed/$1/SKILL.md" || exit 1
}

run_entrypoint() {
  HOME="$fake_home" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh "$@"
}

# --- 1. Fresh home: symlinks created ---
setup_seed "skill-a"
setup_seed "skill-b"
run_entrypoint true || exit 1
test -L "$fake_home/.codex/skills/skill-a" || exit 1
test -L "$fake_home/.codex/skills/skill-b" || exit 1
test "$(readlink "$fake_home/.codex/skills/skill-a")" = "$fake_seed/skill-a" || exit 1
test -f "$fake_home/.codex/skills/skill-a/SKILL.md" || exit 1

# --- 2. Idempotency: second launch leaves symlinks unchanged ---
run_entrypoint true || exit 1
test -L "$fake_home/.codex/skills/skill-a" || exit 1
test "$(readlink "$fake_home/.codex/skills/skill-a")" = "$fake_seed/skill-a" || exit 1

# --- 3. Image update: new content in the seed resolves through the symlink ---
printf '%s\n' "updated content" >"$fake_seed/skill-a/SKILL.md"
test "$(cat "$fake_home/.codex/skills/skill-a/SKILL.md")" = "updated content" || exit 1

# --- 4. User-owned skill preserved: a regular directory alongside managed symlinks ---
mkdir -p "$fake_home/.codex/skills/my-skill" || exit 1
printf '%s\n' "user content" >"$fake_home/.codex/skills/my-skill/SKILL.md" || exit 1
run_entrypoint true || exit 1
test -d "$fake_home/.codex/skills/my-skill" || exit 1
test -f "$fake_home/.codex/skills/my-skill/SKILL.md" || exit 1

# --- 5. Collision (directory): user dir with managed name fails visibly ---
mkdir -p "$fake_home/.codex/skills/skill-a-user" || exit 1
rm -f "$fake_home/.codex/skills/skill-a"  # remove the managed symlink first
mkdir -p "$fake_home/.codex/skills/skill-a" || exit 1
# Actually let's use a fresh home for collision tests to avoid interference
fake_home2=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_home" "$fake_home2" "$fake_home3" "$fake_home4" "$fake_home5"' EXIT
setup_seed "skill-c"
mkdir -p "$fake_home2/.codex/skills/skill-c" || exit 1
if HOME="$fake_home2" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true 2>/dev/null; then
  echo 'FAIL: directory collision should abort entrypoint' >&2
  exit 1
fi

# --- 6. Collision (file): user file with managed name fails visibly ---
fake_home3=$(mktemp -d) || exit 1
mkdir -p "$fake_home3/.codex/skills" || exit 1
touch "$fake_home3/.codex/skills/skill-c" || exit 1
if HOME="$fake_home3" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true 2>/dev/null; then
  echo 'FAIL: file collision should abort entrypoint' >&2
  exit 1
fi

# --- 7. Symlink collision (wrong target): existing symlink to elsewhere fails visibly ---
fake_home4=$(mktemp -d) || exit 1
mkdir -p "$fake_home4/.codex/skills" || exit 1
ln -s /tmp/other "$fake_home4/.codex/skills/skill-c" || exit 1
if HOME="$fake_home4" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true 2>/dev/null; then
  echo 'FAIL: symlink collision should abort entrypoint' >&2
  exit 1
fi

# --- 8. Stale symlink cleanup: skill removed from seed, symlink removed ---
fake_home5=$(mktemp -d) || exit 1
setup_seed "skill-d"
setup_seed "skill-e"
HOME="$fake_home5" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true || exit 1
test -L "$fake_home5/.codex/skills/skill-d" || exit 1
test -L "$fake_home5/.codex/skills/skill-e" || exit 1
rm -rf "$fake_seed/skill-d"
HOME="$fake_home5" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true || exit 1
test ! -e "$fake_home5/.codex/skills/skill-d" || exit 1
test -L "$fake_home5/.codex/skills/skill-e" || exit 1

# --- 9. Missing seed dir: no-op, no skills_home created ---
empty_home=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_home" "$fake_home2" "$fake_home3" "$fake_home4" "$fake_home5" "$empty_home"' EXIT
if [ -e "$empty_home/.codex/skills" ]; then
  echo 'FAIL: missing seed should not create skills_home' >&2
  exit 1
fi
HOME="$empty_home" CODEX_SKILLS_SEED_DIR="/nonexistent" images/codex/entrypoint.sh true || exit 1
test ! -e "$empty_home/.codex/skills" || exit 1

# --- 10. Dangling symlink cleanup: a stale managed symlink is removed ---
fake_home6=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_home" "$fake_home2" "$fake_home3" "$fake_home4" "$fake_home5" "$empty_home" "$fake_home6"' EXIT
setup_seed "skill-f"
HOME="$fake_home6" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true || exit 1
test -L "$fake_home6/.codex/skills/skill-f" || exit 1
rm -rf "$fake_seed/skill-f"
HOME="$fake_home6" CODEX_SKILLS_SEED_DIR="$fake_seed" images/codex/entrypoint.sh true || exit 1
test ! -e "$fake_home6/.codex/skills/skill-f" || exit 1

echo ok
