#!/usr/bin/env bash

set -xe

source ./cluster/cluster.sh

export KUBECONFIG=${KUBECONFIG:-$(cluster::kubeconfig)}
export PATH=$(pwd)/build/_output/bin:${PATH}
${GO} test ./tests/... ${TEST_ARGS}
