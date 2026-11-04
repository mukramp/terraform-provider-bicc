# Plan: hardening and distributing the BICC provider

Source: team | Last updated: 2026-11-03

This file owns the roadmap and the open provider tasks. It is the ONE place for task status; other files link here instead of copying status.
It was migrated from the former `plan.md` (untracked, removed in the same change).

Evidence labels used in this repo: `observed` (checked directly on the date given), `reported` (stated in the old plan or another artifact, not reproduced), `inferred`, `intended` (target, not yet true).

## Context

The provider (`bicc_job` resource, Terraform plugin framework; served protocol 6 by default (not confirmed by running the binary), the registry manifest declares 5.0, see Q12 in [DECISIONS.md](DECISIONS.md)) exists and is released up to v3.0.0. The remaining work is hardening it, distributing it, and migrating a consuming repo (`bicc-job-tf`, a sibling repo, see [CONSUMER-MIGRATION.md](CONSUMER-MIGRATION.md)) from the Python CLI (`bicc-job-cli`) to it.
Reference behaviour is the CLI. Mapping of CLI pieces to provider code (reported, from the old plan):

| CLI piece | Provider equivalent |
|---|---|
| `client.py`: PUT `/biacm/rest/meta/jobs/`, GET/DELETE `.../jobs/{id}`, GET `.../datastores/{key}`, basic auth, 300s timeout | `bicc/client/client.go`, `bicc/client/jobs.go` |
| `payload.py` (build_job, build_data_store, resolve_columns, date conversion) | `buildJobFromModel` and helpers in `bicc/resource_job.go` |
| `diff.py` (normalise_api, normalise_config, diff) | `buildDataStoresFromAPI`, `ModifyPlan`, plan modifiers |
| `cmd_apply`, `cmd_delete`, `cmd_plan` | Create/Update, Delete, Read |
| `tests/test_plan_columns.py` | Source of behavioural test cases (not yet ported) |
| `BICC_HOST`, `BICC_USERNAME`, `BICC_PASSWORD` | Provider config (env fallback: task 1) |

## Task status (single source of truth)

| # | Task | Status (2026-11-03) | Depends on |
|---|---|---|---|
| 1 | Env var credentials work | Implemented in the working tree (`bicc/provider.go`, `bicc/provider_test.go`, docs hunks); uncommitted and unreleased. Not verified end to end | none |
| 2 | Fix import | Not started | none |
| 3 | Handle job rename safely | Not started. Blocked on check A1 | task 5, A1 |
| 4 | Handle PUT response status | Not started. Blocked on check A2 | A2 |
| 5 | Make Read and Delete safe | Not started | A5, A6 recommended first |
| 6 | Stop swallowing column lookup errors | Not started | none |
| 7 | Add tests | Not started | writes tests for 2 to 6 |
| 8 | Clean up docs, examples, CI | Partial: README, `docs/index.md`, `docs/resources/job.md` corrected in the change that created this file. Open: `examples/main.tf`, CI, LICENSE, `main.go` version, `tfplugindocs` | none |
| 9 | Data source and lookup by name | Not started. Blocked on check A3 | A3 |
| 10 | Release v3.1.0 and distribution | Not started. Question Q2 is open (see [DECISIONS.md](DECISIONS.md)) | tasks 1 to 6 |

Tracker: no GitHub issues exist (`gh issue list --state all` returned nothing on 2026-11-03). Tasks 3, 4, 9 rest on unverified API behaviour ([ASSUMPTIONS.md](ASSUMPTIONS.md), group A), so no issues are created for them until those checks are done. Tasks 1, 2, 5, 6, 7, 8 do not depend on unverified behaviour and can become issues when the owner wants them tracked there.

## Current state of the provider (code at `3e25a02` plus the uncommitted task 1 edits; read on 2026-11-03)

### Implemented (observed unless noted)
- Go provider on `terraform-plugin-framework` v1.19.0; it is the only direct dependency (`go.mod`). No `terraform-plugin-testing`.
- Provider config: `host`, `username`, `password` (sensitive), optional `port` (default 443, `bicc/provider.go`). Fixed 300s HTTP timeout (`bicc/client/client.go:32`). No TLS options.
- One resource, `bicc_job`:
  - `id` computed from the PUT response with `UseStateForUnknown`; the job is re-read after create and update.
  - `name` (Required), `description`, `data_stores` as a Set of nested attributes keyed in code by `data_store_key`.
  - Per data store: `filters`, `initial_extract_date`, `chunk_type`, `chunk_*` ints, boolean flags, `auto_populate_all_columns`, `column_overrides` (list), `columns` (set).
  - `ModifyPlan` merges Optional+Computed fields from state to keep set hashes stable and pins `auto_populate_all_columns` to state for PVOs that always return explicit columns.
  - Auto-populate: fetches datastore columns at apply time, keeps `is_populate || is_primary_key`, applies `column_overrides`, drops columns that end up unpopulated. Columns are not stored in state in auto mode.
  - Dates: truncated to `YYYY-MM-DD` on read, sent as `YYYY-MM-DDT00:00:00.000Z` on write, with a plan modifier that ignores format-only differences.
  - Import by numeric job ID (but see KI-2).
- Release tooling: GoReleaser (multi-platform zips, SHA256SUMS, GPG detached signature), GitHub Actions release workflow on `v*` tags, registry manifest. Tags v1.0.0, v1.0.1, v2.0.0 to v2.0.4, v3.0.0.
- Tests: 17 test functions (client unit tests with `httptest`, provider config tests, a few helper tests). `go vet ./...` and `go test ./... -count=1` pass (observed 2026-11-03, Go 1.27.3 locally; `go.mod` says 1.25.5).

### Not implemented
- `bicc_datastore` data source.
- `bicc_job_backfill`: built, then removed in v3.0.0 (commit `262107e`). Out of scope, see DR-2 in [DECISIONS.md](DECISIONS.md).
- Any use of the list-jobs API, so no import-by-name.
- `RequiresReplace` on `name` (observed: no `RequiresReplace` anywhere in `bicc/`).
- Handling of the PUT response `status` (observed: `JobResponse.Status` is parsed at `bicc/client/jobs.go:78` and never read).
- `skip_delete` or any other protection against deleting BICC jobs. Deliberately not planned (DR-1).
- Plan-time resolution of auto-populate columns (resolved at apply time on purpose, see task 6).
- Acceptance tests (`TF_ACC`).

Known problems with evidence are in [KNOWN-ISSUES.md](KNOWN-ISSUES.md) (KI-n numbers below).

## Provider tasks

Before starting tasks 3, 4 and 9, run the checks in [ASSUMPTIONS.md](ASSUMPTIONS.md) group A: PUT behaviour on rename (A1), PUT `status` values (A2), whether the list API returns job IDs (A3).

### Task 1. Make env var credentials work
- **Original plan:** `host`, `username`, `password` with env fallbacks so that secret injection through env vars keeps working in the pipeline.
- **Was:** the three attributes were `Required`, so Terraform rejected configs that omitted them before `Configure` ran (KI-1).
- **Done (uncommitted):** the three attributes are `Optional` in `bicc/provider.go`; config-over-env precedence and the "Missing ..." errors are unchanged; `TestProviderSchema_CredentialsOptional` asserts the schema; `docs/index.md` and `README.md` hunks describe it.
- **Not done:** the test checks schema flags only; it does not run a config through Terraform's own validation. Not confirmed by a real `terraform plan` with an empty `provider "bicc" {}` block (ASSUMPTIONS B5), nor in the pipeline.
- **Gives you:** credentials only through env vars, so the password is not in `.tf` files or state. Required for the pipeline approach.

### Task 2. Fix import
- **Original plan:** import by job ID, then plan until clean per environment.
- **Current:** import yields empty `data_stores` (KI-2).
- **Change:** keep all API data stores when `oldByKey` is empty (import); keep the phantom-store filtering for normal Read; import with `auto_populate_all_columns = false` and explicit columns; add an import test.
- **Gives you:** real state after import, so a per-environment import step is reviewable instead of a full rewrite on first apply.

### Task 3. Handle job rename safely
- **Original plan:** `RequiresReplace` on `name`, because PUT upserts by name.
- **Current:** plain Required string; state keeps the config name (KI-3).
- **Change:** run check A1, add `RequiresReplace`, make Read write the API name into state.
- **Gives you:** renames appear as replacements in the plan; state cannot claim a name BICC does not have. A replacement is delete plus create and, reportedly, deletes the job's last-extract-date state, which means a full re-extract. With real deletion everywhere (DR-1) that is a real consequence of every rename, so it must be visible in the plan. Depends on task 5.

### Task 4. Handle the PUT response status
- **Original plan:** `SUCCESS_WITH_WARNING` becomes a warning diagnostic; any other non-success status is an error.
- **Current:** status parsed, never checked (KI-4).
- **Change:** check status after create and update; warn and surface `message` on `SUCCESS_WITH_WARNING`; error otherwise; copy the exact rules from the CLI's `cmd_apply`; add `httptest` cases. Run check A2 first.
- **Gives you:** failures reported in a 200 body fail the apply; warnings are visible, as in the CLI today.

### Task 5. Make Read and Delete safe
- **Original plan:** decide what removing a job file does; suggested `prevent_destroy` or a provider-side `skip_delete`.
- **Decision:** real deletion in all environments, so no `skip_delete` in the first version (DR-1).
- **Current:** Read drops state on any error; Delete fails on 404 (KI-5).
- **Change:** typed not-found error in the client; remove from state only on a real 404 and return an error for other failures; Delete treats 404 as success; no retries until rate limit and concurrent behaviour are known (ASSUMPTIONS C2).
- **Gives you:** transient API failures no longer make Terraform plan to recreate or drop a job, and deletes are idempotent. Because deletes run in prd, this is the main safety work in the provider.

### Task 6. Stop swallowing column lookup errors
- **Original plan:** `auto_populate_all_columns` needs a live lookup, resolved in plan (`ModifyPlan`) or modelled as computed to avoid "inconsistent result after apply".
- **Current:** lookup at apply time, errors swallowed, key not URL-escaped (KI-6).
- **Change:** return the error and fail the apply; URL-escape the key; keep the lookup at apply time (a plan-time lookup would need live API access for every plan).
- **Gives you:** no job is silently sent with zero columns. This is the one failure that can damage a production job without any error.

### Task 7. Add tests
- **Original plan:** `httptest` unit tests ported from the CLI; acceptance tests (`TF_ACC`) against sit.
- **Current:** client tests exist; `ModifyPlan`, `buildJobFromModel`, `buildDataStoresFromAPI`, CRUD and import are untested; no acceptance tests.
- **Change:** port `tests/test_plan_columns.py` cases; add `ModifyPlan` tests (stable set hashes, auto-populate pin, post-import); add `terraform-plugin-testing` with an acceptance suite (create, import, update, rename, destroy), against a mock server first and sit second.
- **Gives you:** a safety net for tasks 2 to 6; the import bug would have been caught.
- **Run:** `go test ./... -count=1` (unit). Acceptance suite: `TF_ACC=1 go test ./bicc/...` once it exists; against sit only with owner approval (live system).

### Task 8. Clean up docs, examples and CI
- **Done in the change that created this file:** `README.md` (backfill removed, version pins, Go version, `chunk_type` value, anchor, limitations), `docs/index.md` (backfill mention, version pin), `docs/resources/job.md` (`columns` is a Set, unenforced "Min: 1" removed, import caveat).
- **Open:**
  - `examples/main.tf`: v1 block syntax (`data_stores { ... }`), invalid against the current schema; pins `~> 1.0`; commented-out `bicc_job_backfill` examples (about lines 1298 to 1366) and a `data_store_keys` output (line 1454) meant for backfill. Needs a builder.
  - CI (`.github/workflows/ci.yml`): runs build and test on PRs only. Add `go vet`, `golangci-lint`, run on pushes to `main`.
  - Add a `LICENSE` file (README says MIT; no file exists, observed).
  - Declare `version` in `main.go` (observed: `.goreleaser.yml` ldflags set `main.version`, `main.go` declares none).
  - Generate docs with `tfplugindocs`. Caution: its `generate` writes into `docs/`, and `validate` rejects unknown subdirectories there. The project docs in this directory are flat `.md` files for that reason. Check that `tfplugindocs validate` still passes with them, or move them (DR-7).
  - Re-verify every attribute description in `docs/resources/job.md` against the schema (only the items above were checked).
  - Outside this repo: `bicc-job-tf/README.md` still describes backfill (see CONSUMER-MIGRATION.md).
- **Gives you:** docs matching the schema, which the consumer repo will be written against.

### Task 9. Decisions: data source and lookup by name
- **Original plan:** `bicc_datastore` data source for column metadata; import by name if the list API returns IDs.
- **Current:** neither exists; no list endpoint in the client. The CLI reads `id` from the list response (`bicc-job-cli/bicc/client.py`, `list_jobs` and a loop reading `job.get("id")`; observed in code on 2026-11-03), but the live API is unchecked (A3). The old plan said a README claims the opposite; that claim was not found in this repo.
- **Change:** run A3. If the list API returns IDs, add a client list call and import-by-name. Skip the data source unless column metadata is needed outside `bicc_job`.
- **Gives you:** import-by-name removes the need to dig out IDs for the few jobs that have no scheduler entry in the consumer repo.

### Task 10. Release and distribution
- **Original plan:** release with GoReleaser and GPG; get the provider into the Terraform pipeline image through a private registry, a mirror, or GitHub Releases.
- **Current (observed 2026-11-03):** release workflow and config exist; the public registry already lists `tra-jkv/bicc` versions 1.0.0 to 3.0.0 and the GitHub repo is public. Nothing is set up in the consumer repo.
- **Change:** tag v3.1.0 after tasks 1 to 6 (owner-only, red lane: the tag publishes publicly); decide distribution (Q2).
- **Gives you:** a version the consumer repo can pin to.

## Rollout order

1. Run check group A on sit ([ASSUMPTIONS.md](ASSUMPTIONS.md)). Needs the owner or explicit approval: it writes to a live system.
2. Provider hardening: task 1 (finish), then 2, 5, 6 with tests (7), then 3 and 4, then 8. Tag v3.1.0 (10).
3. Distribution (Q2 in [DECISIONS.md](DECISIONS.md)).
4. Consumer repo: import and plan on sit until clean, run check groups B and C, then stg, uat, prd. Backfill and GBQ table deletion are not part of this rollout. Details: [CONSUMER-MIGRATION.md](CONSUMER-MIGRATION.md).
5. Switch the consumer's scheduler to the resource IDs and retire the job ID from its `scheduler.json`.
6. Retire the CLI pipelines.
