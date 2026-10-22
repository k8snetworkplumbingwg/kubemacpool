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
   pull request already exists.

The state of an existing pull request is handled as follows:

- If an open pull request already exists for a Renovate branch, the PR opener
  still validates pushes to that branch but does not create a duplicate pull
  request.
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

## Validation

All Renovate branches are checked with:

- `make vendor`
- `make check-go-version`
- a clean Git tree after vendoring

Security branches use the `renovate/security/**` prefix. Branches that
change Go module files additionally run `govulncheck` against both:

- the merge base with `main`
- the candidate branch

`renovate-security-gate` parses both raw `govulncheck` reports and reports two separate checks:

1. **Govulncheck sanity check**
   - fails if the baseline does not have a reachable vulnerability that the candidate fixes, logging that the Renovate alert is a false alarm
   - fails if the candidate introduces a reachable vulnerability
   - passes only when the candidate removes at least one reachable vulnerability and introduces none
   - Renovate does not expose the triggering CVE ID in branch metadata, so the gate verifies removal of at least one reachable baseline vulnerability
2. **CVSS threshold check**
   - passes when at least one fixed CVE is at or above the threshold, or when its CVSS score is unclear
   - fails when every fixed CVE has a known score below the threshold
   - records every fixed CVE and its CVSS result, including unclear results, in the pull-request description

The threshold follows Red Hat Product Security’s guidance for vulnerabilities with CVSS scores of `7.0` and above.

Security branches that only update GitHub Actions dependencies do not change
Go module files and skip the Go vulnerability gate. GitHub's Dependabot alert
for the action justifies the update, and the change is limited to workflow
files.

If either check fails, the PR opener workflow deletes the rejected branch when no pull request is already open.

## Debug artifacts

The Renovate workflow uploads a debug log and the input configuration file.
The security validation uploads its console log, the raw baseline and candidate
`govulncheck` JSON, and the Markdown CVSS summary. Artifacts are retained for
seven days.

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

The update policy is:

- Go digest, patch, and minor updates are grouped into a single
  `all dependencies` branch; direct major updates are grouped into a separate
  `major dependencies` branch that also rewrites import paths
  (`gomodUpdateImportPaths`).
- Routine updates for `k8s.io/**` and `sigs.k8s.io/**` are disabled because
  they currently require code changes. Vulnerability alerts override this
  rule, so security fixes for those modules still flow through the
  `renovate/security/**` path and the CVSS gate.
- Indirect Go modules keep Renovate's default of no routine updates; their
  security fixes still flow through vulnerability alerts.
- GitHub Actions are updated to newer tags, one branch per action.
- `postUpgradeTasks` runs `make vendor` once per branch (`executionMode:
  branch`) after installing Go via `installTools`, and commits only
  `go.mod`, `go.sum`, and `vendor/**`.

The Renovate workflow checks out the repository with
`persist-credentials: false`; the Renovate action clones and pushes with its
own token.

## Authentication

The workflow requires a `RENOVATE_TOKEN` repository secret backed by a
personal access token with repository write access. GitHub App installation
tokens are not supported because they expire after one hour. The default
`GITHUB_TOKEN` cannot be used because pushes created with it do not trigger the
PR opener workflow.

The workflow runs only through GitHub's `workflow_dispatch` trigger during the
pilot phase. After the workflow is available on `main`, it can be run from the
Actions tab with the same token requirement. A weekly schedule can be
re-enabled later by adding a `schedule` trigger once the pilot is validated.
