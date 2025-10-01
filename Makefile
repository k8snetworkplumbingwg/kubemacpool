# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
REPO ?= kubevirt
IMAGE_TAG ?= latest
IMAGE_GIT_TAG ?= $(shell git describe --abbrev=8 --tags)
IMG ?= $(REPO)/kubemacpool
OCI_BIN ?= $(shell if podman ps >/dev/null 2>&1; then echo podman; elif docker ps >/dev/null 2>&1; then echo docker; fi)
TLS_SETTING := $(if $(filter $(OCI_BIN),podman),--tls-verify=false,)
PLATFORM_LIST ?= linux/amd64,linux/s390x,linux/arm64
ARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
PLATFORMS ?= linux/${ARCH}
PLATFORMS := $(if $(filter all,$(PLATFORMS)),$(PLATFORM_LIST),$(PLATFORMS))
# Define the platforms for building a multi-platform image.
# Example:
# PLATFORMS ?= linux/amd64,linux/arm64,linux/s390x
# Alternatively, you can set the PLATFORMS variable using:
# export PLATFORMS=linux/arm64,linux/s390x,linux/amd64
# or export PLATFORMS=all to automatically include all supported platforms.
DOCKER_BUILDER ?= kubemacpool-docker-builder
KUBEMACPOOL_IMAGE_TAGGED := ${REGISTRY}/${IMG}:${IMAGE_TAG}
KUBEMACPOOL_IMAGE_GIT_TAGGED := ${REGISTRY}/${IMG}:${IMAGE_GIT_TAG}

BIN_DIR = $(CURDIR)/build/_output/bin/

export GOFLAGS=-mod=vendor
export GO111MODULE=on
export GOROOT=$(BIN_DIR)/go/
export GOBIN=$(GOROOT)/bin/
export PATH := $(GOBIN):$(PATH)
export GO := $(GOBIN)/go
KUSTOMIZE := GOFLAGS=-mod=mod $(GO) run sigs.k8s.io/kustomize/kustomize/v4@v4.5.2
CONTROLLER_GEN := GOFLAGS=-mod=mod $(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.6.0
GOFMT := GOFLAGS=-mod=mod $(GO)fmt
VET := GOFLAGS=-mod=mod $(GO) vet
DEEPCOPY_GEN := GOFLAGS=-mod=mod $(GO) install k8s.io/code-generator/cmd/deepcopy-gen@latest
GO_VERSION = $(shell hack/go-version.sh)
GOLANGICI_LINT ?= GOFLAGS=-mod=mod $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
LINTER_COVERAGE ?= tests/...

E2E_TEST_EXTRA_ARGS ?=
export E2E_TEST_TIMEOUT ?= 100m
E2E_TEST_ARGS ?= $(strip -test.v -test.timeout=$(E2E_TEST_TIMEOUT) -ginkgo.timeout=$(E2E_TEST_TIMEOUT) -ginkgo.v $(E2E_TEST_EXTRA_ARGS))

export KUBECTL ?= cluster/kubectl.sh

all: generate

$(GO):
	hack/install-go.sh $(BIN_DIR) > /dev/null

# Run tests
test: $(GO)
	$(GO) test ./pkg/... ./cmd/... -coverprofile cover.out

functest: $(GO)
	GO=$(GO) TEST_ARGS="$(E2E_TEST_ARGS)" ./hack/functest.sh

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: generate-deploy
	$(KUBECTL) apply -f config/test/kubemacpool.yaml

deploy-test: generate-test
	$(KUBECTL) apply -f config/test/kubemacpool.yaml

generate-deploy: $(GO) manifests
	$(KUSTOMIZE) build config/release > config/release/kubemacpool.yaml

generate-test: $(GO) manifests
	$(KUSTOMIZE) build config/test > config/test/kubemacpool.yaml

generate-external: $(GO) manifests
	cp -r config/test config/external
	cd config/external && \
		$(KUSTOMIZE) edit set image quay.io/kubevirt/kubemacpool=$(REGISTRY)/$(IMG)
	$(KUSTOMIZE) build config/external > config/external/kubemacpool.yaml

# Generate manifests e.g. CRD, RBAC etc.
manifests: $(GO)
	$(CONTROLLER_GEN) crd rbac:roleName=kubemacpool paths=./pkg/... output:crd:dir=config/ output:stdout

# Run go fmt against code
fmt: $(GO)
	$(GOFMT) -d pkg/ cmd/ tests/

# Run go vet against code
vet: $(GO)
	$(VET) ./pkg/... ./cmd/... ./tests/...

lint:
	GOTOOLCHAIN=$$(grep '^toolchain' go.mod | awk '{print $$2}' | sed 's/go//' | awk -F. '{print $$1"."$$2}' || echo ""); \
	$(GOLANGICI_LINT) run --verbose $(LINTER_COVERAGE)

lint-fix:
	GOTOOLCHAIN=$$(grep '^toolchain' go.mod | awk '{print $$2}' | sed 's/go//' | awk -F. '{print $$1"."$$2}' || echo ""); \
	$(GOLANGICI_LINT) run --verbose --fix $(LINTER_COVERAGE)

install-deep-copy: $(GO)
	$(DEEPCOPY_GEN)

# Generate code
generate-go: install-deep-copy fmt vet manifests
	PATH=$(GOBIN):$(PATH) $(GO) generate ./pkg/... ./cmd/...

generate: generate-go generate-deploy generate-test generate-external

check: $(GO)
	./hack/check.sh

# Build the docker image
container:
	./hack/build-multiarch-kubemacpool.sh $(ARCH) $(PLATFORMS) $(KUBEMACPOOL_IMAGE_TAGGED) $(KUBEMACPOOL_IMAGE_GIT_TAGGED) $(DOCKER_BUILDER) $(OCI_BIN) $(REGISTRY)

# Push the docker image
docker-push:
	@if [ "$(OCI_BIN)" = "podman" ]; then \
	    podman manifest push ${TLS_SETTING} ${REGISTRY}/${IMG}:${IMAGE_TAG} ${REGISTRY}/${IMG}:${IMAGE_TAG}; \
	fi
	@if [[ "${REGISTRY}" == localhost* || "${REGISTRY}" == 127.0.0.1* ]]; then \
	    echo "Local registry detected (${REGISTRY}). Skipping IMAGE_GIT_TAG handling."; \
	else \
	    if skopeo inspect docker://${REGISTRY}/${IMG}:${IMAGE_GIT_TAG} >/dev/null 2>&1; then \
	    	echo "Tag '${IMAGE_GIT_TAG}' already exists. Skipping tagging and push."; \
	    elif skopeo inspect docker://${REGISTRY}/${IMG}:${IMAGE_GIT_TAG} 2>&1 | grep -q "manifest unknown"; then \
	        if [ "$(OCI_BIN)" = "podman" ]; then \
	            podman tag ${REGISTRY}/${IMG}:${IMAGE_TAG} ${REGISTRY}/${IMG}:${IMAGE_GIT_TAG}; \
	            podman manifest push ${TLS_SETTING} ${REGISTRY}/${IMG}:${IMAGE_GIT_TAG} ${REGISTRY}/${IMG}:${IMAGE_GIT_TAG}; \
	        fi \
	    else \
	        echo "Error checking for tag '${IMAGE_GIT_TAG}'. Aborting to avoid potential overwrite."; \
	        exit 1; \
	    fi; \
	fi

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

check-go-version:
	./hack/check-go-version.sh

vendor: $(GO)
	$(GO) mod tidy -compat=$(GO_VERSION)
	$(GO) mod vendor

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
	check \
	container \
	push \
	cluster-up \
	cluster-down \
	cluster-sync \
	check-go-version \
	lint
