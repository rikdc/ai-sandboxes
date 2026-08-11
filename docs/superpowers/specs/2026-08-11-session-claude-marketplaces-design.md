# Session Claude marketplace/plugin overlay

## Purpose

Implement Implementation task 10 from `docs/session-images.md`: make a session
profile's `claude_marketplaces` field actually install and enable plugins in
the derived session image, instead of being rejected by validation. This is
the only package layer in scope; `apt`/`npm`/`python` (tasks 8/9) stay
rejected and out of scope.

## Non-goals

- `apt`, `npm`, or `python` session layers (tasks 8/9).
- Image GC or last-used metadata (task 11).
- `docs/private-profiles.md` removal or the broader documentation rollout
  (task 12).
- Any change to how the base image's own marketplaces
  (`config/marketplaces.json`) are installed.

## Data flow

```text
validate-profile.sh (accepts claude_marketplaces; still rejects apt/npm/python)
  -> resolve-image.sh (passes canonical profile JSON to the renderer)
  -> render-dockerfile.sh (emits a marketplace-install layer when non-empty)
  -> docker buildx build (multi-stage when marketplaces present)
  -> images/claude/entrypoint.sh (merges base + session seeds at launch)
```

## Component changes

### `scripts/session/validate-profile.sh`

Remove `claude_marketplaces` from the "reject if non-empty" check. Leave
`apt`, `npm`, and `python` in that rejection. The existing structural
validation for marketplace entries (public-GitHub URL, full 40-hex-char SHA,
safe path, plugin-name format, dedup) is already present and unchanged.

### `scripts/session/render-dockerfile.sh`

New signature: `render-dockerfile.sh CONTEXT_DIR BASE_IMAGE_REF CANONICAL_PROFILE_JSON`.

The third argument is the canonical profile JSON *string* (not a file path):
resolve-image.sh already holds this in its `$canonical` shell variable from
`validate-profile.sh`'s stdout, and passes it straight through as an
argument, matching how it's already used elsewhere in that script (e.g. `jq
--argjson request "$canonical"`). No extra file needed in the context dir for
this.

- If empty: emit today's single-stage Dockerfile unchanged.
- If non-empty:
  - Write `session-marketplaces.json` into the context dir, shaped exactly
    like `config/marketplaces.json` (`{"claude": [...profile entries...],
    "codex": []}`).
  - Copy `scripts/marketplaces/install-claude.sh` into the context dir
    verbatim. The renderer must not reference the checkout path directly in
    the Dockerfile — matches the existing "context dir contains only
    generated files and trusted installer scripts" invariant.
  - Emit a multi-stage Dockerfile mirroring `images/claude/Dockerfile`'s own
    build/final split:
    - `FROM $base_image_ref AS build`: set `HOME` to a throwaway build-only
      directory (never copied into the final stage) and
      `CLAUDE_CODE_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache`, run
      the copied installer against `session-marketplaces.json`, then extract
      the resulting `$HOME/.claude/settings.json` to
      `/opt/claude-session/plugin-seed/settings.json`, matching
      `images/claude/Dockerfile`'s own extraction step.
    - `FROM $base_image_ref` (final): copy only
      `/opt/claude-session/plugin-cache` and
      `/opt/claude-session/plugin-seed` from the build stage, root-owned and
      read-only (`chmod -R a-w`), matching the base image's existing
      immutability model. Set both
      `ENV CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR=/opt/claude-session/plugin-seed`
      and `CLAUDE_CODE_SESSION_PLUGIN_CACHE_DIR=/opt/claude-session/plugin-cache`
      — the design doc names both a "session seed dir" and a "matching
      session plugin-cache variable"; the entrypoint only needs the seed dir
      today (see Verification step), but the cache dir is set too for
      symmetry and in case runtime plugin-code discovery needs it. Then
      continue with the existing `COPY resolved.json` step and `USER node`.
  - The base image's own `CLAUDE_CODE_PLUGIN_CACHE_DIR` and
    `CLAUDE_CODE_PLUGIN_SEED_DIR` are untouched by any of this — they still
    point at `/opt/claude-plugin-cache` and `/opt/claude-plugin-seed`.

### `scripts/session/resolve-image.sh`

- Pass `$canonical` straight through as `render-dockerfile.sh`'s third
  argument (see above — no new file needed).
- Extend `renderer_hash` (used in the cache key) to hash both
  `render-dockerfile.sh` and `scripts/marketplaces/install-claude.sh`
  concatenated, not just the renderer. A change to the installer should also
  bust the session-image cache, for the same reason the renderer itself was
  added to the hash.
- No other changes: `canonical` (the profile) already flows into the cache
  key and into `resolved.json`'s `claude_marketplaces` field, both already
  present from the earlier vertical slice.

### `images/claude/entrypoint.sh`

Generalize the seed merge from a hardcoded `enabledPlugins`-only merge to a
recursive deep merge across up to three settings objects: the base seed
(`$CLAUDE_CODE_PLUGIN_SEED_DIR/settings.json`, always present), the session
seed (`$CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR/settings.json`, present only in
session images with marketplaces), and the user's persisted settings.

Precedence, highest to lowest: user's persisted settings, then session seed,
then base seed. Use jq's `*` operator (recursive merge, right side wins per
key, recursing into nested objects) to compute `(base * session) * current`.
This generalizes the current single-key merge to any object-shaped top-level
key without hardcoding `enabledPlugins` specifically — see Verification step
below for confirming this is the right shape for whatever keys Claude's
installer actually writes.

If `$CLAUDE_CODE_SESSION_PLUGIN_SEED_DIR` is unset or its `settings.json` is
missing, behave exactly as today (base seed only). Base images without any
session overlay are completely unaffected.

## Verification step (do this before finalizing the entrypoint merge)

Build the base image, run `scripts/marketplaces/install-claude.sh` once
against a throwaway marketplace, and inspect the resulting
`settings.json` directly (not just CLI output) to confirm:

1. Marketplace/plugin registration data is object-shaped (keyed by
   marketplace or plugin name), so `*` recursively merges it correctly — not
   array-shaped, which `*` does not merge element-wise.
2. There is no other required top-level key format the generalized merge
   would mishandle.
3. Whether registration entries store an absolute path to the marketplace's
   checkout (e.g. under `/opt/claude-plugin-cache/marketplaces/<name>`) or a
   path relative to `CLAUDE_CODE_PLUGIN_CACHE_DIR` resolved at read time. If
   absolute, runtime plugin-code discovery for the session's marketplace
   already works once its registration entry is merged into settings.json,
   regardless of which single cache dir `CLAUDE_CODE_PLUGIN_CACHE_DIR` names
   at runtime — the design above does not need to change. If relative, the
   base image's single `CLAUDE_CODE_PLUGIN_CACHE_DIR` at runtime cannot also
   resolve session-cache-relative paths, and this design needs an additional
   mechanism (e.g. Claude supporting multiple cache roots, or entrypoint.sh
   rewriting relative paths to absolute ones during the merge) before it is
   complete — treat that as a blocking finding, not something to route
   around silently.

If registration data turns out to be array-shaped, adjust the merge to
concatenate-and-dedupe those specific keys instead of relying on `*` alone.

## Testing

- New valid fixture: `scripts/session/fixtures/valid/claude-marketplaces.json`
  — only `claude_marketplaces` populated, structurally valid per the existing
  marketplace regex constraints.
- `scripts/session/tests/test-validate-profile.sh` picks this up
  automatically (it iterates `fixtures/valid/*.json` and
  `fixtures/invalid/*.json` generically).
- `scripts/session/tests/test-render-dockerfile.sh`: update for the new
  3-arg signature; add cases asserting the marketplace layer is absent for
  an empty-marketplaces profile and present (with the expected `FROM ... AS
  build` stage and env vars) for a non-empty one.
- New integration test (docker+msb gated, following the existing
  `test-resolve-image.sh`/`test-load-image.sh` pattern of skipping cleanly
  when those tools aren't available): build a session image from a profile
  with a test marketplace, launch it with a fresh home volume, and assert
  both the base image's own marketplace/plugin and the session's
  marketplace/plugin show as enabled — the acceptance criterion named
  explicitly in task 10.
- `scripts/verify`: wire the new test script in, matching the existing
  `bash scripts/session/tests/test-*.sh` list.

## Documentation

Update `docs/session-images.md`:

- Move task 10 out of the "not yet implemented" framing in the "Session
  profile schema" section — `claude_marketplaces` is no longer rejected.
- Confirm the "Claude marketplaces and plugins" section under "Package
  layers" still accurately describes the shipped behavior (it already
  describes this design closely; adjust only where implementation specifics
  differ, e.g. exact directory names).
