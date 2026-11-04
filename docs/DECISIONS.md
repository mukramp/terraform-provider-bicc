# Decisions and open questions

Source: team | Last updated: 2026-11-03

Owns: consequential decisions (with status and supersession) and the questions only the owner can answer. This is a single log instead of one ADR file per decision because the repo is small; split into `docs/adr/` if it grows past about ten entries.

Status values: `Decided` (recorded in the old plan as resolved by the owner; the facts under it may still be unverified, listed per entry), `Proposed`, `Superseded by DR-n`. Nothing here was re-decided during the migration from `plan.md`; the owner should confirm that the recorded wording matches what they decided.

## Decisions

### DR-1. Removing a job deletes it in every environment
- Status: Decided (as recorded in the old plan). Confirmed by the owner 2026-11-03 (Q6).
- Removing a job from the consumer repo deletes the BICC job in all environments (sit, stg, uat, prd), including its last-extract-date state. There is no `skip_delete` in the first version. `prevent_destroy` is not used either (see [CONSUMER-MIGRATION.md](CONSUMER-MIGRATION.md)).
- Consequence: plan review is the only safeguard, so Delete must be correct (task 5 in [PLAN.md](PLAN.md)) and the first plans per environment must be read carefully. A rename is delete plus create (task 3).
- Assumptions underneath: DELETE works on the BICC API (A5, observed 2026-11-04 on the SIT test job); a delete removes the last-extract-date state (reported, not checked).

### DR-2. Backfill is not managed by Terraform
- Status: Decided (as recorded in the old plan). Confirmed by the owner 2026-11-03 (Q6). Matches commit `262107e` (v3.0.0): the BICC API does not support setting `lastExtractDate` through PUT (commit message, reported).
- Backfills are done via the BICC UI. `bicc_job_backfill` stays removed and no replacement is planned. Docs that suggest otherwise are tracked in [KNOWN-ISSUES.md](KNOWN-ISSUES.md) (KI-10).

### DR-3. Scope is `bicc_job` only
- Status: Decided (as recorded in the old plan). Confirmed by the owner 2026-11-03 (Q6).
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

### DR-8. SIT boundary: only the test job in `sit_testing/` may be touched
- Status: Owner statement 2026-11-03 ("it is imperative that no other jobs or datastores in SIT are touched during development or testing"). The operational rules below are the agent's proposal and wait for the owner's confirmation (see the open items in the 2026-11-03 session report).
- The only BICC objects development and testing may create, update or delete are the ones defined in `../sit_testing/main.tf`: job `SIT_TEST_JOB` with data stores `FscmTopModelAM.FinFunCurrenciesAM.CurrencyPVO` and `FscmTopModelAM.FinFunTerritoriesAM.CountryPVO` (as read from the file; the owner's "two data stores" is read to mean these two inside this one job).
- Proposed rules:
  1. Live runs go through `sit_testing` (`make plan|apply|destroy`) only. Run `make plan` first and read it; if it lists any resource other than `bicc_job.sit_test_job`, stop.
  2. PUT upserts by job name, so a wrong name overwrites a real job. Any test job name must start with `SIT_TEST_` and must be shown by a read-only list to not exist before it is created.
  3. Raw API calls (`curl`) that write, and every DELETE, use only a job ID taken from the `sit_testing` state or from a create response of a test job. Never an ID found by browsing the job list.
  4. Unit and plugin-framework tests use `httptest` mocks. Any acceptance test against sit refuses to run unless every job name it uses starts with `SIT_TEST_`.
  5. Import tests import `SIT_TEST_JOB` only.
- Not verified: which instance `.env` points to (agents cannot read it); that `SIT_TEST_JOB` does not collide with an existing real job.

## Open questions (owner-only, batched)

### Q2. How is the provider distributed to the Terraform pipeline? OPEN
Options from the old plan:
- A. Filesystem mirror baked into the Terraform pipeline image (`provider_installation` block, signed zips from the GitHub release). No extra infrastructure; every provider release needs an image rebuild and someone must own the image.
- B. Private registry (HCP/TFE or similar). Normal version pinning; needs a registry, a pipeline token and someone to run it.
- C. Public registry as `tra-jkv/bicc`. No pipeline changes. The old plan listed "the repo must be public (a security decision)" as the blocker. **New evidence (observed 2026-11-03): the repo is already public and the registry already serves versions 1.0.0 to 3.0.0 with about 1348 downloads of 3.0.0** (ASSUMPTIONS R1). The blocker no longer applies as stated; whether the public listing is intended is for the owner to confirm.
- Also unknown: which pipeline and image runs Terraform for the consumer repo and who owns that image.
- Update 2026-11-03: **Q2 stays open; the answer is under discussion.** Work continues in a fork, `mukramp/terraform-provider-bicc`, started from Tra Nguyen's upstream (`tra-jkv`, v3.0.0) as a base. The fork has no registry listing and no release secrets, and the registry address `tra-jkv/bicc` belongs to upstream. That affects option C: publishing from the fork would need a new registry namespace and a new source address (`main.go`, README, docs, consumer config), or an agreement with upstream. Nothing in the fork changes address, module path or release config until Q2 is decided.
- Recommendation: C only if the work goes back to upstream or the address question is settled; otherwise A (filesystem mirror from fork-built zips) avoids the address problem but needs the image owner. Confidence is low, because who owns the long-term repo is not decided. Default if unanswered: no distribution work starts; provider hardening continues in the fork.
- Reversible: yes until the consumer repo pins a source address.

### Q5. Terraform pipeline behaviour (full plan and apply of every job instead of changed files only, plan on PRs, worker reachability). OPEN
- Checked on sit as group C in [ASSUMPTIONS.md](ASSUMPTIONS.md); the questions C4 and C5 need an owner decision after C1 and C2.
- Update 2026-11-03: owner: **stays open until more assumptions are tested.** Not asked again before the group A and B checks have results.

### Q7. Commit and release of the pending task 1 change
- The task 1 edits (`bicc/provider.go`, `bicc/provider_test.go`, plus doc hunks) were made earlier and are uncommitted. This migration does not commit them without a statement from the owner that they are finished. See `AGENTS.md`, authority.
- Update 2026-11-03: owner: **the change still has to be verified through `../sit_testing`** (its Makefile loads `.env` for the Terraform commands) before it is committed. That is check B5 in [ASSUMPTIONS.md](ASSUMPTIONS.md). Release stays red lane and owner-only.
- Update 2026-11-04: B5 verified on SIT (create, no-diff plan, destroy; see ASSUMPTIONS B3, B5, A5). The change is committed on branch `feat/env-credentials` in the fork (`git log` has the commit). Not merged, not released.

### Q8. Should a successful column lookup that leaves zero populated columns fail the apply? OPEN (task 6)
- Today the provider would PUT such a data store with zero columns. Task 6 turns that into an error (recommended default, not owner-confirmed).
- Evidence: on SIT none of the 145 data store keys in use would hit the error (ASSUMPTIONS A11; SIT only; whether `column_overrides` that deselect everything occur is not covered). Nothing shows what BICC does with a job PUT with zero columns.
- Options: A. Fail the apply (recommended: it avoids sending a job with no columns, which can damage a production job without any error). B. Send the job as before.
- Default if unanswered: A, as implemented on the branch. Reversible: yes, one block in `buildJobFromModel`.

### Q9. KI-12: auto-populate column selection, change the code or the doc?
- The doc (`docs/resources/job.md`) says auto mode includes all available columns. The code keeps only `isPopulate || isPrimaryKey` columns, then drops columns that end up not populated (a primary key with `isPopulate=false` is dropped unless an override sets it back). An override with `is_populate=true` on a non-default column has no effect, unlike the CLI's `resolve_columns` (read in `bicc-job-cli/bicc/payload.py`, not run).
- Options: A. Change the doc to match the code (recommended, no behaviour change). B. Change the code to match the CLI. Needs live checks first (ORA-01792 with more than 1000 columns, B2).
- Default if unanswered: A. Reversible: yes. Pinned by a test, `TestBuildJob_PreExisting_PrimaryKeyWithIsPopulateFalse`, on the task 6 branch.

### Q10. Do jobs that the account cannot see return the same HTTP 400 `Invalid Job Id provided` as missing jobs?
- Task 5 treats that 400 as "job does not exist" and drops it from state (ASSUMPTIONS A6). If a job the account cannot see returns the same 400, a permission change would make Read drop those jobs and the next plan would recreate them.
- Cannot be checked safely from here (needs a second account or a job the SIT account cannot read). Options: A. Owner checks it or knows the answer before any prd use (recommended). B. Accept the risk for sit, check before stg.
- Default if unanswered: no prd use until answered.

### Q11. Raise the HTTP client timeout for PUT and DELETE?
- Observed deletes take about 150s and one create 28s against a fixed 300s timeout (ASSUMPTIONS A10). If the timeout fires after BICC deleted the job, the next run recovers cleanly with the task 5 predicate.
- Options: A. Leave it (default). B. Make the timeout configurable or raise it for PUT and DELETE only (separate yellow-lane change).
- Default if unanswered: A.

## Resolved

- Q1. Does a Go provider prototype exist? Yes: this provider.
- Q3. Should removing a job file delete the BICC job? Yes, see DR-1.
- Q4. Do you want `bicc_job_backfill`? No, see DR-2.
- Q6. Confirm the recorded decisions DR-1 to DR-3. Confirmed by the owner 2026-11-03.
