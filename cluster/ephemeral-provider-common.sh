#!/bin/bash

set -e

_cli="docker run --privileged --net=host --rm ${USE_TTY} -v /var/run/docker.sock:/var/run/docker.sock kubevirtci/gocli@sha256:df958c060ca8d90701a1b592400b33852029979ad6d5c1d9b79683033704b690"

function _main_ip() {
    echo 127.0.0.1
}

function _port() {
    ${_cli} ports --prefix $provider_prefix "$@"
}

function prepare_config() {
    BASE_PATH=${MACPOOL_PATH:-$PWD}
    cat >hack/config-provider-$MACPOOL_PROVIDER.sh <<EOF
master_ip=$(_main_ip)
docker_tag=devel
kubeconfig=${BASE_PATH}/cluster/$MACPOOL_PROVIDER/.kubeconfig
kubectl=${BASE_PATH}/cluster/$MACPOOL_PROVIDER/.kubectl
docker_prefix=localhost:$(_port registry)/k8s-MACPOOL
manifest_docker_prefix=registry:5000/k8s-MACPOOL
EOF
}

function _registry_volume() {
    echo ${job_prefix}_registry
}

function _add_common_params() {
    local params="--nodes ${MACPOOL_NUM_NODES} --memory 4096M --cpu 4 --random-ports --background --prefix $provider_prefix --registry-volume $(_registry_volume) kubevirtci/${image} ${MACPOOL_PROVIDER_EXTRA_ARGS}"
    if [[ $TARGET =~ windows.* ]]; then
        params="--memory 8192M --nfs-data $WINDOWS_NFS_DIR $params"
    elif [[ $TARGET =~ openshift.* ]]; then
        params="--memory 4096M --nfs-data $RHEL_NFS_DIR $params"
    fi
    echo $params
}

function build() {
    # Let's first prune old images, keep the last 5 iterations to improve the cache hit chance
    for arg in ${docker_images}; do
        local name=$(basename $arg)
        images_to_prune="$(docker images --filter "label=${job_prefix}" --filter "label=${name}" --format="{{.ID}} {{.Repository}}:{{.Tag}}" | cat -n | sort -uk2,2 | sort -k1 | tr -s ' ' | grep -v "<none>" | cut -d' ' -f3 | tail -n +6)"
        if [ -n "${images_to_prune}" ]; then
            docker rmi ${images_to_prune}
        fi
    done
}

function _kubectl() {
    export KUBECONFIG=${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubeconfig
    ${MACPOOL_PATH}cluster/$MACPOOL_PROVIDER/.kubectl "$@"
}

function down() {
    ${_cli} rm --prefix $provider_prefix
}
