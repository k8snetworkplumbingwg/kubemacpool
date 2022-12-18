# Build controller-manager
ARG GO_VERSION
FROM docker.io/library/golang:$GO_VERSION AS builder

WORKDIR $GOPATH/src/github.com/k8snetworkplumbingwg/kubemacpool
COPY . .
RUN GOFLAGS=-mod=vendor GO111MODULE=on CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /manager $GOPATH/src/github.com/k8snetworkplumbingwg/kubemacpool/cmd/manager

# Copy the controller-manager into a thin image
FROM registry.access.redhat.com/ubi8/ubi-minimal
COPY --from=builder /manager /
CMD ["/manager"]
