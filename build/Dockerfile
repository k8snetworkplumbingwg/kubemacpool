# Copy the controller-manager into a thin image
FROM registry.access.redhat.com/ubi9/ubi-minimal
COPY _output/bin/manager /
ENTRYPOINT ["/manager"]
