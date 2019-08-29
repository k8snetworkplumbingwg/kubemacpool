#!/usr/bin/env bash

source hack/common.sh
KUBECONFIG=${MACPOOL_DIR}/cluster/$MACPOOL_PROVIDER/.kubeconfig go test -timeout 20m -v -race ./tests/...
