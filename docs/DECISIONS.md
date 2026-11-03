# Decisions and open questions

Source: team | Last updated: 2026-11-03

Owns: consequential decisions (with status and supersession) and the questions only the owner can answer. This is a single log instead of one ADR file per decision because the repo is small; split into `docs/adr/` if it grows past about ten entries.

Status values: `Decided` (recorded in the old plan as resolved by the owner; the facts under it may still be unverified, listed per entry), `Proposed`, `Superseded by DR-n`. Nothing here was re-decided during the migration from `plan.md`; the owner should confirm that the recorded wording matches what they decided.

## Decisions

### DR-1. Removing a job deletes it in every environment
- Status: Decided (as recorded in the old plan).
- Removing a job from the consumer repo deletes the BICC job in all environments (sit, stg, uat, prd), including its last-extract-date state. There is no `skip_delete` in the first version. `prevent_destroy` is not used either (see [CONSUMER-MIGRATION.md](CONSUMER-MIGRATION.md)).
- Consequence: plan review is the only safeguard, so Delete must be correct (task 5 in [PLAN.md](PLAN.md)) and the first plans per environment must be read carefully. A rename is delete plus create (task 3).
- Assumptions underneath: DELETE works on the BICC API (A5, unknown); a delete removes the last-extract-date state (reported, not checked).

### DR-2. Backfill is not managed by Terraform
- Status: Decided (as recorded in the old plan). Matches commit `262107e` (v3.0.0): the BICC API does not support setting `lastExtractDate` through PUT (commit message, reported).
- Backfills are done via the BICC UI. `bicc_job_backfill` stays removed and no replacement is planned. Docs that suggest otherwise are tracked in [KNOWN-ISSUES.md](KNOWN-ISSUES.md) (KI-10).

### DR-3. Scope is `bicc_job` only
- Status: Decided (as recorded in the old plan).
- No `bicc_datastore` data source unless column metadata is needed outside `bicc_job` (task 9).

### DR-4. No local access to the consumer's Terraform GCS backend
- Status: Constraint, reported by the old plan; not re-checked.
- The `gcs` backend of the consumer repo (`backend "gcs" {}` in its generated `terraform.tf`) is reachable only from CI/CD pipelines, not from a developer machine. Consequences:
  - `terraform init`, `plan` and `apply` cannot run locally in the consumer's environment directories.
  - Anything that needs the real backend (plans against real state, check group C, the first import and plan per environment) runs only through the pipeline. That makes distribution of the provider to the pipeline (Q2) a prerequisite for testing in the consumer repo.
  - Local testing is limited to `go test` here and to Terraform runs in a scratch directory with local state. `sit_testing/` (a sibling directory with its own git repo) is that setup: local state, `dev_overrides` to the locally built provider, credentials from its `.env` via `BICC_*` env vars; `make init|plan|apply|destroy` rebuild the provider and load `.env`. It targets the sit BICC instance and has not been run yet (reported by the old plan).

### DR-5. Out of scope for now: deleting the downstream GBQ table when a job is deleted
- Status: Deferred. Not designed or planned. Revisit after the job migration is stable.
- To establish then: who creates the tables today, one table per job or per data store, and that a job rename must not delete a table.

### DR-6. Job execution and scheduling stay outside the provider
- Status: Decided (design stated in `README.md`, "Design" section).
- Reason given there: BICC cannot combine a recurring schedule with an external OCI bucket destination (reported, from the project's own testing; not re-verified).

### DR-7. Project docs live in flat files in `docs/`
- Status: Agent decided (green lane, reversible), announced here.
- `docs/` is also the Terraform Registry's documentation directory (`index.md`, `resources/`, ...). The project docs (`PLAN.md`, `KNOWN-ISSUES.md`, `ASSUMPTIONS.md`, `DECISIONS.md`, `CONSUMER-MIGRATION.md`) sit next to `index.md` as flat files, as the request asked. Risk: the registry or `tfplugindocs validate` may reject or publish them (ASSUMPTIONS R4, unknown). Fallback if so: move them to `project/` or `docs/project/` excluded from validation, and update the links in `AGENTS.md` and `README.md`.

## Open questions (owner-only, batched)

### Q2. How is the provider distributed to the Terraform pipeline? OPEN
Options from the old plan:
- A. Filesystem mirror baked into the Terraform pipeline image (`provider_installation` block, signed zips from the GitHub release). No extra infrastructure; every provider release needs an image rebuild and someone must own the image.
- B. Private registry (HCP/TFE or similar). Normal version pinning; needs a registry, a pipeline token and someone to run it.
- C. Public registry as `tra-jkv/bicc`. No pipeline changes. The old plan listed "the repo must be public (a security decision)" as the blocker. **New evidence (observed 2026-11-03): the repo is already public and the registry already serves versions 1.0.0 to 3.0.0 with about 1348 downloads of 3.0.0** (ASSUMPTIONS R1). The blocker no longer applies as stated; whether the public listing is intended is for the owner to confirm.
- Also unknown: which pipeline and image runs Terraform for the consumer repo and who owns that image.
- Recommendation: C, because the registry listing already exists and needs no pipeline change; confirm that the public listing is intended and that nothing in the BICC job definitions is sensitive. Default if unanswered: no distribution work starts; provider hardening continues.
- Reversible: yes until the consumer repo pins a source address.

### Q5. Terraform pipeline behaviour (full plan and apply of every job instead of changed files only, plan on PRs, worker reachability). OPEN
- Checked on sit as group C in [ASSUMPTIONS.md](ASSUMPTIONS.md); the questions C4 and C5 need an owner decision after C1 and C2.

### Q6. Confirm the recorded decisions DR-1 to DR-3
- The migration copied them from the old plan without change. Confirm they are current, in particular DR-1, because it is a red-lane consequence (deletion in prd).
- Default if unanswered: they stay as recorded.

### Q7. Commit and release of the pending task 1 change
- The task 1 edits (`bicc/provider.go`, `bicc/provider_test.go`, plus doc hunks) were made earlier and are uncommitted. This migration does not commit them without a statement from the owner that they are finished. See `AGENTS.md`, authority.

## Resolved

- Q1. Does a Go provider prototype exist? Yes: this provider.
- Q3. Should removing a job file delete the BICC job? Yes, see DR-1.
- Q4. Do you want `bicc_job_backfill`? No, see DR-2.
