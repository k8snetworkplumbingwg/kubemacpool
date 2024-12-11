ARG BUILD_ARCH=amd64
FROM --platform=linux/${BUILD_ARCH} quay.io/centos/centos:stream9 AS builder
ARG TARGETOS
ARG TARGETARCH
ENV TARGETOS=${TARGETOS:-linux}
ENV TARGETARCH=${TARGETARCH:-amd64}

ARG BUILDOS
ARG BUILDARCH
ENV BUILDOS=${BUILDOS:-linux}
ENV BUILDARCH=${BUILDARCH:-amd64}

WORKDIR /go/src/kubemacpool
RUN dnf install -y wget && dnf clean all
COPY go.mod .
COPY go.sum .
RUN GO_VERSION=$(sed -En 's/^go +(.*)$/\1/p' go.mod) && \
    wget https://dl.google.com/go/go${GO_VERSION}.${BUILDOS}-${BUILDARCH}.tar.gz && \
    tar -C /usr/local -xzf go${GO_VERSION}.${BUILDOS}-${BUILDARCH}.tar.gz && \
    rm go${GO_VERSION}.${BUILDOS}-${BUILDARCH}.tar.gz

ENV PATH=$PATH:/usr/local/go/bin
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o build/_output/bin/manager github.com/k8snetworkplumbingwg/kubemacpool/cmd/manager

# Copy the controller-manager into a thin image
FROM --platform=linux/${TARGETARCH} registry.access.redhat.com/ubi9/ubi-minimal
COPY --from=builder /go/src/kubemacpool/build/_output/bin/manager /
ENTRYPOINT ["/manager"]
