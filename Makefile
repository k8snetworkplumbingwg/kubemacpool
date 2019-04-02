
# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
IMAGE_TAG ?= latest

IMG ?= schseba/mac-controller

all: generate generate-deploy generate-test

# Run tests
test:
	go test ./pkg/... ./cmd/... -coverprofile cover.out

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
manifests:
	go run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go crd --output-dir config/default/crd
	go run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go rbac --output-dir config/default/rbac

# Run go fmt against code
fmt:
	go fmt ./pkg/... ./cmd/...

# Run go vet against code
vet:
	go vet ./pkg/... ./cmd/...

# Generate code
generate: fmt vet manifests
	go generate ./pkg/... ./cmd/...

goveralls:
	./hack/goveralls.sh

docker-goveralls: docker-test
	./hack/run.sh goveralls

# Test Inside a docker
docker-test:
	./hack/run.sh test

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

.PHONY: test deploy deploy-test generate-deploy generate-test manifests fmt vet generate goveralls docker-goveralls docker-test docker-build docker-push cluster-up cluster-down cluster-sync
