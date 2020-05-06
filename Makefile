# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
IMAGE_TAG ?= latest
IMG ?= kubevirt/kubemacpool
DOCKER_BUILDER_LOCATION=hack/docker-image

BIN_DIR = $(CURDIR)/build/_output/bin/

export GOFLAGS=-mod=vendor
export GO111MODULE=on
export GOROOT=$(BIN_DIR)/go/
export GOBIN=$(GOROOT)/bin/
export PATH := $(GOBIN):$(PATH)
GOFMT := $(GOBIN)/gofmt
GO := $(GOBIN)/go

export KUBECTL ?= cluster/kubectl.sh

all: generate generate-deploy generate-test

$(GO):
	hack/install-go.sh $(BIN_DIR) > /dev/null

$(GOFMT): $(GO)

# Run tests
test: $(GO)
	$(GO) test ./pkg/... ./cmd/... -coverprofile cover.out

functest: $(GO)
	GO=$(GO) ./hack/functest.sh

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: generate-deploy
	$(KUBECTL) apply -f config/test/kubemacpool.yaml

deploy-test: generate-test
	$(KUBECTL) apply -f config/test/kubemacpool.yaml

generate-deploy: manifests
	kustomize build config/release > config/release/kubemacpool.yaml

generate-test: manifests
	kustomize build config/test > config/test/kubemacpool.yaml

# Generate manifests e.g. CRD, RBAC etc.
manifests: $(GO)
	$(GO) run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go crd rbac:roleName=kubemacpool paths=./pkg/... output:crd:dir=config/ output:stdout

# Run go fmt against code
fmt: $(GOFMT)
	$(GOFMT) -d pkg/ cmd/ tests/

# Run go vet against code
vet:
	$(GO) vet ./pkg/... ./cmd/... ./tests/...

# Generate code
generate: $(GO) fmt vet manifests
	$(GO) generate ./pkg/... ./cmd/...

goveralls:
	./hack/goveralls.sh

docker-goveralls: docker-builder docker-test
	DOCKER_BASE_IMAGE=${REGISTRY}/${IMG}:kubemacpool_builder ./hack/run.sh goveralls

docker-generate: docker-builder
	DOCKER_BASE_IMAGE=${REGISTRY}/${IMG}:kubemacpool_builder ./hack/run.sh

manager: $(GO)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/manager github.com/k8snetworkplumbingwg/kubemacpool/cmd/manager

# Build the docker image
container: manager
	docker build build/ -t ${REGISTRY}/${IMG}:${IMAGE_TAG}

# Build the docker builder image
docker-builder:
	docker build ${DOCKER_BUILDER_LOCATION} -t ${REGISTRY}/${IMG}:kubemacpool_builder --build-arg GO_VERSION=1.12.12

# Push the docker image
docker-push:
	docker push ${REGISTRY}/${IMG}:${IMAGE_TAG}

cluster-up:
	./cluster/up.sh

cluster-down:
	./cluster/down.sh

cluster-sync:
	./cluster/sync.sh

tools-vendoring: $(GO)
	./hack/vendor-tools.sh $$(pwd)/tools.go

.PHONY: test deploy deploy-test generate-deploy generate-test manifests fmt vet generate goveralls docker-goveralls docker-test manager container docker-push cluster-up cluster-down cluster-sync tools-vendoring docker-build-base-image
