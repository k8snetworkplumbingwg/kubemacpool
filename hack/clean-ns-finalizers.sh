#!/bin/env bash

set -xe

kubectl=./cluster/kubectl.sh

$kubectl get namespace kubemacpool-system -o json \
    | jq '.spec.finalizers = []' | \
    $kubectl replace --raw "/api/v1/namespaces/kubemacpool-system/finalize" -f -
