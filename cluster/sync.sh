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

registry_port=$(./cluster/cli.sh ports registry | tr -d '\r')
registry=localhost:$registry_port

REGISTRY=$registry make docker-build
REGISTRY=$registry make docker-push
REGISTRY=$registry make docker-generate

./cluster/kubectl.sh delete --ignore-not-found -f ./config/test/kubemacpool.yaml || true

# Wait until all objects are deleted
until [[ `./cluster/kubectl.sh get ns | grep "kubemacpool-system " | wc -l` -eq 0 ]]; do
    sleep 5
done

./cluster/kubectl.sh apply -f ./config/test/kubemacpool.yaml

# Make sure all containers are ready
while [ -n "$(./cluster/kubectl.sh get pods --all-namespaces -o'custom-columns=status:status.containerStatuses[*].ready,metadata:metadata.name' --no-headers | grep false)" ]; do
    echo "Waiting for all containers to become ready ..."
    ./cluster/kubectl.sh get pods --all-namespaces -o'custom-columns=status:status.containerStatuses[*].ready,metadata:metadata.name' --no-headers
    sleep 10
done
