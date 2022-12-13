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
KUSTOMIZE := GOFLAGS=-mod=mod go run sigs.k8s.io/kustomize/kustomize/v4@v4.5.2
CONTROLLER_GEN := GOFLAGS=-mod=mod go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.6.0
DEEPCOPY_GEN := GOFLAGS=-mod=mod go install k8s.io/code-generator/cmd/deepcopy-gen@latest
GO_VERSION = $(shell hack/go-version.sh)

E2E_TEST_EXTRA_ARGS ?=
export E2E_TEST_TIMEOUT ?= 1h
E2E_TEST_ARGS ?= $(strip -test.v -test.timeout=$(E2E_TEST_TIMEOUT) -ginkgo.timeout=$(E2E_TEST_TIMEOUT) $(E2E_TEST_EXTRA_ARGS))

export KUBECTL ?= cluster/kubectl.sh

all: generate

# Run tests
test:
	go test ./pkg/... ./cmd/... -coverprofile cover.out

functest:
	TEST_ARGS="$(E2E_TEST_ARGS)" ./hack/functest.sh

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: generate-deploy
	$(KUBECTL) apply -f config/test/kubemacpool.yaml

deploy-test: generate-test
	$(KUBECTL) apply -f config/test/kubemacpool.yaml

generate-deploy: manifests
	$(KUSTOMIZE) build config/release > config/release/kubemacpool.yaml

generate-test: manifests
	$(KUSTOMIZE) build config/test > config/test/kubemacpool.yaml

generate-external: manifests
	cp -r config/test config/external
	cd config/external && \
		$(KUSTOMIZE) edit set image quay.io/kubevirt/kubemacpool=$(REGISTRY)/$(IMG)
	$(KUSTOMIZE) build config/external > config/external/kubemacpool.yaml

# Generate manifests e.g. CRD, RBAC etc.
manifests:
	$(CONTROLLER_GEN) crd rbac:roleName=kubemacpool paths=./pkg/... output:crd:dir=config/ output:stdout

# Run go fmt against code
fmt:
	GOFLAGS=-mod=mod gofmt -d pkg/ cmd/ tests/

# Run go vet against code
vet:
	GOFLAGS=-mod=mod go vet ./pkg/... ./cmd/... ./tests/...

install-deep-copy:
	$(DEEPCOPY_GEN)

# Generate code
generate-go: install-deep-copy fmt vet manifests
	PATH=$(GOBIN):$(PATH) go generate ./pkg/... ./cmd/...

generate: generate-go generate-deploy generate-test generate-external

check:
	./hack/check.sh

manager:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/manager github.com/k8snetworkplumbingwg/kubemacpool/cmd/manager

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

vendor:
	go mod tidy -compat=$(GO_VERSION)
	go mod vendor

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
	manager \
	container \
	push \
	cluster-up \
	cluster-down \
	cluster-sync
