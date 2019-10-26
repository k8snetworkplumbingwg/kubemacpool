#!/bin/bash
MACPOOL_PATH="${MACPOOL_PATH:=`pwd`}"
docker run --rm -t --user $(id -u):$(id -g) \
           --network host \
           --volume `pwd`:/go/src/github.com/k8snetworkplumbingwg/kubemacpool \
           --volume ${MACPOOL_PATH}/cluster/$MACPOOL_PROVIDER/:$HOME/.kube/ \
           --env KUBECONFIG=$HOME/.kube/.kubeconfig \
           --env COVERALLS_TOKEN=$COVERALLS_TOKEN \
           --workdir /go/src/github.com/k8snetworkplumbingwg/kubemacpool \
           quay.io/schseba/kube-macpool-base-image:latest make $@
