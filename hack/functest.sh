#!/usr/bin/env bash

set -xe

source ./cluster/cluster.sh

export KUBEVIRT_CLIENT_GO_SCHEME_REGISTRATION_VERSION=v1
KUBECONFIG=${KUBECONFIG:-$(cluster::kubeconfig)} $GO test ./tests/... -test.timeout=2h --ginkgo.timeout=2h -race -test.v $E2E_TEST_ARGS -ginkgo.v
