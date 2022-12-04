#!/usr/bin/env bash

set -xe

source ./cluster/cluster.sh

export KUBECONFIG=${KUBECONFIG:-$(cluster::kubeconfig)}
${GO} test ./tests/... ${TEST_ARGS}
