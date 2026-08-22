#!/usr/bin/env bash
# Shared resolver for the user-owned ai-sandboxes configuration directory.
# Source this file; do not execute it. Every path that consumes marketplaces,
# tools, or runtime configuration resolves through here so there is exactly
# one definition of where user configuration lives.
#
# Resolution order for the configuration directory:
#   1. $AI_SANDBOX_CONFIG_DIR (explicit override, tests and advanced users)
#   2. $XDG_CONFIG_HOME/ai-sandboxes
#   3. $HOME/.config/ai-sandboxes
#
# The repository checkout is never a source of mutable local state: the
# checked-in config/*.json files are neutral defaults that seed missing user
# files and policy (tool-catalog.json) that stays repository-owned.

# Directory mode and file mode for everything this library creates.
readonly _AISB_CONFIG_DIR_MODE=0700
readonly _AISB_CONFIG_FILE_MODE=0600

# The three user-editable configuration files, in the fixed order used by
# ai_sandboxes_config_digest. Keep both lists in sync.
_AISB_CONFIG_FILES=(
  marketplaces.json
  tools.json
  runtime.json
)

_ai_sandboxes_die() {
  local caller=${_AISB_CALLER:-ai-sandboxes}
  printf '%s: %s\n' "$caller" "$*" >&2
  exit 1
}

# _ai_sandboxes_locate_config_dir CALLER
# Prints the resolved absolute configuration directory WITHOUT creating it:
# pure resolution plus validation. Read-only consumers (scripts/update
# --check) use this so a scheduled probe never mutates the host.
_ai_sandboxes_locate_config_dir() {
  local caller=$1 dir
  _AISB_CALLER=$caller
  if test -n "${AI_SANDBOX_CONFIG_DIR:-}"; then
    dir=$AI_SANDBOX_CONFIG_DIR
  else
    dir="${XDG_CONFIG_HOME:-$HOME/.config}/ai-sandboxes"
  fi
  if test -z "$dir"; then
    _ai_sandboxes_die "configuration directory resolved to an empty string (set AI_SANDBOX_CONFIG_DIR explicitly)"
  fi
  case $dir in
    /*) ;;
    *) _ai_sandboxes_die "configuration directory '$dir' must be an absolute path (set AI_SANDBOX_CONFIG_DIR to an absolute path)" ;;
  esac
  case $dir in
    *$'\n'*) _ai_sandboxes_die "configuration directory contains a newline: refusing to operate on it" ;;
  esac
  if test -e "$dir" && ! test -d "$dir"; then
    _ai_sandboxes_die "'$dir' exists but is not a directory"
  fi
  printf '%s\n' "$dir"
}

# ai_sandboxes_resolve_config_dir CALLER
# Prints the resolved absolute configuration directory, creating it with mode
# 0700 when missing. Fails on relative paths, newlines, or an unwritable
# location. The CALLER argument names the failing script in diagnostics.
ai_sandboxes_resolve_config_dir() {
  local caller=$1 dir
  dir=$(_ai_sandboxes_locate_config_dir "$caller") || return $?
  mkdir -p "$dir" || _ai_sandboxes_die "could not create configuration directory $dir"
  chmod "$_AISB_CONFIG_DIR_MODE" "$dir" 2>/dev/null \
    || true # Best effort: an existing directory owned by the user may already have the right mode.
  printf '%s\n' "$dir"
}

# ai_sandboxes_config_files_present CONFIG_DIR
# Succeeds when every expected user configuration file exists as a regular
# readable file; fails otherwise (callers treat that as unknown/drifted
# configuration state rather than initializing anything themselves).
ai_sandboxes_config_files_present() {
  local config_dir=$1 name dst
  for name in "${_AISB_CONFIG_FILES[@]}"; do
    dst="$config_dir/$name"
    if ! test -f "$dst" || ! test -r "$dst"; then
      return 1
    fi
  done
  return 0
}

# ai_sandboxes_init_config_files CALLER REPO_ROOT CONFIG_DIR
# Seeds every missing user configuration file from the checked-in neutral
# default (mode 0600) and fails clearly when an existing entry is not a
# regular readable file. Never overwrites existing user content.
ai_sandboxes_init_config_files() {
  local caller=$1 repo_root=$2 config_dir=$3 name src dst
  _AISB_CALLER=$caller
  for name in "${_AISB_CONFIG_FILES[@]}"; do
    dst="$config_dir/$name"
    if test -e "$dst" || test -L "$dst"; then
      if ! test -f "$dst" || ! test -r "$dst"; then
        _ai_sandboxes_die "existing configuration entry '$dst' is not a regular readable file; fix or remove it"
      fi
      continue
    fi
    src="$repo_root/config/$name"
    if ! test -f "$src"; then
      _ai_sandboxes_die "checked-in default '$src' is missing; cannot initialize user configuration"
    fi
    install -m "$_AISB_CONFIG_FILE_MODE" "$src" "$dst" \
      || _ai_sandboxes_die "could not initialize $dst from the checked-in default"
  done
}

# ai_sandboxes_validate_config_entries CALLER CONFIG_DIR
# Without initializing anything, verify that every expected entry is a regular
# readable file when present.
ai_sandboxes_validate_config_entries() {
  local caller=$1 config_dir=$2 name dst
  _AISB_CALLER=$caller
  for name in "${_AISB_CONFIG_FILES[@]}"; do
    dst="$config_dir/$name"
    test -e "$dst" || continue
    if ! test -f "$dst" || ! test -r "$dst"; then
      _ai_sandboxes_die "configuration entry '$dst' is not a regular readable file; fix or remove it"
    fi
  done
}

# _ai_sandboxes_sha256 FILE... — sha256 over stdin/files. Prefers coreutils'
# sha256sum (Linux) and falls back to Perl's shasum (macOS).
_ai_sandboxes_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

# ai_sandboxes_config_digest CONFIG_DIR
# Prints a deterministic sha256 over the three configuration files in fixed
# filename order. Each file's digest is hashed together with its filename so a
# change shifting bytes across a file boundary cannot produce a collision.
# Raw bytes are hashed: reformatting alone changes the digest, which only
# costs an unnecessary rebuild, never a wrong one.
ai_sandboxes_config_digest() {
  local config_dir=$1 name line
  local per_file=()
  for name in "${_AISB_CONFIG_FILES[@]}"; do
    if ! line=$(_ai_sandboxes_sha256 "$config_dir/$name"); then
      _ai_sandboxes_die "could not read $config_dir/$name while computing the configuration digest"
    fi
    per_file+=("${line%% *}|$name")
  done
  printf '%s\n' "${per_file[@]}" | _ai_sandboxes_sha256 | awk '{print $1}'
}
