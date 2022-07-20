# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
REPO ?= kubevirt
IMAGE_TAG ?= latest
IMAGE_GIT_TAG ?= $(shell git describe --abbrev=8 --tags)
IMG ?= $(REPO)/kubemacpool
OCI_BIN ?= $(shell if podman ps >/dev/null 2>&1; then echo podman; elif docker ps >/dev/null 2>&1; then echo docker; fi)
TLS_SETTING := $(if $(filter $(OCI_BIN),podman),--tls-verify=false,)

BIN_DIR = $(CURDIR)/build/_output/bin/

export GOFLAGS=-mod=vendor
export GO111MODULE=on
export GOROOT=$(BIN_DIR)/go/
export GOBIN=$(GOROOT)/bin/
export PATH := $(GOBIN):$(PATH)
GOFMT := $(GOBIN)/gofmt
export GO := $(GOBIN)/go
KUSTOMIZE := $(GOBIN)/kustomize
CONTROLLER_GEN := $(GOBIN)/controller-gen
DEEPCOPY_GEN := $(GOBIN)/deepcopy-gen
GOVERALLS := $(GOBIN)/goveralls

export KUBECTL ?= cluster/kubectl.sh

all: generate

$(GO):
	hack/install-go.sh $(BIN_DIR) > /dev/null

$(KUSTOMIZE): go.mod
	$(MAKE) tools

$(CONTROLLER_GEN): go.mod
	$(MAKE) tools

$(DEEPCOPY_GEN): go.mod
	$(MAKE) tools

$(GOVERALLS): go.mod
	$(MAKE) tools

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

generate-deploy: $(KUSTOMIZE) manifests
	$(KUSTOMIZE) build config/release > config/release/kubemacpool.yaml

generate-test: $(KUSTOMIZE) manifests
	$(KUSTOMIZE) build config/test > config/test/kubemacpool.yaml

generate-external: $(KUSTOMIZE) manifests
	cp -r config/test config/external
	cd config/external && \
		$(KUSTOMIZE) edit set image quay.io/kubevirt/kubemacpool=$(REGISTRY)/$(IMG)
	$(KUSTOMIZE) build config/external > config/external/kubemacpool.yaml

# Generate manifests e.g. CRD, RBAC etc.
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd rbac:roleName=kubemacpool paths=./pkg/... output:crd:dir=config/ output:stdout

# Run go fmt against code
fmt: $(GOFMT)
	$(GOFMT) -d pkg/ cmd/ tests/

# Run go vet against code
vet: $(GO)
	$(GO) vet ./pkg/... ./cmd/... ./tests/...

# Generate code
generate-go: $(DEEPCOPY_GEN) fmt vet manifests
	PATH=$(GOBIN):$(PATH) $(GO) generate ./pkg/... ./cmd/...

generate: generate-go generate-deploy generate-test generate-external

check: $(KUSTOMIZE)
	./hack/check.sh

manager: $(GO)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/manager github.com/k8snetworkplumbingwg/kubemacpool/cmd/manager

# Build the docker image
container: manager
	$(OCI_BIN) build build/ -t ${REGISTRY}/${IMG}:${IMAGE_TAG}

# Push the docker image
docker-push:
	$(OCI_BIN) push ${TLS_SETTING} ${REGISTRY}/${IMG}:${IMAGE_TAG}
	$(OCI_BIN) tag ${REGISTRY}/${IMG}:${IMAGE_TAG} ${REGISTRY}/${IMG}:${IMAGE_GIT_TAG}
	$(OCI_BIN) push ${TLS_SETTING} ${REGISTRY}/${IMG}:${IMAGE_GIT_TAG}

cluster-up:
	./cluster/up.sh

cluster-down:
	./cluster/down.sh

cluster-sync:
	./cluster/sync.sh

cluster-clean:
	./cluster/clean.sh

bump-kubevirtci:
	./hack/bump-kubevirtci.sh

vendor: $(GO)
	$(GO) mod tidy
	$(GO) mod vendor

tools: $(GO)
	GO=$(GO) GOBIN=$(GOBIN) ./hack/install-tools.sh $$(pwd)/tools.go

.PHONY: \
	vendor \
	test \
	deploy \
	deploy-test \
	generate \
	generate-go \
	generate-deploy \
	generate-test \
	manifests \
	fmt \
	vet \
	goveralls \
	check \
	manager \
	container \
	push \
	cluster-up \
	cluster-down \
	cluster-sync \
	tools
