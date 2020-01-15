#!/usr/bin/env bash

set -ex

./cluster/dind-cluster/dind-cluster-v1.13.sh up
docker run -d -p 5000:5000 --rm --network kubeadm-dind-net --name registry registry:2
kubectl config view --raw > ./cluster/dind-cluster/config

# Deploy CNA
./cluster/dind-cluster/kubectl create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.15.0/namespace.yaml
./cluster/dind-cluster/kubectl create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.15.0/network-addons-config.crd.yaml
./cluster/dind-cluster/kubectl create -f https://github.com/kubevirt/cluster-network-addons-operator/releases/download/0.15.0/operator.yaml
./cluster/dind-cluster/kubectl create -f ./hack/cna/cna-cr.yaml

# Deploy Kubevirt
./cluster/dind-cluster/kubectl create -f https://github.com/kubevirt/kubevirt/releases/download/v0.20.4/kubevirt-operator.yaml
./cluster/dind-cluster/kubectl create -f https://github.com/kubevirt/kubevirt/releases/download/v0.20.4/kubevirt-cr.yaml


# Build kubemacpool
REGISTRY="localhost:5000" make docker-build
REGISTRY="localhost:5000" make docker-push

REGISTRY="localhost:5000" make docker-generate
if [[ -n "$(git status --porcelain)" ]] ; then echo "It seems like you need to run make. Please run it and commit the changes"; git status --porcelain; false; fi

# wait for cluster operator
./cluster/dind-cluster/kubectl wait networkaddonsconfig cluster --for condition=Available --timeout=800s

# wait for kubevirt
./cluster/dind-cluster/kubectl wait -n kubevirt kv kubevirt --for condition=Available --timeout 800s

# enable emulation for kubevirt
./cluster/dind-cluster/kubectl create configmap kubevirt-config -n kubevirt --from-literal debug.useEmulation=true

# deploy test kubemacpool
./cluster/dind-cluster/kubectl apply -f config/test/kubemacpool.yaml

# Wait for kubemacpool pod
set +e
retry_counter=0
while [[ "$(./cluster/dind-cluster/kubectl get -n kubemacpool-system deploy | grep -v 1/1 | wc -l)" -eq 0 ]] && [[ $retry_counter -lt 20 ]]; do
    echo "Waiting for kubemacpool to be ready..."
    ./cluster/dind-cluster/kubectl get -n kubemacpool-system deploy
    ./cluster/dind-cluster/kubectl get -n kubemacpool-system pod
    sleep 10
    retry_counter=$((retry_counter + 1))
done

if [ $retry_counter -eq 20 ]; then
    exit 1
fi
set -e

# Give the kubemacpool time to start the webhook service
# TODO: remove this after we implement a readiness check on the pod
sleep 15
