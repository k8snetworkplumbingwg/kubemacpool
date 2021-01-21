#!/usr/bin/env bash

set -xe

source ./cluster/kubevirtci.sh

export KUBEVIRT_CLIENT_GO_SCHEME_REGISTRATION_VERSION=v1
KUBECONFIG=${KUBECONFIG:-$(kubevirtci::kubeconfig)} $GO test -test.timeout=40m -race -test.v ./tests/... $E2E_TEST_ARGS -ginkgo.v --test-suite-params="$POLARION_TEST_SUITE_PARAMS"
