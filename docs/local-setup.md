# Local setup

This page explains how the repository is installed and kept up to date on a
maintainer's machine. It reads as a walkthrough for setting up a fresh machine
and assumes fish, Docker, `msb` (Microsandbox), and `gh` are already installed.

## Layout

The repository is *installed* software, not a working clone: you `git pull` it,
never push from it (releases are driven from `main` via GitHub Actions), so it
belongs outside your scratch/development directory.

| Thing | Location |
| --- | --- |
| Repository checkout | `~/opt/ai-sandboxes` |
| Fish wrappers (copied, trust boundary) | `~/.config/fish/functions/{claude,codex,claude-session}.fish` |
| Trust-boundary guard (copied) | `~/.config/ai-sandboxes/trusted/guard.fish` |
| Session profiles (by name) | `~/.config/ai-sandboxes/profiles/<name>.json` |
| Session egress allowlist | `~/.config/microvms/claude-egress` |

The wrappers and guard are **copies**, not symlinks, and they embed the checkout
path at install time, so the checkout location is stable by design. If you ever
move it, re-run `scripts/install-fish-functions`.

## One-time setup

```console
git clone git@github.com:rikdc/ai-sandboxes.git ~/opt/ai-sandboxes
cd ~/opt/ai-sandboxes
./scripts/build          # build base/tools/claude/codex images
./scripts/verify         # verify the images
./scripts/load-msb       # load the images into Microsandbox
./scripts/install-fish-functions   # install the claude/codex/claude-session wrappers
```

`scripts/load-msb` is only meaningful if `msb` is installed. The wrappers also
assume fish; re-run `scripts/install-fish-functions` after any update that
changes `shell/**`.

Migrating an existing checkout already on disk is just `mv`, then running
`scripts/install-fish-functions` again to refresh the embedded path.

## Day to day

- `./scripts/build` — rebuild all images after changing `config/` or
  `versions.env`.
- `./scripts/verify` — the ARM64 verification the same code runs in CI.
- `./scripts/load-msb` — reload Microsandbox after a rebuild.

## Session profiles

Personal Claude-only software, marketplaces, apt/npm packages, and shared state
live in an explicit session profile, kept separate from the public runtime.
Store profiles in a personal repository (your dotfiles) and make them available
by name under `~/.config/ai-sandboxes/profiles/`:

```console
mkdir -p ~/.config/ai-sandboxes/profiles
ln -s ~/dotfiles/ai-sandboxes/profiles/work.json ~/.config/ai-sandboxes/profiles/work.json
claude-session --profile work
```

A bare profile name (no `/`) resolves to
`~/.config/ai-sandboxes/profiles/<name>.json` (docs/session-images.md). Start
from `config/session-profile.example.json` in the checkout. Profiles can contain shared-state and package selections, so keep them
private and never commit credentials.

## Updating

`scripts/update` is a brew-upgrade-style updater for the installed checkout. It
only manages the `main` branch and refuses to run on a dirty tree.

```console
~/opt/ai-sandboxes/scripts/update --check   # is there an update? read-only
~/opt/ai-sandboxes/scripts/update           # fast-forward and refresh
~/opt/ai-sandboxes/scripts/update --verify  # also run the full ./scripts/verify
```

Exit codes for `--check`: `0` up to date, `1` an update is available, `2` an
error (no upstream, not on `main`, dirty tree, diverged history, network
failure). `--check` only fetches; it never modifies the checkout.

The default update fast-forwards to `origin/main`, then, based on which files
actually changed, rebuilds the images, re-installs the fish wrappers (when
`shell/**` changed), reloads Microsandbox, and runs light version checks
(claude/codex `--version` against `versions.env`). A failed step prints the exact
next action and exits non-zero, so you can re-run `scripts/update` safely.

### Automation: tell you, don't do it for you

The updater is safe to run `--check` on a schedule, but the apply step (build +
verify + reload) should stay deliberate. Two options, in increasing automation:

- A fish prompt badge that calls `scripts/update --check` (cached for ~30
  minutes) and shows an indicator when you are behind.
- A `launchd` agent that runs `scripts/update --check` hourly and fires a macOS
  notification only when an update exists. Do **not** schedule the default mode.

A thin `update-agents` fish function in your dotfiles is the usual trigger:

```fish
function update-agents
    cd ~/opt/ai-sandboxes
    ./scripts/update
end
```

## Release markers

`versions.env` pins the agent runtime versions. When a version bump merges to
`main`, CI publishes an immutable GitHub Release
(`agent-versions-codex-<X>-claude-<Y>`) carrying the verified commit — a
record, not something a local image pulls from. Local images are always built
from your checkout via `./scripts/build`.