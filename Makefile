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
	kubectl apply -f config/test/kubemacpool.yaml

deploy-test: generate-test
	kubectl apply -f config/test/kubemacpool.yaml

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
generate: fmt vet manifests
	go generate ./pkg/... ./cmd/...

goveralls:
	./hack/goveralls.sh

docker-goveralls: docker-builder docker-test
	DOCKER_BASE_IMAGE=${REGISTRY}/${IMG}:kubemacpool_builder ./hack/run.sh goveralls

docker-generate: docker-builder
	DOCKER_BASE_IMAGE=${REGISTRY}/${IMG}:kubemacpool_builder ./hack/run.sh

# Build the docker image
docker-build: docker-builder
	docker build . -t ${REGISTRY}/${IMG}:${IMAGE_TAG}

# Build the docker builder image
docker-builder:
	docker build ${DOCKER_BUILDER_LOCATION} -t ${REGISTRY}/${IMG}:kubemacpool_builder

# Push the docker image
docker-push:
	docker push ${REGISTRY}/${IMG}:${IMAGE_TAG}

cluster-up:
	./cluster/up.sh

cluster-down:
	./cluster/down.sh

cluster-sync:
	./cluster/sync.sh

deploy-test-cluster:
	./hack/deploy-test-cluster.sh

tools-vendoring:
	./hack/vendor-tools.sh $$(pwd)/tools.go

.PHONY: test deploy deploy-test generate-deploy generate-test manifests fmt vet generate goveralls docker-goveralls docker-test docker-build docker-push cluster-up cluster-down cluster-sync deploy-test-cluster tools-vendoring docker-build-base-image
