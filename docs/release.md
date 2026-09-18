# Packaging & Release Process

This document covers how `apl` is packaged and how a new version actually
reaches users, end to end: local build artifacts, the two-implementation
(Python POC / Go release) split, and the CI automation that turns a merged
PR into a published GitHub Release + Homebrew formula update.

## 1. What gets built

`apl` has two implementations (see [`plan.md`](../plan.md) §4), but only the
**Go build is distributed as a package** — the Python POC (`poc/apl/`) is
installed via `uv tool install --editable poc/` for local development only,
never released as a versioned artifact.

[GoReleaser](https://goreleaser.com/) (`.goreleaser.yaml`, repo root) builds
the Go binary (`cmd/apl`) for:

| OS | Arch | Archive |
|---|---|---|
| linux | amd64, arm64 | `.tar.gz`, plus `.deb`/`.rpm` (via [nfpm](https://nfpm.goreleaser.com/)) |
| darwin | amd64, arm64 | `.tar.gz` |
| windows | amd64, arm64 | `.zip` |

Every archive is named `apl_<version>_<os>_<arch>.<ext>`, and a
`checksums.txt` covering all of them is generated and attached to the
release. The version string is injected at build time via
`-ldflags "-X main.version={{.Version}}"` (see `cmd/apl/main.go`) — nothing
in the Go source tree stores the version.

GoReleaser also:
- Publishes a GitHub Release on this repo (`release.github`) with those
  assets attached.
- Regenerates the Homebrew formula (`brews:` block) and pushes it to the
  separate tap repo `cheoljoo/homebrew-apl`, which is what makes
  `brew install cheoljoo/apl/apl` / `brew upgrade apl` work.

The Python POC's version lives in `poc/pyproject.toml`'s
`[project].version` and is kept in sync automatically (§3) even though it
isn't packaged for distribution.

## 2. Versioning policy: what bumps the version

Version numbers are decided by [Conventional
Commits](https://www.conventionalcommits.org/) prefixes on **PR titles**,
not by hand:

| Prefix | Effect |
|---|---|
| `feat: ...` | minor bump, listed under "Features" in `CHANGELOG.md` |
| `fix: ...` | patch bump, listed under "Bug Fixes" |
| `perf: ...` | patch bump |
| `ci: ...`, `docs: ...`, `chore: ...`, `refactor: ...`, `test: ...` | no version bump, not shown in the changelog by default |

This repo's merge settings are **squash-merge only** (merge commit and
rebase merge are disabled at the repo level), with the squash commit's
title/message set to the PR's title/body. That's deliberate: individual
commits on a feature branch keep this org's usual
`[AGILEDEV-XXXX] feat: ...` Jira-ticket-prefixed convention, which breaks
conventional-commit type detection (the type has to be the very first
token). The squashed commit that actually lands on `main` uses the PR
title instead, so **the PR title is what must start with `feat:`/`fix:`/etc.**
for the release automation to classify it correctly.

## 3. The pipeline: PR merge → Release PR → tag → build → publish

```
 feat/fix PR merged (squash)
        │  push to main
        ▼
 release-please.yml  ──opens/updates──▶  "chore(main): release X.Y.Z" PR
        │                                (accumulates every feat/fix
        │                                 merged since the last release —
        │                                 nothing ships until this PR
        │                                 itself is merged)
        │  (someone merges the Release PR)
        ▼
 release-please tags vX.Y.Z + creates a bare GitHub Release
        │  push of tag vX.Y.Z
        ▼
 release.yml  ──▶  goreleaser release --clean
        │
        ├─▶ builds all OS/arch binaries + archives + .deb/.rpm
        ├─▶ attaches them to the GitHub Release release-please already
        │   created for that tag (GoReleaser appends artifacts to an
        │   existing release rather than making a second one)
        └─▶ pushes the regenerated formula to cheoljoo/homebrew-apl
```

Concretely:

1. **`.github/workflows/release-please.yml`** runs on every push to `main`.
   It reads `release-please-config.json` (`release-type: "simple"`, plus an
   `extra-files` entry that bumps `poc/pyproject.toml`'s
   `[project].version` to keep the POC's version string in sync) and
   `.release-please-manifest.json` (tracks the current released version).
   It either opens a new Release PR or, if one is already open, adds the
   newly-merged PR's changes to it — this is the "bundling": several
   feature PRs can land before anyone decides it's time to cut a release.
2. Merging that Release PR (squash, like any other PR here) is the **only**
   action that actually triggers a release. Normal feature/fix PR merges
   never publish anything by themselves.
3. When the Release PR merges, release-please creates the `vX.Y.Z` git tag
   and a GitHub Release containing just the changelog (no build assets
   yet).
4. **`.github/workflows/release.yml`** triggers on that tag push and runs
   `goreleaser release --clean`, which does the actual building and
   publishing described in §1.

## 4. Required secrets (one-time setup, already done for this repo)

Two fine-grained GitHub Personal Access Tokens are required, because two
different repos need to be pushed to from Actions, and Actions' default
`GITHUB_TOKEN` can't do either job (see §5.1 and §5.2 for why):

| Secret | Scope | Permissions | Used by |
|---|---|---|---|
| `RELEASE_PLEASE_TOKEN` | `ai-prompt-log` only | Contents: R/W, Pull requests: R/W | `release-please.yml`, so its tag push actually triggers `release.yml` |
| `HOMEBREW_TAP_GITHUB_TOKEN` | `cheoljoo/homebrew-apl` only | Contents: R/W | `.goreleaser.yaml`'s `brews[0].repository.token`, to push the formula update |

To (re)create one: <https://github.com/settings/tokens?type=beta> →
**Generate new token** → Resource owner `cheoljoo` → Repository access →
**Only select repositories** → pick the one repo it needs → Repository
permissions → set the permissions from the table above → generate → copy
the value → register it without ever pasting the raw value into chat:

```
gh secret set <SECRET_NAME> --repo cheoljoo/ai-prompt-log
```

Also required, one-time repo setting (Settings → Actions → General →
Workflow permissions): **"Allow GitHub Actions to create and approve pull
requests"** must be on, or release-please's very first run fails outright
(§5.3).

## 5. Known pitfalls (all hit and fixed during the first real release, v0.3.0)

### 5.1 A tag pushed with the default `GITHUB_TOKEN` doesn't trigger other workflows

GitHub Actions deliberately does not let events caused by the default
`GITHUB_TOKEN` trigger other workflow runs, to prevent infinite workflow
loops. `release-please-action`, if left on the default token, tags a
release successfully but `release.yml`'s `on: push: tags:` simply never
fires — the tag exists, nothing builds. Fixed by giving
`release-please-action` the `RELEASE_PLEASE_TOKEN` PAT instead (a real
user token, not subject to this restriction).

### 5.2 The Homebrew tap is a separate repo

`cheoljoo/homebrew-apl` isn't this repo, so the default `GITHUB_TOKEN`
(scoped to `ai-prompt-log`) can't push a formula update to it regardless of
permissions granted in the workflow YAML. `HOMEBREW_TAP_GITHUB_TOKEN` is a
separate PAT scoped only to that tap repo.

### 5.3 "GitHub Actions is not permitted to create or approve pull requests"

This repo, like most, defaults to *not* letting Actions open PRs
(`can_approve_pull_request_reviews: false` at the repo level). This gate is
separate from — and not overridden by — the `permissions:` block inside a
workflow YAML. release-please's first run failed with exactly this error
until the repo setting in §4 was flipped on.

### 5.4 Recovering a tag whose push didn't trigger a build

If a tag ends up created (by any of the above problems, or manually) but
`release.yml` never ran for it, there are two ways to recover, in order of
preference:

- **Re-push the tag as yourself** (not via Actions): `git push --delete
  origin vX.Y.Z && git push origin vX.Y.Z`. A push from a real user/PAT
  triggers `on: push: tags:` normally. This only makes sense if the tag's
  GitHub Release has no build assets yet (i.e. nothing shipped under that
  tag to begin with) — don't do this to a tag people may have already
  pulled artifacts from.
- **`workflow_dispatch`**: `release.yml` also has a manual
  `workflow_dispatch` trigger (`gh workflow run release.yml --ref main`)
  as a fallback. It only reflects the workflow file as it exists on the
  ref you dispatch against, and `actions/checkout` there defaults to that
  same ref — so this only produces a correct build if `main`'s current
  HEAD happens to still be exactly the tagged commit. Re-pushing the tag
  (previous bullet) is more reliable and was what actually fixed v0.3.0.

### 5.5 `goreleaser check` reports a `brews` deprecation

Pre-existing, unrelated to the CI setup: `goreleaser check` exits non-zero
because the `brews:` top-level key is deprecated upstream. This does not
affect `goreleaser release` (the command CI actually runs), which was
verified to work fine — the deprecation is a soft warning outside of
`check`. Left as-is; migrating to whatever GoReleaser eventually replaces
`brews:` with is a separate, non-urgent cleanup.

## 6. Cutting a release, step by step (what a human actually does)

1. Merge feature/fix PRs as normal, with titles starting `feat:`/`fix:`/etc.
   (§2). Nothing is released yet.
2. At any point, release-please's bot will have an open PR titled
   `chore(main): release X.Y.Z` reflecting everything mergeable so far.
   Watch it accumulate, or check anytime with:
   ```
   gh pr list --repo cheoljoo/ai-prompt-log --search "chore(main): release"
   ```
3. When ready to ship, review and squash-merge that Release PR like any
   other PR.
4. Everything after that (tag, build, GitHub Release assets, Homebrew tap
   update) is automatic — no further action needed. Confirm with:
   ```
   gh release view vX.Y.Z --repo cheoljoo/ai-prompt-log
   ```
5. Users upgrade with `brew update && brew upgrade apl` (`brew upgrade`
   alone won't see the new version — `brew update` is what refreshes the
   tap's local clone).
