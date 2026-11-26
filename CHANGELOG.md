# Changelog

## Ogen 6

### Features

- [CON-437](https://linear.app/ogen/issue/CON-437/harbor-tenant-lifecycle-tab-on-tenants-progress-stages-health-stuck) Harbor: Tenant lifecycle tab on /tenants (progress stages, health, stuck tenants) · harbor

### Improvements

- [CON-355](https://linear.app/ogen/issue/CON-355/harbor-edit-per-post-type-canvas-sizes-in-the-platform-catalog) Harbor — edit per-post-type canvas sizes in the platform catalog · api, harbor
- [CON-475](https://linear.app/ogen/issue/CON-475/posts-list-text-search-text-in-the-posts-filter) Posts list: text search (text=) in the posts filter · api
- [CON-530](https://linear.app/ogen/issue/CON-530/move-ogen-code-review-from-coderabbit-to-greptile) Move ogen code review from CodeRabbit to Greptile · api, harbor

### Other

- [CON-531](https://linear.app/ogen/issue/CON-531/polar-billing-ogen-dev-plan-proto-adapter-core-plan-changes-admin) Polar billing — Ogen dev plan (proto, adapter + core, plan changes, admin + cutover, river-service, contract) · river-service, api
- [CON-532](https://linear.app/ogen/issue/CON-532/polar-billing-harbor-dev-plan-billingv1-renames-product-mapping) Polar billing — Harbor dev plan (billing.v1 renames, product mapping, cutover scope, Polar copy, P&L vendor + FX) · harbor
- [CON-534](https://linear.app/ogen/issue/CON-534/ui-text-search-on-the-posts-lists-and-the-calendar-text-in-the-posts) UI: text search on the posts lists and the calendar (text= in the posts filter) · ui
- [`3b59987`](https://github.com/ogen-app/harbor/commit/3b59987696373c4d3e424fba9f18b0af4cf1751f) make test-remote runs the Go suite on another Mac over SSH · harbor
- [`054c443`](https://github.com/ogen-app/harbor/commit/054c44348a0f871983c70348294f0a0da92e7529) address review: the remote dir is keyed by the checkout's full path and safe to pass unquoted · harbor

### Builds

| Service | Build |
| --- | --- |
| river-service | 37 → 40 |
| api | 1569 → 1583 |
| harbor | 242 → 253 |
| ui | 1001 → 1004 |

## Ogen 5

### Fixes

- [CON-527](https://linear.app/ogen/issue/CON-527/entitlement-counters-count-every-workspace-a-new-trial-workspace-is) Entitlement counters count every workspace: a new Trial workspace is told it has used all its campaigns · api
- [CON-528](https://linear.app/ogen/issue/CON-528/assistant-cant-draft-or-compile-ideas-for-a-named-platform-it-refuses) Assistant can't draft or compile ideas for a named platform: it refuses as "not one of the campaign's platforms" · api
- [CON-529](https://linear.app/ogen/issue/CON-529/assistant-cant-adapt-or-edit-a-post-selected-on-the-posts-list-the) Assistant can't adapt or edit a post selected on the Posts list: the post tools only work with the post open · api

### Other

- [`e500d74`](https://github.com/ogen-app/ui/commit/e500d742495bf3e2a3b41ffe986644a4db98e590) Brand cards: every entry a row with its own door, and the series run in two dialogs (#317) · ui

### Builds

| Service | Build |
| --- | --- |
| api | 1565 → 1569 |
| ui | 1000 → 1001 |

## Ogen 4

### Fixes

- [CON-525](https://linear.app/ogen/issue/CON-525/ui-setup-after-signup-on-the-real-api-a-read-per-source-setup-state) UI: setup after signup on the real API — a read per source, setup state from the workspace, site name and favicon, Zernio back to setup, positioning · ui
- [CON-526](https://linear.app/ogen/issue/CON-526/zernio-sync-worker-never-sweeps-connected-accounts-never-reach-ogen) Zernio sync worker never sweeps: connected accounts never reach Ogen · api

### Other

- [`1802352`](https://github.com/ogen-app/ui/commit/1802352f98a58cf92e56d9f8ecca47d7545827b4) Every button in sentence case, and one small heading (#309) · ui
- [`9865409`](https://github.com/ogen-app/ui/commit/9865409c40ac9772744edd6fc4d4aa1cc4980d61) Cards: the last misfits on ui/card, black content, and a clearer campaign Strategy (#315) · ui
- [`c2f7720`](https://github.com/ogen-app/ui/commit/c2f7720bba18cbf86cdbe95d3aead4a60485afc1) Workspace Settings: plan beside the cards, no time zone row, and Polar for Lemon Squeezy (#316) · ui

### Builds

| Service | Build |
| --- | --- |
| api | 1564 → 1565 |
| ui | 996 → 1000 |

## Ogen 3

### Fixes

- [CON-498](https://linear.app/ogen/issue/CON-498/assistant-a-campaigns-analytics-tab-sends-no-window-so-the-assistant) Assistant: a campaign's Analytics tab sends no window, so the assistant can't tell what it is looking at · api
- [CON-513](https://linear.app/ogen/issue/CON-513/api-for-setup-after-signup-a-read-per-source-an-onboarding-state-on) API for setup after signup: a read per source, an onboarding state on the workspace, the site's name and favicon · api
- [CON-522](https://linear.app/ogen/issue/CON-522/ui-set-window-from-a-campaigns-analytics-tab-lands-on-the-tab-not-the) UI: set_window from a campaign's Analytics tab lands on the tab, not the workspace dashboard · ui
- [CON-524](https://linear.app/ogen/issue/CON-524/connecting-linkedin-fails-on-production-with-we-couldnt-reach-the) Connecting LinkedIn fails on production with "We couldn't reach the platform" · api

### Builds

| Service | Build |
| --- | --- |
| api | 1553 → 1564 |
| ui | 995 → 996 |

## Ogen 2

### Fixes

- [CON-521](https://linear.app/ogen/issue/CON-521/new-account-opens-on-a-blank-screen-get-apicampaigns-answers-null-and) New account opens on a blank screen: GET /api/campaigns answers null and the campaign switcher reads its length · api, ui

### Builds

| Service | Build |
| --- | --- |
| api | 1552 → 1553 |
| ui | 994 → 995 |

## Ogen 1

First release.

| Service | Build |
| --- | --- |
| pdf-service | 18 |
| document-service | 10 |
| image-service | 12 |
| audio-service | 15 |
| video-service | 25 |
| river-service | 37 |
| api | 1552 |
| harbor | 242 |
| ui | 994 |
