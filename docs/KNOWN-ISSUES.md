# Known issues

Source: team | Last updated: 2026-11-03

Owns: defects and risks in the current code and docs, with evidence. Fix work is tracked in [PLAN.md](PLAN.md) (task numbers in brackets). Numbering is kept from the old plan so older references still work.

Evidence: "observed in code" = read at the stated file and line on 2026-11-03 at commit `3e25a02` (plus uncommitted task 1 edits). No runtime behaviour below was reproduced; nothing has been tested against a live BICC instance.

| ID | Issue | Evidence | Task |
|---|---|---|---|
| KI-1 | **Fixed in the working tree, uncommitted.** Env var fallback was dead: `host`, `username`, `password` were `Required`, so Terraform rejected configs that omitted them before `Configure` ran. Unit tests bypassed this by calling `Configure` with a null config. | `bicc/provider.go` diff; `TestProviderSchema_CredentialsOptional` passes. End-to-end not confirmed (ASSUMPTIONS B5). | 1 |
| KI-2 | **Import yields empty `data_stores`.** `ImportState` passes an empty `oldByKey` and `buildDataStoresFromAPI` drops every data store not in `oldByKey`. "Import then plan shows no changes" will not hold. | observed in code: `bicc/resource_job.go:442` and `:853-862`. No test. | 2 |
| KI-3 | **`name` has no `RequiresReplace`**, and Read/Update/Create keep the config name in state instead of the API name. What PUT does for a changed name is unknown: the earlier plan assumed it creates a new job, the code assumes the name is ignored. | observed in code: no `RequiresReplace` in `bicc/`; behaviour unverified (A1). | 3 |
| KI-4 | **PUT `status` is parsed but never checked.** Only the HTTP code (200/201) is checked. | observed in code: `bicc/client/jobs.go:78` (field), `:135` (code check); no reader of `.Status`. | 4 |
| KI-5 | **Read removes the resource on any `GetJob` error**, including 5xx and network errors. Delete errors on 404 (only 200/204 accepted). 404 is an untyped "job not found" error. No retries. | observed in code: `bicc/resource_job.go:315-319`, `bicc/client/jobs.go:162-164`, `:217`. | 5 |
| KI-6 | **Column lookup errors are swallowed**: a failed `GetDataStoreColumns` sends the job with zero columns. The datastore key is not URL-escaped. | observed in code: `bicc/resource_job.go:704-706`, `bicc/client/jobs.go:180`. (The old plan cited `jobs.go:261`; the file has 223 lines, `:180` is the real location.) | 6 |
| KI-7 | **Drift is deliberately hidden** for `is_silent_error`, `use_union_for_incremental`, `is_effective_date_disabled`, `chunk_type` and `auto_populate_all_columns` (state/plan wins over the API value). Known trade-off, not a bug: out-of-band changes to these fields do not show in plan. | reported (old plan); consistent with the `ModifyPlan` comments in `bicc/resource_job.go` (about lines 460-470). | none |
| KI-8 | **Data stores returned by the API but absent from config/state are dropped** (the API re-adds removed stores; commit `54a364a`). Must be kept for normal Read when fixing import. | observed in code: `bicc/resource_job.go:853-862`. API behaviour unverified (A7). | 2 |
| KI-9 | **Unparseable `initial_extract_date` is silently dropped** on write. | observed in code: `bicc/resource_job.go:790-793` (parse error ignored, field stays nil). | none yet |
| KI-10 | **Docs and examples were misleading.** See the breakdown below. | observed | 8 |
| KI-11 | **CI and release hygiene.** CI runs on PRs only, with build and test; no lint, vet, docs generation or `LICENSE` file. `.goreleaser.yml` ldflags set `main.version`, which `main.go` does not declare. | observed: `.github/workflows/ci.yml`, `.goreleaser.yml`, `main.go`, no `LICENSE*` in the repo root. | 8 |

## KI-10 breakdown

Backfill is done via the BICC UI and is not managed by Terraform (DR-2).

Fixed in the change that created this file:
- `README.md`: documented `bicc_job_backfill` (removed in v3.0.0); pinned `~> 2.0` and a `2.0.0` local install path; used `chunk_type = "DATE"` (the code value is `DateSeqIncr`); said Go >= 1.21 while `go.mod` needs 1.25.5; had a broken Design anchor; the Limitations section only mentioned scheduling.
- `docs/index.md`: said the provider covers "backfill management"; example pinned `~> 2.0`.
- `docs/resources/job.md`: called `columns` a List (it is a Set); claimed `data_stores` is "Min: 1" though no validator enforces it; said import drift is suppressed automatically (contradicted by KI-2).

Still open:
- `examples/main.tf`: v1 block syntax (`data_stores { ... }`), invalid against the current schema (`data_stores` is a nested attribute set); pins `~> 1.0`; commented-out `bicc_job_backfill` examples (about lines 1298 to 1366); `data_store_keys` output (line 1454) meant for backfill. `.tf` file, needs a builder.
- `bicc-job-tf/README.md` (sibling repo, not in this one): line 37 says `cloudbuild-cd.yaml` "Runs apply + backfill + config deploy"; lines 59-60 describe an ephemeral `backfills/<job_name>.json` directory; line 387 says backfill moves to a `bicc_job_backfill` Terraform resource. Reported by the old plan and re-checked by grep on 2026-11-03.
- Not misleading: `bicc-job-cli/README.md` has no backfill mention.
