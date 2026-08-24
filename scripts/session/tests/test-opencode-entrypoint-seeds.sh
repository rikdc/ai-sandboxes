#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../../.." || exit 1

fake_seed=$(mktemp -d) || exit 1
fake_plugin_seed=$(mktemp -d) || exit 1
fake_home=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_plugin_seed" "$fake_home"' EXIT

# --- helpers ---
setup_skill() {
  mkdir -p "$fake_seed/$1" || exit 1
  printf '%s\n' "SKILL.md for $1" >"$fake_seed/$1/SKILL.md" || exit 1
}

setup_plugin() {
  printf '%s\n' "export const plugin = '$1'" >"$fake_plugin_seed/$1.ts" || exit 1
}

run_entrypoint() {
  HOME="$fake_home" OPENCODE_SKILLS_SEED_DIR="$fake_seed" OPENCODE_PLUGINS_SEED_DIR="$fake_plugin_seed" images/opencode/entrypoint.sh "$@"
}

# --- 1. Fresh home: skill and plugin symlinks created ---
setup_skill "skill-a"
setup_skill "skill-b"
setup_plugin "logger"
run_entrypoint true || exit 1
test -L "$fake_home/.config/opencode/skills/skill-a" || exit 1
test -L "$fake_home/.config/opencode/skills/skill-b" || exit 1
test "$(readlink "$fake_home/.config/opencode/skills/skill-a")" = "$fake_seed/skill-a" || exit 1
test -f "$fake_home/.config/opencode/skills/skill-a/SKILL.md" || exit 1
test -L "$fake_home/.config/opencode/plugins/logger.ts" || exit 1
test "$(readlink "$fake_home/.config/opencode/plugins/logger.ts")" = "$fake_plugin_seed/logger.ts" || exit 1

# --- 2. Idempotency: second launch leaves symlinks unchanged ---
run_entrypoint true || exit 1
test -L "$fake_home/.config/opencode/skills/skill-a" || exit 1
test "$(readlink "$fake_home/.config/opencode/skills/skill-a")" = "$fake_seed/skill-a" || exit 1

# --- 3. Image update: new content in the seed resolves through the symlink ---
printf '%s\n' "updated content" >"$fake_seed/skill-a/SKILL.md"
test "$(cat "$fake_home/.config/opencode/skills/skill-a/SKILL.md")" = "updated content" || exit 1

# --- 4. User-owned entry preserved: a regular directory alongside managed symlinks ---
mkdir -p "$fake_home/.config/opencode/skills/my-skill" || exit 1
printf '%s\n' "user content" >"$fake_home/.config/opencode/skills/my-skill/SKILL.md" || exit 1
run_entrypoint true || exit 1
test -d "$fake_home/.config/opencode/skills/my-skill" || exit 1
test -f "$fake_home/.config/opencode/skills/my-skill/SKILL.md" || exit 1

# --- 5. Collision (directory): user dir with managed name fails visibly ---
fake_home2=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_plugin_seed" "$fake_home" "$fake_home2" "$fake_home3" "$fake_home4" "$fake_home5"' EXIT
setup_skill "skill-c"
mkdir -p "$fake_home2/.config/opencode/skills/skill-c" || exit 1
if HOME="$fake_home2" OPENCODE_SKILLS_SEED_DIR="$fake_seed" images/opencode/entrypoint.sh true 2>/dev/null; then
  echo 'FAIL: directory collision should abort entrypoint' >&2
  exit 1
fi

# --- 6. Plugin collision (file): user file with managed name fails visibly ---
fake_home3=$(mktemp -d) || exit 1
mkdir -p "$fake_home3/.config/opencode/plugins" || exit 1
touch "$fake_home3/.config/opencode/plugins/logger.ts" || exit 1
if HOME="$fake_home3" OPENCODE_PLUGINS_SEED_DIR="$fake_plugin_seed" images/opencode/entrypoint.sh true 2>/dev/null; then
  echo 'FAIL: plugin collision should abort entrypoint' >&2
  exit 1
fi

# --- 7. Symlink collision (wrong target): existing symlink to elsewhere fails visibly ---
fake_home4=$(mktemp -d) || exit 1
mkdir -p "$fake_home4/.config/opencode/skills" || exit 1
ln -s /tmp/other "$fake_home4/.config/opencode/skills/skill-c" || exit 1
if HOME="$fake_home4" OPENCODE_SKILLS_SEED_DIR="$fake_seed" images/opencode/entrypoint.sh true 2>/dev/null; then
  echo 'FAIL: symlink collision should abort entrypoint' >&2
  exit 1
fi

# --- 8. Stale symlink cleanup: entry removed from seed, symlink removed ---
fake_home5=$(mktemp -d) || exit 1
setup_skill "skill-d"
setup_skill "skill-e"
HOME="$fake_home5" OPENCODE_SKILLS_SEED_DIR="$fake_seed" images/opencode/entrypoint.sh true || exit 1
test -L "$fake_home5/.config/opencode/skills/skill-d" || exit 1
test -L "$fake_home5/.config/opencode/skills/skill-e" || exit 1
rm -rf "$fake_seed/skill-d"
HOME="$fake_home5" OPENCODE_SKILLS_SEED_DIR="$fake_seed" images/opencode/entrypoint.sh true || exit 1
test ! -e "$fake_home5/.config/opencode/skills/skill-d" || exit 1
test -L "$fake_home5/.config/opencode/skills/skill-e" || exit 1

# --- 9. Missing seed dirs: no-op, no managed homes created ---
empty_home=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_plugin_seed" "$fake_home" "$fake_home2" "$fake_home3" "$fake_home4" "$fake_home5" "$empty_home"' EXIT
if [ -e "$empty_home/.config/opencode/skills" ] || [ -e "$empty_home/.config/opencode/plugins" ]; then
  echo 'FAIL: missing seeds should not create managed homes' >&2
  exit 1
fi
HOME="$empty_home" OPENCODE_SKILLS_SEED_DIR="/nonexistent" OPENCODE_PLUGINS_SEED_DIR="/nonexistent" images/opencode/entrypoint.sh true || exit 1
test ! -e "$empty_home/.config/opencode/skills" || exit 1
test ! -e "$empty_home/.config/opencode/plugins" || exit 1

# --- 10. Dangling symlink cleanup: a stale managed symlink is removed ---
# `test ! -e` is a false-positive trap here: -e returns false for a dangling
# symlink, so a surviving stale link would appear "removed". Assert ! -L to
# actually verify the link was unlinked.
fake_home6=$(mktemp -d) || exit 1
trap 'rm -rf "$fake_seed" "$fake_plugin_seed" "$fake_home" "$fake_home2" "$fake_home3" "$fake_home4" "$fake_home5" "$empty_home" "$fake_home6"' EXIT
setup_skill "skill-f"
HOME="$fake_home6" OPENCODE_SKILLS_SEED_DIR="$fake_seed" images/opencode/entrypoint.sh true || exit 1
test -L "$fake_home6/.config/opencode/skills/skill-f" || exit 1
rm -rf "$fake_seed/skill-f"
HOME="$fake_home6" OPENCODE_SKILLS_SEED_DIR="$fake_seed" images/opencode/entrypoint.sh true || exit 1
test ! -L "$fake_home6/.config/opencode/skills/skill-f" || exit 1
test ! -e "$fake_home6/.config/opencode/skills/skill-f" || exit 1

echo ok
