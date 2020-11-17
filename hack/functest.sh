#!/usr/bin/env bash

set -xe

source ./cluster/kubevirtci.sh

KUBECONFIG=${KUBECONFIG:-$(kubevirtci::kubeconfig)} $GO test ./tests/... $E2E_TEST_ARGS -test.timeout=40m -ginkgo.v -test.v --test-suite-params="$POLARION_TEST_SUITE_PARAMS"
