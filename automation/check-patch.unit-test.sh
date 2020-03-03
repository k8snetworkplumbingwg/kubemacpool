#!/bin/bash -e

teardown() {
    cp $(find . -name "*junit*.xml") $ARTIFACTS || true
}

main() {
    source automation/check-patch.setup.sh
    cd ${TMP_PROJECT_PATH}

    trap teardown EXIT SIGINT SIGTERM SIGSTOP

    make all
    make test
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
