# Consumer migration: `bicc-job-tf` to this provider

Source: team (reported by the old plan, not re-checked unless marked) | Last updated: 2026-11-03

Owns: what has to change in the consuming repo `bicc-job-tf` (a sibling directory, a different repo) to use this provider. Provider-side tasks are in [PLAN.md](PLAN.md). Nothing here is `intended` to be done in this repo; the work happens in the consumer repo, under its own rules.

Status: not started. The consumer repo currently runs the Python CLI (`bicc-job-cli`) from two Cloud Build files.

## Planned changes in `bicc-job-tf` (intended)

- Keep the JSON job files at first. Use `for_each = fileset("jobs/*.json")` with `jsondecode(file(...))` mapped into `bicc_job`. This avoids converting about 60 files, and `validate_pvo_mapping.py` keeps working. The provider's snake_case attribute names match the JSON files, but check that the nested types (`data_stores` as a set, `columns` as a set) accept the decoded JSON shape. Unverified.
- `terraform.tf` is GENERATED. Add a separate `bicc.tf` with its own `required_providers { bicc = ... }` block. The source address must still resolve inside the pipeline image (Q2 in [DECISIONS.md](DECISIONS.md)).
- Remove the job ID from `scheduler.json`; keep `schedule`, `extract_type`, `external_storage` keyed by `job_key`. `scheduler.tf` then uses `bicc_job[each.key].id`.
- Import before the first apply, per environment, with `import {}` blocks so it runs in the pipeline. IDs come from `scheduler.json`; the 2 stg and 2 uat jobs without a schedule entry need IDs from the API, old CD logs, or import-by-name (task 9). Requires task 2. Even with task 2 fixed, expect a first plan with some differences; review it before apply.
- Removing a job file deletes the BICC job in all environments (DR-1). Do not use `prevent_destroy`: it fails the whole apply and forces a code edit every time a job is decommissioned. Removing a `scheduler.json` entry already destroys the Cloud Workflow today (`scheduler.tf:29`), so both are destroyed in the same plan.
- Pipeline behaviour (full plan and apply of every job instead of only changed files, plan on PRs, worker reachability) is open: ASSUMPTIONS group C.
- Remove `cloudbuild-ci.yaml`, `cloudbuild-cd.yaml` and the bicc-job-cli image once the standard Terraform plan/apply pipeline works. A real plan replaces the `|| true` drift check.
- Credentials: pass `BICC_*` as env vars (requires task 1). Reading `bicc-*-do-<env>` through `google_secret_manager_secret_version` would put the password in state, so avoid it.
- Make sure the Cloud Build workers can reach each BICC host. The current CLI step already does.

## Docs in the consumer repo that are wrong about backfill

Tracked in [KNOWN-ISSUES.md](KNOWN-ISSUES.md) (KI-10): `bicc-job-tf/README.md` lines 37, 59-60 and 387, re-checked by grep on 2026-11-03. Fixing them is a change in that repo.

## Order

See "Rollout order" in [PLAN.md](PLAN.md). Out of scope: backfill (DR-2), GBQ table deletion (DR-5).
