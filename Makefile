# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
IMAGE_TAG ?= latest
IMG ?= kubevirt/kubemacpool

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
	$(GO) run vendor/github.com/kubernetes-sigs/kustomize build config/release > config/release/kubemacpool.yaml

generate-test: manifests
	$(GO) run vendor/github.com/kubernetes-sigs/kustomize build config/test > config/test/kubemacpool.yaml

# Generate manifests e.g. CRD, RBAC etc.
manifests: $(GO)
	$(GO) run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go crd --output-dir config/default/crd
	$(GO) run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go rbac --output-dir config/default/rbac

# Run go fmt against code
fmt: $(GOFMT)
	$(GOFMT) -d pkg/ cmd/ tests/

# Run go vet against code
vet:
	$(GO) vet ./pkg/... ./cmd/... ./tests/...

# Generate code
generate: fmt vet manifests
	$(GO) generate ./pkg/... ./cmd/...

goveralls:
	./hack/goveralls.sh

docker-goveralls: docker-test
	./hack/run.sh goveralls

# Build the docker image
docker-build:
	docker build . -t ${REGISTRY}/${IMG}:${IMAGE_TAG}

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

.PHONY: test deploy deploy-test generate-deploy generate-test manifests fmt vet generate goveralls docker-goveralls docker-test docker-build docker-push cluster-up cluster-down cluster-sync deploy-test-cluster tools-vendoring
