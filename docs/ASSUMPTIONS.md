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
| A3 | Does `GET` on the jobs list return job IDs? | Task 9 (import-by-name). The CLI code reads `id` from the list response (`bicc-job-cli/bicc/client.py`), which suggests yes. | inferred | CLI code read 2026-11-03; live API not called |
| A4 | Does PUT tell you whether it created or updated? | Messages and plan accuracy | unknown | none |
| A5 | DELETE on an existing job, then DELETE again (404?), then GET after delete. | Task 5 typed-404 handling. A client comment says the API "may not support DELETE" (`bicc/client/jobs.go:207`); the CLI's `cmd_delete` uses it. DR-1 relies on deletion working. | unknown | none |
| A6 | GET on a missing job: status code and body. | Task 5: tell a real 404 from other errors | unknown | none |
| A7 | Remove a data store from a job and PUT. Does the API re-add it? | Commit `54a364a` filters these; task 2 must keep the filter | inferred | commit message and code comment only |
| A8 | Datastore key lookup with special characters: does URL-escaping matter? | Task 6 | unknown | none |

## B. Provider against a real instance (after tasks 1, 2 and 6)

| ID | Check | Status | Evidence |
|---|---|---|---|
| B1 | Import an existing sit job and run `terraform plan`. Record every difference. (Task 2) | unknown | none |
| B2 | Apply with `auto_populate_all_columns = true` on a PVO with more than 1000 columns. Does the ORA-00028 / ORA-01792 issue appear on apply? | unknown | none (the code comment in `bicc/resource_job.go` says default selection avoids ORA-01792; not re-tested) |
| B3 | Plan twice in a row with no changes; the second plan must be empty. | unknown | none |
| B4 | Rename, then destroy, on a throwaway job. | unknown | none |
| B5 | `terraform plan` with an empty `provider "bicc" {}` block and credentials only in `BICC_*` env vars (task 1). Run via `sit_testing`, `make plan`. | unknown | none |

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
