# Renovate automation design

## Goal

Run Renovate on `main` as a manual pilot, create candidate branches, and open
pull requests only after those branches pass repository validation.

## Workflows

The automation uses two GitHub Actions workflows:

1. `renovate.yaml` runs Renovate manually and creates candidate branches.
2. `renovate-pr-opener.yaml` validates candidate branches, opens pull requests after successful validation, and deletes rejected branches when no pull request exists.

## Branch lifecycle

1. The Renovate workflow is triggered manually on `main`.
2. Renovate creates `renovate/**` branches but does not open pull requests
   until their status checks are green; the opener workflow validates each
   branch and creates the pull request within the same run.
3. The PR opener workflow validates the pushed `renovate/**` branch and checks
   whether an open pull request already exists.
4. If validation succeeds and no open pull request already exists, the opener
   workflow opens the pull request.
5. If validation fails, the opener workflow deletes the branch unless an open
   pull request already exists or the update is held for human review.

The state of an existing pull request is handled as follows:

- If an open pull request already exists for a Renovate branch, the PR opener
  still validates pushes to that branch, does not create a duplicate pull
  request, and refreshes the pull-request body with the latest CVSS summary.
- If a security pull request is manually closed and its branch was deleted,
  the next Renovate run creates a fresh branch, which restarts validation.
- If a security pull request is manually closed but its branch remains and is
  conflicted, `rebaseWhen: conflicted` causes Renovate to refresh that branch,
  which restarts validation.
- `vulnerabilityAlerts.recreateWhen: always` allows Renovate to propose the
  same closed security update again instead of treating it as permanently
  dismissed. When the retained branch is unchanged and not conflicted,
  Renovate recreates the pull request without a new push; that branch already
  passed validation. When the branch no longer exists, the fresh branch
  restarts validation.

## Pull requests

The opener workflow builds each pull request from the candidate branch's
latest commit: the commit subject becomes the pull-request title, and the
body carries the commit message without sign-off trailers, a note about the
validation that ran, and — for security updates — the CVSS summary from the
gate. The CVSS summary sits in a delimited section of the body that is
replaced on every refresh, so repeated evaluations never accumulate stale
summaries. Sign-off trailers stay in the commits, where DCO validation reads
them, and are kept out of the pull-request description. When a pull request
already exists, the CVSS summary is appended only if it changed.

## Validation

All Renovate branches are checked with:

- `make vendor`
- `make check-go-version`
- a clean Git tree after vendoring

Security branches use the `renovate/security/**` prefix and run the Go
vulnerability gate before those checks, so the gate's verdict is recorded
even when the update later fails vendoring. The gate wrapper reports its
outcome as an exit status: `0` approved, `1` rejected, `2` scan failure,
and `3` held for human review. Branches that change Go module files run
`govulncheck` against both:

- the merge base with `main`
- the candidate branch

Each revision is scanned with its own Go toolchain, installed from its own
`go.mod`, so an update that only bumps the toolchain to pick up a
standard-library fix still shows up as a fixed vulnerability in the diff.

`renovate-security-gate` parses both raw `govulncheck` reports and reports two separate checks:

1. **Govulncheck sanity check**
   - fails if the baseline does not have a reachable vulnerability that the candidate fixes, logging that the Renovate alert is a false alarm
   - fails if the candidate introduces a reachable vulnerability
   - passes only when the candidate removes at least one reachable vulnerability and introduces none
   - Renovate does not expose the triggering CVE ID in branch metadata, so the gate verifies removal of at least one reachable baseline vulnerability
2. **CVSS threshold check**
   - passes when at least one fixed CVE is at or above the threshold
   - fails when every fixed CVE has a known score below the threshold
   - is held for human review when no fixed CVE meets the threshold and at least one score is unclear
   - records every fixed CVE and its CVSS result, including unclear results, in the pull-request description

The threshold follows Red Hat Product Security’s guidance for vulnerabilities with CVSS scores of `7.0` and above.

CVSS scores are resolved through the GitHub Advisory API first and fall back
to the NVD CVE API, preferring primary metrics and the newest CVSS version.
Fresh Go advisories often have no score on GitHub while NVD already publishes
one, so the fallback keeps reachable fixes from looking unclear.

Security branches that only update GitHub Actions dependencies do not change
Go module files and skip the Go vulnerability gate. GitHub's Dependabot alert
for the action justifies the update, and the change is limited to workflow
files.

If either check fails, the PR opener workflow deletes the rejected branch when no pull request is already open.

### Unclear CVSS scores

When no fixed CVE meets the threshold and at least one CVSS score is
unclear, the gate cannot decide automatically. The validation fails with a
dedicated status, which keeps the branch alive (cleanup is skipped) and
stops both the opener workflow and Renovate's `status-success` policy from
opening a pull request. This hold only applies when the sanity check
passed: a branch that introduces reachable vulnerabilities is rejected and
cleaned up like any other false alarm, even if it also fixes an unclear
one.

Instead, the `security-review-tracker` job maintains one
`[renovate-security-review] <CVE>` issue per unclear CVE:

- a new issue is created when the CVE is first seen, explaining how to take
  or reject the update
- an open issue is updated on every Renovate run, so it always points at the
  latest evaluation
- a closed issue is left alone, respecting an earlier human rejection
- the issue closes itself once a CVSS score becomes available, because the
  gate can then pass or reject the update automatically

A maintainer takes the update by opening the pull request from the compare
view linked in the issue, or rejects it by closing the issue and letting the
branch be cleaned up once a score confirms the decision.

### Updates that fail validation

A security update can pass the gate and still fail automated validation —
for example when a major bump changes an API and vendoring or the build
breaks. A scan failure is tracked the same way: when a `govulncheck` scan
cannot complete, for example because the update breaks compilation of the
scanned code, the gate cannot evaluate the update at all. Such updates
need human work, so the `security-intervention-tracker` job maintains one
`[renovate-security-intervention] <branch>` issue per failing branch:

- a new issue is created the first time a gate-approved or unevaluable
  update fails validation, linking the validation log and explaining how
  to resolve it
- an open issue is updated on every evaluation, so it always points at the
  latest run
- a closed issue is left alone, respecting an earlier human decision
- the issue closes itself once validation passes and the pull request is
  opened, so a failed pull-request creation keeps the issue open

The compare view linked in the issue targets the evaluated commit rather
than the branch, because cleanup deletes the candidate branch while the
issue remains the durable recovery record.

The candidate branch is still cleaned up like any rejected branch, so it
cannot go stale; Renovate recreates it on every run and the issue is the
durable record. Tracker issues are looked up by their dedicated label, so
the lookup stays correct even when the repository accumulates many issues.

## Debug artifacts

The Renovate workflow uploads a debug log and the repository configuration
file.
The security validation uploads its console log, the raw baseline and candidate
`govulncheck` JSON, the Markdown CVSS summary, and the machine-readable gate
result. Artifacts are retained for seven days.

## Renovate configuration

`renovate.json` targets only `main`, enables the Go module and GitHub Actions
managers, and uses `prCreation: status-success`. Vulnerability alerts use the
`security/` branch prefix and the same PR-creation policy. A pull request is
therefore never created before the branch's validation workflow succeeds: the
opener creates it during the validation run, and Renovate itself would only
create one after the same green status checks, so the two can never race.
Branch topics include the dependency and target version. Renovate rebases an
existing branch only when it is conflicted, and closed security pull requests
are recreated on later Renovate runs.

Renovate discovers `renovate.json` from the repository itself; the workflow
deliberately does not pass it through the action's `configurationFile` input.
Passing the file both ways applies it twice — once as file config and once as
repository config — which concatenates array options such as `extends` and
applied `:gitSignOff` twice, duplicating every commit sign-off.

The update policy is security-only: a blanket package rule disables every
routine update, so Renovate never proposes digest, patch, minor, or major
bumps on its own. Vulnerability alerts override the rule through Renovate's
forced `vulnerabilityAlerts` settings, so every `renovate/security/**`
branch is alert-driven and passes the CVSS gate before a pull request can
open. `postUpgradeTasks` runs `make vendor` once per branch
(`executionMode: branch`) after installing Go via `installTools`, and
commits only `go.mod`, `go.sum`, and `vendor/**`.

The Renovate workflow checks out the repository with
`persist-credentials: false`; the Renovate action clones and pushes with its
own token.

## Authentication

The workflow authenticates Renovate as a GitHub App so the credential is
owned by the organization instead of a personal account. It requires two
repository secrets:

- `RENOVATE_APP_ID`: the numeric App ID of the GitHub App.
- `RENOVATE_APP_PRIVATE_KEY`: the contents of the private key (`.pem`)
  generated for the App.

The GitHub App must be installed on this repository with the following
permissions:

- Contents (read & write): pushing `renovate/**` branches.
- Pull requests (read & write): creating, labeling, and assigning pull
  requests.
- Issues (read & write): maintaining the security tracker issues.
- Workflows (read & write): committing updates to files under
  `.github/workflows/` from the github-actions manager.
- Commit statuses (read & write): reporting `renovate/artifacts` statuses
  when post-upgrade tasks fail.
- Dependabot alerts (read): vulnerability alerts.

Each workflow run mints a fresh installation token through
`actions/create-github-app-token`. Installation tokens expire after one
hour, which does not matter because every run mints a new token and a
Renovate run completes within minutes. The default `GITHUB_TOKEN` cannot
be used because pushes created with it do not trigger the PR opener
workflow; pushes created with a GitHub App installation token do trigger
it.

The workflow runs only through GitHub's `workflow_dispatch` trigger during the
pilot phase. After the workflow is available on `main`, it can be run from the
Actions tab with the same token requirement. A weekly schedule can be
re-enabled later by adding a `schedule` trigger once the pilot is validated.
