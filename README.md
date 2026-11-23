# ogen-release

The Ogen product release: which build of every service ships together, and the
workflows that deploy it. No service code lives here.

- `release.yaml` pins `ghcr.io/ogen-app/<service>:<build>` for each service, in
  deploy order, with its Railway ids.
- A push to `main` deploys **development**, which is also staging.
- A tag `vN.0.0` (**Ogen N**) deploys **production** after approval, with the
  same images (phase 5, CON-520).

## Flow

1. Service CI pushes `ghcr.io/ogen-app/<service>:<build>` (build = commit
   count on the default branch) and sends a `bump` dispatch here.
2. [`bump.yml`](.github/workflows/bump.yml) runs `cmd/bump`, which pins the new
   build and commits `fix(deps): <service> <old> → <new>` to `main` as the
   `ogen-release-bot` App. A build at or below the pinned one is ignored, so a
   late or replayed dispatch never downgrades.
3. [`deploy-development.yml`](.github/workflows/deploy-development.yml) runs
   `cmd/deploy --env development`, then the smoke checks, and reports the
   result as the commit status `development-smoke`.

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
| Repo secret | `SMOKE_DEVICE_COOKIE` | the smoke account's `ogen_device` cookie, so CI signs in as a known device and no new-device email goes out each run |

To get `SMOKE_DEVICE_COOKIE`, run the smoke test once locally (the repo is
public; never print it in CI):

```sh
SMOKE_EMAIL=… SMOKE_PASSWORD=… go run ./cmd/smoke --base-url <dev origin> --print-device-cookie
```
