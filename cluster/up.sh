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

$(kubevirtci::path)/cluster-up/up.sh

# Deploy CNA
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.35.0/namespace.yaml
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.35.0/network-addons-config.crd.yaml
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.35.0/operator.yaml
./cluster/kubectl.sh create -f ./hack/cna/cna-cr.yaml

# wait for cluster operator
./cluster/kubectl.sh wait networkaddonsconfig cluster --for condition=Available --timeout=800s


# deploy kubevirt
./cluster/kubectl.sh apply -f https://github.com/kubevirt/kubevirt/releases/download/v0.20.4/kubevirt-operator.yaml

# Ensure the KubeVirt CRD is created
count=0
until ./cluster/kubectl.sh get crd kubevirts.kubevirt.io; do
    ((count++)) && ((count == 30)) && echo "KubeVirt CRD not found" && exit 1
    echo "waiting for KubeVirt CRD"
    sleep 1
done

./cluster/kubectl.sh apply -f https://github.com/kubevirt/kubevirt/releases/download/v0.20.4/kubevirt-cr.yaml

# Ensure the KubeVirt CR is created
count=0
until ./cluster/kubectl.sh -n kubevirt get kv kubevirt; do
    ((count++)) && ((count == 30)) && echo "KubeVirt CR not found" && exit 1
    echo "waiting for KubeVirt CR"
    sleep 1
done

./cluster/kubectl.sh wait -n kubevirt kv kubevirt --for condition=Available --timeout 180s || (echo "KubeVirt not ready in time" && exit 1)

echo "Done"
