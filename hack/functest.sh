#!/usr/bin/env bash

set -xe

source ./cluster/kubevirtci.sh

KUBECONFIG=${KUBECONFIG:-$(kubevirtci::kubeconfig)} $GO test -test.timeout=2h -race -test.v ./tests/... $E2E_TEST_ARGS -ginkgo.v --test-suite-params="$POLARION_TEST_SUITE_PARAMS"
