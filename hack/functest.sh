#!/usr/bin/env bash

set -e

source ./cluster/kubevirtci.sh

KUBECONFIG=$(kubevirtci::kubeconfig) go test -timeout 20m -v -race ./tests/...
