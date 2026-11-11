#!/usr/bin/env bash
set -euo pipefail

THRESHOLD="${1:-7.0}"
BASE_REF="${BASE_REF:-origin/main}"
ARTIFACT_DIR="${ARTIFACT_DIR:-}"
PROJECT_ROOT="$(readlink -e "$(dirname "${BASH_SOURCE[0]}")/../")"
WORK_DIR="$(mktemp -d)"
BASE_DIR="${WORK_DIR}/base"
BASE_JSON="${WORK_DIR}/base.json"
CANDIDATE_JSON="${WORK_DIR}/candidate.json"
SUMMARY_FILE="${WORK_DIR}/security-summary.md"
RESULT_FILE="${WORK_DIR}/security-gate-result.json"

save_artifacts() {
    if [[ -z "${ARTIFACT_DIR}" ]]; then
        return
    fi
    if ! mkdir -p "${ARTIFACT_DIR}"; then
        return
    fi
    for file in "${BASE_JSON}" "${CANDIDATE_JSON}" "${SUMMARY_FILE}" "${RESULT_FILE}"; do
        if [[ -f "${file}" ]]; then
            cp "${file}" "${ARTIFACT_DIR}/$(basename "${file}")"
        fi
    done
}

cleanup() {
    save_artifacts
    git worktree remove --force "${BASE_DIR}" >/dev/null 2>&1 || true
    rm -rf "${WORK_DIR}"
}
trap cleanup EXIT INT TERM

BASE_SHA="$(git merge-base "${BASE_REF}" HEAD)"
git worktree add --detach "${BASE_DIR}" "${BASE_SHA}" >/dev/null

# Exit codes: 0 gate passed, 1 gate rejected, 2 scan failure, 3 needs review.
if ! make -s -C "${PROJECT_ROOT}" --no-print-directory govulncheck >"${CANDIDATE_JSON}"; then
    echo "candidate govulncheck scan failed" >&2
    exit 2
fi
# The baseline is scanned with its own toolchain, installed from its go.mod,
# so a toolchain-only security update shows up as a fixed vulnerability.
if ! make -s -C "${BASE_DIR}" --no-print-directory govulncheck >"${BASE_JSON}"; then
    echo "baseline govulncheck scan failed" >&2
    exit 2
fi

make -C "${PROJECT_ROOT}" --no-print-directory renovate-security-gate >/dev/null
set +e
"${PROJECT_ROOT}/build/_output/bin/renovate-security-gate" \
    --base "${BASE_JSON}" \
    --candidate "${CANDIDATE_JSON}" \
    --threshold "${THRESHOLD}" \
    --summary "${SUMMARY_FILE}" \
    --result "${RESULT_FILE}"
GATE_STATUS=$?
set -e

if [[ -s "${SUMMARY_FILE}" ]]; then
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
        cat "${SUMMARY_FILE}" >>"${GITHUB_STEP_SUMMARY}"
    fi
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
        {
            echo "security_summary<<SECURITY_SUMMARY_EOF"
            cat "${SUMMARY_FILE}"
            echo "SECURITY_SUMMARY_EOF"
        } >>"${GITHUB_OUTPUT}"
    fi
fi

if [[ -n "${GITHUB_OUTPUT:-}" ]] && [[ -s "${RESULT_FILE}" ]] && command -v jq >/dev/null 2>&1; then
    {
        echo "needs_review=$(jq -r '.needs_review' "${RESULT_FILE}")"
        echo "review_ids=$(jq -r '[.cvss_assessments[] | select(.status == "UNCLEAR") | if .cve != "" then .cve else .osv end] | join(" ")' "${RESULT_FILE}")"
        echo "resolved_ids=$(jq -r '[.cvss_assessments[] | select(.score != null) | (.cve, .osv)] | join(" ")' "${RESULT_FILE}")"
    } >>"${GITHUB_OUTPUT}"
fi

exit "${GATE_STATUS}"
