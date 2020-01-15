# Build the manager binary
FROM golang:1.12.12 as builder

# Copy in the go src
WORKDIR /go/src/github.com/k8snetworkplumbingwg/kubemacpool
COPY vendor/ vendor/
COPY cmd/    cmd/
COPY pkg/    pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager github.com/k8snetworkplumbingwg/kubemacpool/cmd/manager

# Copy the controller-manager into a thin image
FROM centos:latest
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/kubemacpool/manager /
ENTRYPOINT ["/manager"]
