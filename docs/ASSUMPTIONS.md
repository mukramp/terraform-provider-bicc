# Assumption register and live checks

Source: team | Last updated: 2026-11-03

Owns: what is verified and what is not, and the procedure for the checks that would verify it. Results go in the Status and Evidence columns of this file, with the date.

Status values: `verified` (checked, evidence linked), `inferred` (derived from code or docs, not run), `unknown`.

## Gate

No live check below has been run. Until the rows that block a task are `verified`:
- tasks 3, 4 and 9 in [PLAN.md](PLAN.md) are not started and no tickets are created for them;
- statements about BICC API behaviour in docs are labelled `reported` or `unverified`.

## How to run the live checks (operate-and-observe)

- The checks write to a live BICC instance. Agents cannot read `.env` files and have no credentials, so the owner runs them or explicitly approves a delegated run. Use the sit instance only.
- Use throwaway jobs with an obvious name prefix (for example `ZZ_TEST_...`), never the real jobs.
- Local setup: the scratch directory `sit_testing/` (a sibling of this repo, its own git repo, local Terraform state, `dev_overrides` to the locally built provider, credentials from its `.env` via `BICC_*` env vars). Run `make plan` before `make apply`; `make destroy` cleans up. Raw API checks (group A) can use `curl` with basic auth against `/biacm/rest/meta/jobs/`.
- Record for each check: date, instance, request, HTTP status and body (redact credentials and host), conclusion. Errors show up in the terminal output of the run; there is no other monitoring.
- Trial first: for any check that applies something, run `terraform plan` first and read it; apply to one throwaway job before anything else.
- Undo: `make destroy` removes what the scratch state created. Anything created by raw API calls must be deleted by job ID at the end of the check.

## A. BICC API behaviour (blocks tasks 3, 4, 5, 9)

| ID | Question | Why it matters | Status | Evidence |
|---|---|---|---|---|
| A1 | PUT an existing job with a changed `name`: new job (new ID) or old job updated with the name ignored? | Task 3. The earlier plan assumed "new job", the code assumes "ignored". | unknown | none |
| A2 | Which `status` values does PUT return for success, success with warning, failure (for example an invalid column or data store key)? How does `cmd_apply` in the CLI treat each? | Task 4 | unknown | none |
| A3 | Does `GET` on the jobs list return job IDs? | Task 9 (import-by-name). The CLI code reads `id` from the list response (`bicc-job-cli/bicc/client.py`), which suggests yes. | observed: **no** | 2026-11-03, read-only `GET /biacm/rest/meta/jobs/` on the instance behind secret `bicc-host-sit`: HTTP 200, 37 jobs, none has an `id` key; keys are `dataStores`, `description`, `name`, `schedules`. Import-by-name through the list API is not possible; the CLI inference was wrong. |
| A4 | Does PUT tell you whether it created or updated? | Messages and plan accuracy | unknown | none |
| A5 | DELETE on an existing job, then DELETE again (404?), then GET after delete. | Task 5 typed-404 handling. A client comment says the API "may not support DELETE" (`bicc/client/jobs.go:207`); the CLI's `cmd_delete` uses it. DR-1 relies on deletion working. | observed: DELETE works; repeat DELETE returns 200 | 2026-11-04, SIT, test job `SIT_TEST_JOB` only: `terraform destroy` removed it (2m28s; job list went from 40 to 39 and the other 39 jobs were identical by content hash). A raw `DELETE` on the same, now missing id returned **HTTP 200 after 144s** (idempotent, no 404). `GET` afterwards returns 400 (A6). The client comment "may not support DELETE" is wrong. Deletes are slow (2 to 2.5 minutes), close to the 300s client timeout. |
| A10 | How long do create and delete take on SIT? | Client timeout (300s) and pipeline run time | observed | 2026-11-04: create of a 2-data-store job 28s (apply), delete 2m28s (destroy) and 144s for a repeat DELETE of a missing id. |
| A6 | GET on a missing job: status code and body. | Task 5: tell a real 404 from other errors | observed: **400, not 404** | 2026-11-04, SIT, `GET /biacm/rest/meta/jobs/{id}` for the just-deleted `SIT_TEST_JOB` (id from the sit_testing state): HTTP 400, body `{"code":"Bad Request","message":"Invalid Job Id provided"}`. A missing job is a 400 with this message. Task 5's predicate (404 only) does not match it; extend `isJobNotFound` (400 plus that message), keep other 400s as errors. Follow-up, same day, read-only GETs: any numeric id that does not exist (`1`, `-5`, the deleted id) returns the same 400 `Invalid Job Id provided`, so "deleted" and "never existed" cannot be told apart. A **non-numeric** id (`abc`) and an overflowing id (`99999999999999999999`) return a plain **HTTP 404 `Not Found`** (routing level, not a missing job), so a bare 404 must NOT be treated as "job gone". Unchecked: whether the same 400 is returned for an existing job the account cannot see (permissions). |
| A7 | Remove a data store from a job and PUT. Does the API re-add it? | Commit `54a364a` filters these; task 2 must keep the filter | inferred | commit message and code comment only |
| A9 | `GET /biacm/rest/meta/datastores/{key}` for a key that does not exist: status? Do the two keys in `sit_testing/main.tf` exist on the SIT instance? | Task 6 (what a failed lookup looks like); the test job in `sit_testing` | observed: **500, and the two test keys are probably invalid** | 2026-11-03, read-only GETs on the instance behind `bicc-host-sit`: `FscmTopModelAM.FinFunCurrenciesAM.CurrencyPVO` and `FscmTopModelAM.FinFunTerritoriesAM.CountryPVO` return HTTP 500; a nonsense key `FscmTopModelAM.Nope.DoesNotExistPVO` also returns 500 (so a missing key is a 500, not a 404); a key used by an existing job returns 200 with 95 columns. Neither test key is used by any of the 143 data store keys of the existing jobs. `make apply` then failed with HTTP 400 `JBO-26048: Constraint "C_JOB_DATA_STORE_REL_C_DA_FK1" is violated` (foreign key on the data store); the provider swallowed the lookup error (KI-6) and sent the job anyway. Nothing was created (37 jobs identical before and after). |
| A8 | Datastore key lookup with special characters: does URL-escaping matter? | Task 6 | unknown | none |

## B. Provider against a real instance (after tasks 1, 2 and 6)

| ID | Check | Status | Evidence |
|---|---|---|---|
| B1 | Import an existing sit job and run `terraform plan`. Record every difference. (Task 2) | unknown | none |
| B2 | Apply with `auto_populate_all_columns = true` on a PVO with more than 1000 columns. Does the ORA-00028 / ORA-01792 issue appear on apply? | unknown | none (the code comment in `bicc/resource_job.go` says default selection avoids ORA-01792; not re-tested) |
| B3 | Plan twice in a row with no changes; the second plan must be empty. | verified (2 data stores, auto-populate) | 2026-11-04, SIT, `SIT_TEST_JOB` (data stores `HcmTopModelAnalyticsGlobalAM.HcmProfileCoreAM.CountryPVO` and `...CurrencyPVO`): apply created it (id from the state), the next `terraform plan -detailed-exitcode` exited 0, "no differences". |
| B4 | Rename, then destroy, on a throwaway job. | partly verified | Destroy verified 2026-11-04 (see A5). Rename not run (task 3, A1). |
| B5 | `terraform plan` with an empty `provider "bicc" {}` block and credentials only in `BICC_*` env vars (task 1). Run via `sit_testing`, `make plan`. | verified end to end 2026-11-04 (create, no-diff plan, destroy; see B3, A5) | Earlier, 2026-11-03: `make init` and `make plan` with the empty block and `BICC_*` from env: no validation error, plan = 1 create (`bicc_job.sit_test_job`), 0 change, 0 destroy. Credentials were also loaded from Secret Manager into a temporary env file (deleted afterwards) and the plan was the same. **Apply not verified**: the first `make apply` failed with HTTP 403 `Blocked: PUT to non-whitelisted domain` (host confirmed by the owner as the SIT host; owner then whitelisted it). The second apply reached BICC and failed with HTTP 400 because the test job's data store keys are invalid (see A9). Both times a before/after list of all 37 jobs (name plus content hash) showed no change. |

## C. Terraform pipeline behaviour (Q5 in [DECISIONS.md](DECISIONS.md); see also [CONSUMER-MIGRATION.md](CONSUMER-MIGRATION.md))

| ID | Check | Status | Evidence |
|---|---|---|---|
| C1 | How long does a full plan take with all jobs of an environment, and how many API calls does it make? | unknown | none |
| C2 | Does a full plan or apply put noticeable load on the BICC API? Rate limits and concurrent PUT behaviour. | unknown | none |
| C3 | Can the pipeline workers that run Terraform reach each BICC host? | unknown | none |
| C4 | Today the consumer's CD pipeline applies only changed job files. Terraform plans and applies every job each time. Acceptable? | unknown | owner decision after C1, C2 |
| C5 | Should plan run on PRs, replacing the existing drift check? | unknown | owner decision |
| C6 | Do the credentials arrive as `BICC_*` env vars in the pipeline? Needs task 1. | unknown | none |
| C7 | Destroys: does a removed job file show as a destroy in the PR plan? | unknown | none |

## R. Repo facts checked while writing these docs

| ID | Fact | Status | Evidence |
|---|---|---|---|
| R1 | The GitHub repo is public; the registry lists `tra-jkv/bicc` versions 1.0.0, 1.0.1, 2.0.0 to 2.0.4, 3.0.0. | verified 2026-11-03 | `gh api repos/tra-jkv/terraform-provider-bicc` returned `private: false`; `curl https://registry.terraform.io/v1/providers/tra-jkv/bicc` returned the version list (1348 downloads reported for 3.0.0). |
| R2 | `go vet ./...` and `go test ./... -count=1` pass on the working tree. | verified 2026-11-03 | run locally, Go 1.27.3 |
| R3 | The provider has no `RequiresReplace` and never reads PUT `status`. | verified 2026-11-03 | `grep` over `bicc/` |
| R4 | The registry ignores or tolerates extra flat `.md` files in `docs/` next to `index.md` and `resources/`. | unknown | Registry documentation lists the expected files only (`docs/index.md`, `docs/resources/*.md`, `docs/guides/*.md`, and so on); it does not say what happens with others. Check with `tfplugindocs validate` and on the next release. |
