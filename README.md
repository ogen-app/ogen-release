# ogen-release

The Ogen product release: which build of every service ships together, and the
workflows that deploy it. No service code lives here.

**How deploys work, step by step with diagrams:** [`documentation/`](documentation/README.md):
[deploying development](documentation/development.md) and
[deploying production](documentation/production.md).

- `release.yaml` pins `ghcr.io/ogen-app/<service>:<build>` for each service, in
  deploy order, with its Railway ids. An image that runs as several Railway
  services lists them all under `railwayServiceIds` (river-service: light, then
  heavy); one bump moves them all, and they deploy in that order.
- A push to `main` deploys **development**, which is also staging.
- Releases are numbered **Ogen 1, Ogen 2, …**, tagged `ogen-N`. `VERSION` holds
  the last released N (`0`: nothing released yet). A release deploys
  **production** after approval, with the same images. How to release, roll
  back and hotfix: [`RELEASING.md`](RELEASING.md).

## Flow

1. Service CI pushes `ghcr.io/ogen-app/<service>:<build>` (build = commit
   count on the default branch) and sends a `bump` dispatch here.
2. [`bump.yml`](.github/workflows/bump.yml) runs `cmd/bump`, which pins the new
   build and commits `fix(deps): <service> <old> → <new>` to `main` as the
   `ogen-release-bot` App. A build at or below the pinned one is ignored, so a
   late or replayed dispatch never downgrades. So is a service `release.yaml`
   doesn't list yet: its repo can publish before it is onboarded.
3. [`deploy-development.yml`](.github/workflows/deploy-development.yml) runs
   `cmd/deploy --env development`, then the smoke checks, and reports the
   result as the commit status `development-smoke`.
4. [`release.yml`](.github/workflows/release.yml) runs `cmd/release pr` on the
   same push. It rebuilds branch `release/next` from `main` with `VERSION` =
   N+1 and the release's notes in `CHANGELOG.md`, and keeps one **"Release Ogen
   N"** PR open listing every build that changed since `ogen-N`, with release
   notes: for each changed build, the CON issues in its commits (titles and
   Feature / Improvement / Bug labels from Linear) and the commits without one.
   With nothing changed, it closes the PR. When the PR merges, the same
   workflow publishes the release first: tag `ogen-N`, a GitHub Release with
   the PR's notes, and on each shipped Linear issue the label `Ogen N` and a
   comment linking the release.
5. [`release-gate.yml`](.github/workflows/release-gate.yml) sets
   `development-smoke` on PRs, since the `main` ruleset requires it: the
   Release PR gets the result of the `main` commit it is built on, and only
   while it is built on `main`'s head; any other PR passes, because it deploys
   to development after merge. So a release always ships the `release.yaml`
   development smoke-tested.
6. [`deploy-production.yml`](.github/workflows/deploy-production.yml) runs on
   the tag `ogen-N` that publishing pushes. It waits for an owner's approval
   in the GitHub Environment `production`, then runs `cmd/deploy --env
   production` with `release.yaml` as of the tag and the smoke checks against
   production. Running it by hand from an older tag rolls production back to
   that release.

## `cmd/deploy`

```sh
RAILWAY_TOKEN=<project token scoped to the environment> \
  go run ./cmd/deploy --env development [--ref <git ref>] [--dry-run]
```

Railway is the truth for what runs: a service is skipped only when its latest
deployment is healthy and runs the pinned image, so a deploy that never
happened or a manual rollback in Railway is caught on the next run. For every
other service, in file order, it sets `SENTRY_RELEASE=<service>@<build>`,
points the instance at the image (keeping replicas and region), deploys, and
waits for `SUCCESS`. A failed, crashed or timed-out deployment stops the chain.

The tool refuses a token scoped to a different environment than `--env`.

## Changing `release.yaml` by hand

Open a PR; only the release App pushes to `main` directly. A manual change is
how a single service is rolled back on development.

## Setup

| Where | Name | What |
| -- | -- | -- |
| Org variable | `RELEASE_APP_CLIENT_ID` | `ogen-release-bot` App client id |
| Org secret | `RELEASE_APP_PRIVATE_KEY` | its private key |
| Repo secret | `RAILWAY_TOKEN_DEVELOPMENT` | Railway project token scoped to development |
| Repo variable | `DEVELOPMENT_BASE_URL` | development origin the smoke test calls |
| Repo secrets | `SMOKE_EMAIL`, `SMOKE_PASSWORD` | dedicated smoke account on development |
| Repo secret | `LINEAR_API_KEY` | Linear key that reads issues, creates labels, labels issues and comments; release notes and marking shipped issues |
| Repo secret | `SMOKE_DEVICE_COOKIE` | the smoke account's `ogen_device` cookie, so CI signs in as a known device and no new-device email goes out each run |

To get `SMOKE_DEVICE_COOKIE`, run the smoke test once locally (the repo is
public; never print it in CI):

```sh
SMOKE_EMAIL=… SMOKE_PASSWORD=… go run ./cmd/smoke --base-url <dev origin> --print-device-cookie
```
