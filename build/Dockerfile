# Copy the controller-manager into a thin image
FROM centos:8
USER root
RUN dnf -y install passwd net-tools
RUN echo 'redhat' | passwd --stdin root 
COPY _output/bin/manager /
ENTRYPOINT ["/manager"]
