# Releasing Ogen

A release is **Ogen N**: one number for the whole product, tagged `ogen-N`, pinning the build of
every service in `release.yaml`. Production runs exactly what development smoke-tested; nothing is
rebuilt for it.

## Release

1. **Merge service changes as usual.** Each green merge publishes an image, bumps `release.yaml`
   on `main`, and deploys development.
2. **Review the "Release Ogen N" PR.** It stays open and updates itself on every bump. Its body
   is the release notes:
   - the issues each changed build brings, under Features, Improvements, Fixes and Other;
   - commits without a CON id;
   - the build table.
3. **Merge it** once `development-smoke` is green. The check reads development's result for the
   exact `main` commit the PR is built on, so it is green only once that commit has been deployed
   and smoke-tested, and only while the PR is up to date with `main`.
4. **Approve the production deploy.** Merging tags `ogen-N`, creates the GitHub Release "Ogen N",
   and labels and comments on the shipped Linear issues. The tag starts **Deploy production**,
   which waits in the `production` Environment until an owner approves it. It then deploys
   `release.yaml` as of the tag, one service at a time, and runs the smoke test against
   production.
5. **Check it:** the Deploy production run is green, and production serves the new builds
   (Railway, each service's latest deployment).

## Roll back

Roll the whole product back to an earlier release:

1. Actions → **Deploy production** → **Run workflow**.
2. In **Use workflow from**, choose the tag to go back to, e.g. `ogen-3`.
3. Approve the run. It deploys that release's `release.yaml`: every service whose running image
   differs goes back to the build that release pinned.

A rollback doesn't change `main` or the open Release PR, so the next release carries everything
forward again. Revert or fix whatever made you roll back before merging the next Release PR.

Schema migrations must be backward-compatible (expand, migrate, contract), so the previous api
build still runs on a newer schema. That's what makes rollback safe.

For one service in a hurry, Railway's own **Rollback** on that service's deployment is faster. The
next production deploy puts it back on the pinned build.

## Hotfix

A hotfix is the next release, rolled forward:

1. Fix it on the service's default branch. The new build bumps `release.yaml` and deploys
   development.
2. The Release PR now holds the fix, plus anything else merged since the last release.
3. Merge and approve as usual.

If `main` holds other unreleased bumps you don't want to ship yet, edit the Release PR's
`release.yaml` by hand, through a PR against `main`, so only the fixed service moves. Development
deploys exactly that, and the gate checks it, before the release.

## Setup

| Where | What |
| --- | --- |
| Environment `production` | Required reviewers: the owners. Deployment branches and tags: **Selected**, tag pattern `ogen-*` |
| Environment secret | `RAILWAY_TOKEN_PRODUCTION`: a Railway project token scoped to **production** |
| Environment secrets | `SMOKE_EMAIL`, `SMOKE_PASSWORD`, `SMOKE_DEVICE_COOKIE`: a smoke account on production |
| Environment variable | `PRODUCTION_BASE_URL`: the production origin the smoke test calls |
| Repo secret | `LINEAR_API_KEY`: release notes and marking shipped issues |
| `main` ruleset | Required check `development-smoke`, with "Require branches to be up to date" |

The Environment secrets take the same names as development's repo secrets and replace them inside
the production job, so the smoke test always signs in to the environment it tests.
