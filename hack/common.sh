#!/bin/bash
#
# Copyright 2018 Red Hat, Inc.
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

MACPOOL_DIR="$(
    cd "$(dirname "$BASH_SOURCE[0]")/../"
    pwd
)"

VENDOR_DIR=$MACPOOL_DIR/vendor


MACPOOL_PROVIDER=${MACPOOL_PROVIDER:-k8s-1.13.3}
MACPOOL_NUM_NODES=${MACPOOL_NUM_NODES:-1}

# Use this environment variable to set a custom pkgdir path
# Useful for cross-compilation where the default -pkdir for cross-builds may not be writable
#MACPOOL_GO_BASE_PKGDIR="${GOPATH}/crossbuild-cache-root/"

# If on a developer setup, expose ocp on 8443, so that the openshift web console can be used (the port is important because of auth redirects)
if [ -z "${JOB_NAME}" ]; then
    MACPOOL_PROVIDER_EXTRA_ARGS="${MACPOOL_PROVIDER_EXTRA_ARGS} --ocp-port 8443"
fi

#If run on jenkins, let us create isolated environments based on the job and
# the executor number
provider_prefix=${JOB_NAME:-${MACPOOL_PROVIDER}}${EXECUTOR_NUMBER}
job_prefix=${JOB_NAME:-nativelb}${EXECUTOR_NUMBER}

# Populate an environment variable with the version info needed.
# It should be used for everything which needs a version when building (not generating)
# IMPORTANT:
# RIGHT NOW ONLY RELEVANT FOR BUILDING, GENERATING CODE OUTSIDE OF GIT
# IS NOT NEEDED NOR RECOMMENDED AT THIS STAGE.

function nativelb_version() {
    if [ -n "${MACPOOL_VERSION}" ]; then
        echo ${MACPOOL_VERSION}
    elif [ -d ${MACPOOL_DIR}/.git ]; then
        echo "$(git describe --always --tags)"
    else
        echo "undefined"
    fi
}
MACPOOL_VERSION="$(nativelb_version)"

determine_cri_bin() {
    if podman ps >/dev/null 2>&1; then
        echo podman
    elif docker ps >/dev/null 2>&1; then
        echo docker
    else
        echo "error: no docker or podman found"
        exit 1
    fi
}
