#!/usr/bin/env bash

source hack/common.sh
KUBECONFIG=${MACPOOL_DIR}/cluster/$MACPOOL_PROVIDER/.kubeconfig go test ./tests/...
