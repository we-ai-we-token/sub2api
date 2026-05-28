# CLAUDE.md

## Branch Management

This repository is a fork of the upstream `Wei-Shaw/sub2api` project.

Use this branch strategy:

- `main` tracks the upstream project only for comparison and should stay clean.
- `pre-release` is the integration branch for local changes and upstream release tag updates.
- `release` is the production branch and should only receive changes that have already been verified on `pre-release`.

Recommended workflow:

1. Fetch upstream release tags instead of merging upstream `main`.
2. Merge the selected upstream release tag into `pre-release`.
3. Merge local feature branches into `pre-release`.
4. Test and verify on `pre-release`.
5. Merge `pre-release` into `release` for production deployment.

Use the helper script for upstream release sync:

```bash
scripts/sync-upstream-release.sh
```

The script fetches `upstream` tags, suggests the latest tag, lets you choose another tag interactively, and merges the selected tag into `pre-release`. It stops there by design; verify `pre-release` before merging it into `release`.

If the `upstream` remote is missing, add it first:

```bash
git remote add upstream <upstream-repository-url>
```

Do not develop directly on `release`. Keep `main` clean so it remains easy to compare with upstream.
