#!/usr/bin/env bash
set -e

CVE_ID="${1}"
if [[ -z "${CVE_ID}" ]]; then
    echo "Usage: $0 <CVE-ID>"
    echo "Example: $0 CVE-2025-22868"
    exit 1
fi

if [[ ! "${CVE_ID}" =~ ^CVE-[0-9]{4}-[0-9]+$ ]]; then
    echo "Error: '${CVE_ID}' is not a valid CVE ID (expected format: CVE-YYYY-NNNNN)"
    exit 1
fi

PROJECT_ROOT="$(readlink -e "$(dirname "${BASH_SOURCE[0]}")/../")"
PARSER="${PROJECT_ROOT}/build/_output/bin/govulncheck-parser"

if [[ ! -x "${PARSER}" ]]; then
    echo "Building govulncheck-parser..."
    make -C "${PROJECT_ROOT}" govulncheck-parser >/dev/null 2>&1
fi

echo "Scanning for ${CVE_ID}..."
if ! OUTPUT=$(make -C "${PROJECT_ROOT}" govulncheck 2>&1); then
    echo "Error: govulncheck scan failed:"
    echo "${OUTPUT}" | tail -10
    exit 2
fi

RESULT=$(echo "${OUTPUT}" | "${PARSER}" "${CVE_ID}") \
    || { echo "Error: failed to parse govulncheck output"; exit 2; }

case "${RESULT}" in
    AFFECTED)
        echo "AFFECTED: ${CVE_ID} — vulnerable code is reachable"
        exit 1
        ;;
    NOT_REACHABLE)
        echo "NOT AFFECTED: ${CVE_ID} — false positive (module is a dependency but vulnerable code is not reachable)"
        exit 0
        ;;
    NOT_FOUND)
        echo "NOT FOUND: ${CVE_ID} — no matching entry in govulncheck results (verify the CVE ID is correct)"
        exit 0
        ;;
    *)
        echo "Error: unexpected result from govulncheck parsing"
        exit 2
        ;;
esac
