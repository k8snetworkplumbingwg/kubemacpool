#!/bin/bash -ex
#
# Copyright 2025 Red Hat, Inc.
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
#

# This script install deny-all NetworkPolicy that affects kubemacpool namespace.

readonly ns="$(./cluster/kubectl.sh get pod -l app=kubemacpool -A -o=custom-columns=NS:.metadata.namespace --no-headers | head -1)"
[[ -z "${ns}" ]] && echo "FATAL: kubemacpool pods not found. Make sure kubemacpool is installed" && exit 1

readonly np_name="deny-all"
./cluster/kubectl.sh -n "${ns}" get networkpolicy "${np_name}" &> /dev/null ||
  cat <<EOF | ./cluster/kubectl.sh -n "${ns}" create -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: ${np_name}
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  - Egress
  ingress: []
  egress: []
EOF
