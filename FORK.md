# Fork status — `nicholasspencer/beads`

A **fork** of `gastownhall/beads` (`steveyegge/beads` redirects there). This file
tracks how we diverge from upstream so the deltas survive a rebase. Update it whenever
a carried patch is added, landed, or dropped.

## Remotes

| Remote   | URL                                        | Role                       |
|----------|--------------------------------------------|----------------------------|
| `origin` | `git@github.com:gastownhall/beads.git`     | upstream (source of truth) |
| `fork`   | `git@github.com:nicholasspencer/beads.git` | our fork (carries patches) |

## Carried patches

| Branch | Commit | What | Upstream | Status |
|--------|--------|------|----------|--------|
| `fix/bd-context-tolerate-non-git-scope` | `33bb04695` | `GetRepoContext` tolerates non-git scopes | none filed | fork-only; pushed to `fork` |

Based off `main @ 9a1c88b63` (the dolt 2.1.4 bump, `#4326`).

### Dropped / not carried

- **`--claim` from custom `active` statuses** (the `claim.go` widening for
  [issue #4164](https://github.com/gastownhall/beads/issues/4164)): authored, then
  **dropped 2026-06-10**. We chose the consumer-side fix instead — factoryskills' `fs
  forge`/`fs agent` now do a plain `bd update --status in_progress --assignee <actor>`
  rather than `bd update --claim` (which is hard-gated to `status=open`). That keeps bd
  vanilla — no divergence to maintain — see `factoryskills-ptzf`. #4164 remains a real
  upstream bug but we no longer depend on a local fix for it.

## Rebuild & install

Upstream needs CGO for the embedded Dolt backend; the `gms_pure_go` tag selects Go's
stdlib regex so ICU is **not** required (matches the `Makefile`).

```bash
cd ~/development/com.gastownhall/beads
git checkout fix/bd-context-tolerate-non-git-scope
GOTOOLCHAIN=auto go install -tags gms_pure_go \
  -ldflags="-X main.Build=$(git rev-parse --short HEAD)" ./cmd/bd
```

`go install` writes to `~/go/bin/bd`, which both `~/.local/bin/bd` (the PATH symlink)
and `gc bd` resolve to — one install updates the whole live toolchain. `make install`
instead writes a *separate copy* to `~/.local/bin/bd`; don't mix the two or a stale
shadow appears.

> ⚠️ The Homebrew `bd` (`/opt/homebrew/bin/bd` → Cellar) is a **different, unpatched**
> install. Keep it off the factory's PATH.

## Rebase status

- `main` is **behind `origin/main` by 17 commits** (as of 2026-06-10).
- The carried branch is based off the stale `main`; re-cherry-pick and rebuild when
  rebasing onto current upstream.
