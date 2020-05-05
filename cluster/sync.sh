#!/bin/bash
#
# Copyright 2018-2019 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -ex

source ./cluster/kubevirtci.sh
kubevirtci::install

if [[ "$KUBEVIRT_PROVIDER" == external ]]; then
    if [[ ! -v DEV_REGISTRY ]]; then
        echo "Missing DEV_REGISTRY variable"
        exit 1
    fi
    push_registry=$DEV_REGISTRY
    manifest_registry=$DEV_REGISTRY
    config_dir=./config/external
else
    registry_port=$(./cluster/cli.sh ports registry | tr -d '\r')
    push_registry=localhost:$registry_port
    manifest_registry=registry:5000
    config_dir=./config/test
fi

REGISTRY=$push_registry make container
REGISTRY=$push_registry make docker-push
REGISTRY=$manifest_registry make generate

# Sometimes this get stuck removing the namespace this is fixed by running
# ./hack/clean-ns-finalizers.sh
./cluster/kubectl.sh delete --ignore-not-found -f $config_dir/kubemacpool.yaml || true

# Wait until all objects are deleted
until [[ `./cluster/kubectl.sh get ns | grep "kubemacpool-system " | wc -l` -eq 0 ]]; do
    sleep 5
done

./cluster/kubectl.sh apply -f $config_dir/kubemacpool.yaml

pods_ready_wait() {
  if [[ "$KUBEVIRT_PROVIDER" != external ]]; then
    echo "Waiting for non-kubemacpool containers to be ready ..."
    ./cluster/kubectl.sh wait pod --all -n kube-system --for=condition=Ready --timeout=5m
  fi

  wait_failed=''
  max_retries=12
  retry_counter=0
  echo "Waiting for kubemacpool leader container to be ready ..."
  while [[ "$(./cluster/kubectl.sh wait pod -n kubemacpool-system --for=condition=Ready -l kubemacpool-leader=true --timeout=1m)" = $wait_failed ]] && [[ $retry_counter -lt $max_retries ]]; do
    sleep 5s
    retry_counter=$((retry_counter + 1))
  done

  if [ $retry_counter -eq $max_retries ]; then
    echo "Failed/timed-out waiting for kubemacpool recourses"
    exit 1
  else
    echo "Kubemacpool leader pod is ready"
  fi
}

pods_ready_wait
