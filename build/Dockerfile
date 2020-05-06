# Copy the controller-manager into a thin image
FROM centos:latest
COPY _output/bin/manager /
ENTRYPOINT ["/manager"]
