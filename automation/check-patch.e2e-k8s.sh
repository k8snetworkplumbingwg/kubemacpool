#!/bin/bash -xe

teardown() {
    make cluster-down
    cp ${E2E_LOGS}/*.log $ARTIFACTS || true
    cp $(find . -name "*junit*.xml") $ARTIFACTS || true
}

main() {
    export KUBEVIRT_PROVIDER='k8s-1.30'
    source automation/check-patch.setup.sh
    cd ${TMP_PROJECT_PATH}

    # Let's fail fast if it's not compiling
    make container

    make cluster-down
    make cluster-up
    trap teardown EXIT SIGINT SIGTERM SIGSTOP
    make cluster-sync
    make E2E_TEST_EXTRA_ARGS="-ginkgo.noColor -test.outputdir=$ARTIFACTS --ginkgo.junit-report=junit.functest.xml" functest
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
