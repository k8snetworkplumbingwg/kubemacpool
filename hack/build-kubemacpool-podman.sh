#!/bin/bash

if [ -z "$ARCH" ] || [ -z "$PLATFORMS" ] || [ -z "$KUBEMACPOOL_IMAGE_TAGGED" ] || [ -z "$KUBEMACPOOL_IMAGE_GIT_TAGGED" ]; then
    echo "Error: ARCH, PLATFORMS, KUBEMACPOOL_IMAGE_TAGGED, and KUBEMACPOOL_IMAGE_GIT_TAGGED must be set."
    exit 1
fi

IFS=',' read -r -a PLATFORM_LIST <<< "$PLATFORMS"

# Remove any existing manifest and image
podman manifest rm "${KUBEMACPOOL_IMAGE_TAGGED}" 2>/dev/null || true
podman manifest rm "${KUBEMACPOOL_IMAGE_GIT_TAGGED}" 2>/dev/null || true
podman rmi "${KUBEMACPOOL_IMAGE_TAGGED}" 2>/dev/null || true
podman rmi "${KUBEMACPOOL_IMAGE_GIT_TAGGED}" 2>/dev/null || true

podman manifest create "${KUBEMACPOOL_IMAGE_TAGGED}"

for platform in "${PLATFORM_LIST[@]}"; do
    podman build \
        --build-arg BUILD_ARCH="$ARCH" \
        --platform "$platform" \
        --manifest "${KUBEMACPOOL_IMAGE_TAGGED}" \
        -f build/Dockerfile .
done
