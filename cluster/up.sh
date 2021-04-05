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

set -ex pipefail

function getLatestPatchVersion {
  local major_minors=$1
  curl -s https://api.github.com/repos/kubevirt/kubevirt/releases | grep .tag_name | grep ${major_minors} | sort -V | tail -1 | awk -F':' '{print $2}' | sed 's/,//' | xargs
}

source ./cluster/cluster.sh
CNAO_VERSION=v0.42.1

#use kubevirt latest z stream release
KUBEVIRT_VERSION=$(getLatestPatchVersion v0.37)
cluster::install

if [[ "$KUBEVIRT_PROVIDER" != external ]]; then
    $(cluster::path)/cluster-up/up.sh
fi

# Deploy CNA
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/${CNAO_VERSION}/namespace.yaml
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/${CNAO_VERSION}/network-addons-config.crd.yaml
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/${CNAO_VERSION}/operator.yaml
./cluster/kubectl.sh create -f ./hack/cna/cna-cr.yaml

# wait for cluster operator
./cluster/kubectl.sh wait networkaddonsconfig cluster --for condition=Available --timeout=800s


# deploy kubevirt
./cluster/kubectl.sh apply -f https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-operator.yaml

# Ensure the KubeVirt CRD is created
count=0
until ./cluster/kubectl.sh get crd kubevirts.kubevirt.io; do
    ((count++)) && ((count == 30)) && echo "KubeVirt CRD not found" && exit 1
    echo "waiting for KubeVirt CRD"
    sleep 1
done

./cluster/kubectl.sh apply -f https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-cr.yaml

# Ensure the KubeVirt CR is created
count=0
until ./cluster/kubectl.sh -n kubevirt get kv kubevirt; do
    ((count++)) && ((count == 30)) && echo "KubeVirt CR not found" && exit 1
    echo "waiting for KubeVirt CR"
    sleep 1
done

./cluster/kubectl.sh wait -n kubevirt kv kubevirt --for condition=Available --timeout 360s || (echo "KubeVirt not ready in time" && exit 1)

echo "Done"
