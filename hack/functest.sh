#!/usr/bin/env bash

set -xe

source ./cluster/kubevirtci.sh

KUBECONFIG=${KUBECONFIG:-$(kubevirtci::kubeconfig)} $GINKGO -timeout=20m -v -race ./tests/...
