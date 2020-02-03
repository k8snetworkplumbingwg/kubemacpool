#!/bin/bash -xe

teardown() {
    make cluster-down
    cp $(find . -name "*junit*.xml") $ARTIFACTS
}

main() {
    export KUBEVIRT_PROVIDER='k8s-1.17.0'
    source automation/check-patch.setup.sh
    cd ${TMP_PROJECT_PATH}

    # Let's fail fast if it's not compiling
    make docker-build

    make cluster-down
    make cluster-up
    trap teardown EXIT SIGINT SIGTERM SIGSTOP
    make cluster-sync
    make functest
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
