#!/bin/env bash

set -xe

namespace=$1

kubectl=./cluster/kubectl.sh

$kubectl get namespace $namespace -o json \
    | jq '.spec.finalizers = []' | \
    $kubectl replace --raw "/api/v1/namespaces/$namespace/finalize" -f -
