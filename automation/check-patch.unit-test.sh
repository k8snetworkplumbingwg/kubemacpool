#!/bin/bash -e

main() {
    source automation/check-patch.setup.sh
    cd ${TMP_PROJECT_PATH}

    make all
    make check
    make test
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
