#!/bin/bash

set -e

image="k8s-multus-1.13.3@sha256:d037d12a7c847067b051a627925a344ffcfe191adf5b53dc84ccdabbac510995"

source cluster/ephemeral-provider-common.sh

function up() {
    ${_cli} run $(_add_common_params)

    # Copy k8s config and kubectl
    ${_cli} scp --prefix $provider_prefix /usr/bin/kubectl - >${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubectl
    chmod u+x ${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubectl
    ${_cli} scp --prefix $provider_prefix /etc/kubernetes/admin.conf - >${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubeconfig

    # Set server and disable tls check
    export KUBECONFIG=${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubeconfig
    ${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubectl config set-cluster kubernetes --server=https://$(_main_ip):$(_port k8s)
    ${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubectl config set-cluster kubernetes --insecure-skip-tls-verify=true

    # Make sure that local config is correct
    prepare_config
}
