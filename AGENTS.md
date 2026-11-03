# AGENTS.md — terraform-provider-bicc

Read this file first. Last updated: 2026-11-03. If anything here contradicts the code, the code wins; fix this file in the same PR.

## 1. What this repo is

A Terraform provider (Go, `terraform-plugin-framework`, protocol 5) that manages Oracle BICC (Business Intelligence Cloud Connector) extraction jobs through the BICC REST API. One resource, `bicc_job`. Module `github.com/tra-jkv/terraform-provider-bicc`, registry address `tra-jkv/bicc`, public GitHub repo. Artifacts: the provider binary, signed multi-platform release zips (GoReleaser, on `v*` tags), and Terraform Registry docs in `docs/`. Job execution and scheduling are deliberately not part of it (DR-6).

## 2. Why it exists

A Python CLI (`bicc-job-cli`, sibling directory) creates and diffs BICC jobs from about 60 JSON files in the sibling repo `bicc-job-tf`, run by Cloud Build. The goal is to replace that with this provider and a standard Terraform plan/apply pipeline, so job changes are reviewable as plans. The provider exists and is released (v3.0.0); the open work is hardening it, distributing it to the pipeline and migrating `bicc-job-tf`. Details: [docs/PLAN.md](docs/PLAN.md), [docs/CONSUMER-MIGRATION.md](docs/CONSUMER-MIGRATION.md).

## 3. Current status — pointer, not a copy

The single live source is the task table in [docs/PLAN.md](docs/PLAN.md). Do not copy per-task status into other files. There is no GitHub issue tracker usage yet (no issues exist).

What an agent must know now (refresh in the PR that changes it):
- Tasks that are ready (no unverified API assumption behind them): 2 (import), 5 (read/delete safety), 6 (column lookup errors), 7 (tests), parts of 8 (examples, CI, LICENSE, `main.go` version).
- Tasks 3, 4, 9 are blocked on live API checks A1 to A3 in [docs/ASSUMPTIONS.md](docs/ASSUMPTIONS.md). Nobody has run any live check yet.
- Open owner questions: [docs/DECISIONS.md](docs/DECISIONS.md) (Q2 distribution, Q5 pipeline behaviour, Q6 confirm decisions, Q7 task 1 commit).
- Task 1 (credentials from env vars) is written in `bicc/provider.go` and `bicc/provider_test.go`; check `git log` for whether it is merged before assuming either way. It is not in the v3.0.0 tag.

## 4. Authority and risk lanes

Facts about this repo: it is PR-based (7 merged PRs, branches `fix/…`, `docs/…`, `feat/…`, `remove/…`); `main` has no branch protection that the API reports (observed 2026-11-03, 404 on the protection endpoint); CI runs `go build` and `go test` on PRs only. Releases publish publicly through the registry and are GPG-signed with repository secrets.

| Lane | Examples here | Rule |
|---|---|---|
| Green | docs, tests, refactors inside existing contracts, fixes that do not change schema or plan/state behaviour, `go build/vet/test`, read-only `gh`/registry queries, local runs with no live BICC writes | Autonomous: commit and push to a feature branch, open a PR |
| Yellow | new or changed schema attributes, plan modifiers, state/Read/Import/Delete behaviour (task 2, 3, 5, 6), PUT/GET client behaviour, dependency upgrades, CI workflow changes | CI green plus independent review (`subagents/code-reviewer`) before merge; negative proofs for the behaviour change |
| Red | creating a tag or release (publishes publicly, signs), anything that calls a live BICC instance and writes (apply, PUT, DELETE, even on sit), anything touching prd, breaking schema changes (major version), GitHub secrets, repo visibility, force-push, deleting branches or tags, changing `.github/workflows/release.yml` | Owner approval first, stated explicitly in the request; documented rollback |

Commit authority: commit your own finished, verified changes by explicit path (never `git add .`, `-A` or `commit -a`), on a feature branch named for the work (`fix/…`, `docs/…`, `feat/…`; there are no ticket IDs yet), then push and open a PR. Never commit to `main`. Never commit `.env`, secrets, built binaries (`terraform-provider-bicc` in the root is git-ignored) or `dist/`. If a file you changed also has someone else's uncommitted edits, do not commit that file; report it.

Merge authority: **owner only** until the owner says otherwise (proposed default, Decision Inbox D1). Release and deploy are authorized separately and are owner-only (red lane).

### Decision rights

| Who decides | Examples | Behaviour |
|---|---|---|
| Owner only | Distribution choice (Q2), pipeline behaviour (Q5), anything red-lane, deletion semantics (DR-1), scope changes, whether the public registry listing is intended, when to cut a release | Ask, batched, with a recommendation and a default |
| Agent decides | Test structure, naming, doc layout, choice between equivalent implementations, read-only checks | Decide and announce in one line |
| Agent proposes, owner vetoes | Script/file layout, trial procedure, new doc files | State the default, proceed, record it (see DR-7 for an example) |

Decision Inbox: keep open owner decisions in [docs/DECISIONS.md](docs/DECISIONS.md), present them in ONE message (question, options, recommendation, default if unanswered, reversible?), and keep doing independent green work meanwhile. A finding that invalidates the plan is reported at once, as a full sweep, not a single question.

### Escalation triggers
Scope or semantic change; anything red-lane; a check result that contradicts a recorded decision or assumption; destructive or irreversible operations; needing credentials. Pause only the affected action, ask the narrow question, continue independent work.

## 5. Repo structure

```
AGENTS.md                      this file: orientation, authority, cold start
README.md                      user-facing: what it is, install, usage, limits, how to run tests
main.go                        provider entry point (registry address tra-jkv/bicc); declares no `version` var (see KI-11)
bicc/
  provider.go                  provider schema + Configure (host/username/password/port, env fallback)
  resource_job.go              bicc_job resource: schema, CRUD, ModifyPlan, import, payload building
  client/client.go             HTTP client (basic auth, 300s timeout)
  client/jobs.go               job + datastore endpoints
  *_test.go, client/*_test.go  unit tests (httptest for the client); no acceptance tests yet
docs/                          NOTE: also the Terraform Registry docs directory
  index.md                     registry page: provider
  resources/job.md             registry page: bicc_job
  PLAN.md                      ONE roadmap + task status (source of truth)
  KNOWN-ISSUES.md              defects with evidence (KI-n)
  ASSUMPTIONS.md               assumption register + live check procedure (A/B/C/R)
  DECISIONS.md                 decisions (DR-n) and open owner questions (Qn)
  CONSUMER-MIGRATION.md        what changes in the sibling repo bicc-job-tf
examples/                      main.tf is v1 syntax and invalid (KI-10); variables.tf, terraform.tfvars.example
.github/workflows/ci.yml       build + test on PRs to main
.github/workflows/release.yml  GoReleaser on v* tags (GPG signing)
.github/pull_request_template.md
.goreleaser.yml, terraform-registry-manifest.json   release config
```

Sibling directories outside this repo (separate repos, not editable from here without their own rules): `../bicc-job-cli` (reference implementation, Python), `../bicc-job-tf` (consumer), `../sit_testing` (scratch Terraform dir with local state, reads `.env`; not run yet).

## 6. Cold-start procedure

1. Read this file, [README.md](README.md), [docs/PLAN.md](docs/PLAN.md), [docs/KNOWN-ISSUES.md](docs/KNOWN-ISSUES.md), [docs/DECISIONS.md](docs/DECISIONS.md). Read [docs/ASSUMPTIONS.md](docs/ASSUMPTIONS.md) before touching anything about BICC API behaviour.
2. Check state: `git status`, `git branch -r`, `gh pr list --state open`. Do not treat an old-looking branch as abandoned.
3. Work in an isolated worktree (`git worktree add ../tpb-<topic> -b <branch> origin/main`). Worktrees isolate files only; local Terraform state and any BICC job names are shared, so prefix throwaway jobs with your own token.
4. Establish the baseline: `go build ./... && go vet ./... && go test ./... -count=1` (all pass on 2026-11-03).
5. Pick ONE ready task from PLAN.md. Claim it by opening a draft PR early with the task number in the title and "IN PROGRESS: owner/session, branch, worktree" in the body (social claim, not a lock).
6. Build, test (see below), update the owning doc in the same PR, request review according to the lane.

## 7. How to verify (required for every code change)

- Always: `go build ./...`, `go vet ./...`, `go test ./... -count=1`. Re-run them yourself; do not rely on a report.
- Behaviour changes in Read/Import/Delete/ModifyPlan/client need a test that fails before the fix (negative proof). Tasks 2 to 6 are listed with their expected behaviour in PLAN.md.
- A passing schema test is not proof of Terraform behaviour (KI-1 was missed that way). Prefer a test that goes through the plugin framework, or a `terraform plan` in a scratch directory.
- Claims about BICC API behaviour: label `observed` (with date and request), `reported` or `unverified`. Update ASSUMPTIONS.md when a check is run.
- Live checks write to a real system: red lane. Follow "How to run the live checks" in ASSUMPTIONS.md.
- Completion is reported per dimension: source/CI complete, released (tag and registry), used in `bicc-job-tf` (imported and plan clean), accepted by the owner. Merged is not released; released is not in use.

## 8. Quick reference: where to find what

| Question | Location |
|---|---|
| What should I work on next? | `docs/PLAN.md` task table |
| Is X a known bug? | `docs/KNOWN-ISSUES.md` |
| Is this API behaviour verified? How do I check it? | `docs/ASSUMPTIONS.md` |
| Why is it designed this way (deletion, backfill, scope)? | `docs/DECISIONS.md`, README "Design" |
| What changes in the consumer repo? | `docs/CONSUMER-MIGRATION.md` |
| What are the resource attributes? | `docs/resources/job.md` and `bicc/resource_job.go` (code wins) |
| How do I build, test, install locally? | `README.md` (Installation, Development) |
| How is a release made? | `.github/workflows/release.yml`, `.goreleaser.yml`; red lane |
| How does the CLI behave (reference behaviour)? | `../bicc-job-cli/bicc/` (`payload.py`, `diff.py`, `commands.py`) |

## 9. Credentials and access

- BICC: `BICC_HOST`, `BICC_USERNAME`, `BICC_PASSWORD` environment variables (the provider block can be empty). Locally they live in `../sit_testing/.env` (git-ignored, never read or print it; agents cannot read `.env`). In the consumer pipeline they come from secret injection; the secret names are in the consumer repo (reported, not checked).
- Release signing: GitHub Actions secrets `GPG_PRIVATE_KEY` and `PASSPHRASE` (names from `release.yml`). Never in the repo.
- GitHub: repo `tra-jkv/terraform-provider-bicc`; use the `gh` CLI. Whether the logged-in account may push here is not verified.
- Terraform GCS backend of the consumer repo: reachable only from its pipelines, not from a developer machine (DR-4).

## 10. Key people

| Who | Role | Evidence |
|---|---|---|
| Tra Nguyen (GitHub `tra-jkv`) | Author of all commits and the repo owner; owner-only decisions go here | `git log`, README "Author"; ownership of decisions is inferred, confirm |
| Owner of `bicc-job-tf` and of the Terraform pipeline image | Unknown, not established (Q2) | PLAN/DECISIONS |

## 11. Key links

| What | Link |
|---|---|
| Repo | https://github.com/tra-jkv/terraform-provider-bicc |
| Registry page | https://registry.terraform.io/providers/tra-jkv/bicc/latest |
| BICC docs | https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/index.html |
| BICC REST API | https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/manage-meta-data-rest-api1-vo-attributes-for-sdm-vo-and-non-sdm.html |
| Plugin framework | https://developer.hashicorp.com/terraform/plugin/framework |
| Registry doc format | https://developer.hashicorp.com/terraform/registry/providers/docs |

## 12. Keeping this current

After significant work, in the same PR: update the owning doc only (ownership is in the structure tree above), refresh the "must know now" lines in section 3, mark superseded decisions in DECISIONS.md, and prune. Never write a state like "PR #N open" in a status file. Commit message prefix for docs: `docs: …`.
