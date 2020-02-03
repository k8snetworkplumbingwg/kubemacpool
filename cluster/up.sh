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

echo 'Deploying Linux bridge CNI and Multus ...'
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.25.0/namespace.yaml
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.25.0/network-addons-config.crd.yaml
./cluster/kubectl.sh create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.25.0/operator.yaml
cat <<EOF | ./cluster/kubectl.sh create -f -
apiVersion: networkaddonsoperator.network.kubevirt.io/v1alpha1
kind: NetworkAddonsConfig
metadata:
  name: cluster
spec:
  linuxBridge: {}
  multus: {}
EOF
./cluster/kubectl.sh wait networkaddonsconfig cluster --for condition=Available --timeout=800s

echo 'Deploying Kubevirt ...'
./cluster/kubectl.sh apply -f https://github.com/kubevirt/kubevirt/releases/download/v0.20.4/kubevirt-operator.yaml
./cluster/kubectl.sh create configmap kubevirt-config -n kubevirt --from-literal debug.useEmulation=true
./cluster/kubectl.sh apply -f https://github.com/kubevirt/kubevirt/releases/download/v0.20.4/kubevirt-cr.yaml
./cluster/kubectl.sh wait -n kubevirt kubevirt kubevirt --for condition=Available

echo 'Done'
