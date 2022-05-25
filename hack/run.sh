#!/bin/bash
source hack/common.sh
MACPOOL_PATH="${MACPOOL_PATH:=`pwd`}"

OCI_BIN=${OCI_BIN:-$(determine_cri_bin)}

${OCI_BIN} run --rm -t --user $(id -u):$(id -g) \
           --network host \
           --volume `pwd`:/go/src/github.com/k8snetworkplumbingwg/kubemacpool \
           --volume ${MACPOOL_PATH}/cluster/$MACPOOL_PROVIDER/:$HOME/.kube/ \
           --env KUBECONFIG=cluster/${MACPOOL_PROVIDER}/.kubeconfig \
           --env COVERALLS_TOKEN=$COVERALLS_TOKEN \
           --workdir /go/src/github.com/k8snetworkplumbingwg/kubemacpool \
           --env RANGE_START=02:00:00:00:00:00 \
           --env RANGE_END=02:FF:FF:FF:FF:FF \
           --env POD_NAMESPACE=default \
           --env POD_NAME=kubemacpool-pod \
           --env REGISTRY=${REGISTRY} \
           --env REPO=${REPO} \
            ${DOCKER_BASE_IMAGE} make $@
