# AGENTS.md - kubemacpool

Kubernetes mutating admission webhook that manages MAC address allocation from a cluster-level pool for Pods and KubeVirt VirtualMachines on secondary networks (Multus/NetworkAttachmentDefinition).

## Project structure

```
cmd/manager/              Entry point; runs webhook server or cert-manager mode (RUN_CERT_MANAGER)
pkg/
  pool-manager/           Core MAC pool: allocation, transactions, collision detection
  webhook/
    pod/                  Mutating webhook for Pods
    virtualmachine/       Mutating webhook for VirtualMachines
  controller/
    pod/                  Pod reconciler — syncs pool state on Pod changes
    virtualmachine/       VM reconciler — handles masquerade interface MACs
    configmap/            Watches kubemacpool ConfigMap for config changes
    vmicollision/         Detects MAC collisions across VirtualMachineInstances
  manager/                KubeMacPoolManager orchestration and setup
  monitoring/
    metrics/              Prometheus metric definitions
    rules/                PrometheusRule and alert definitions
  tls/                    TLS configuration for webhook server
  names/                  Naming constants and utilities
  utils/                  Shared utilities
config/
  default/                Kustomize base with opt-in/opt-out patches
  test/                   Test environment overlay
  release/                Release overlay
  external/               External deployment overlay
  monitoring/             ServiceMonitor and PrometheusRule manifests
  sample/                 Example Pod and VM manifests
tests/                    E2E functional tests (Ginkgo/Gomega)
hack/                     Build scripts, CI helpers, Go installer
tools/                    Prometheus rule generator, metrics docs generator
cluster/                  Local dev cluster management (kubevirtci)
automation/               CI script wrappers
```

## Architecture

### How it works

KubeMacPool intercepts Pod and VM creation/update via mutating admission webhooks. The webhook server allocates MAC addresses from a configurable range and patches the object's network annotation (Pods) or interface spec (VMs) before persistence.

### Key flow

```
Pod/VM Create/Update
  → MutatingWebhookConfiguration routes to webhook server (:8000)
  → poolManager.AllocatePodMac() / AllocateVirtualMachineMac()
  → JSON patch returned in admission response
```

### Pool manager

- Thread-safe MAC map tracking all allocated addresses
- Transaction support with configurable TTL (default 600s) for atomic allocation
- Separate allocation logic for Pods and VMs
- Collision detection across VirtualMachineInstances with Prometheus metrics
- Opt-in or opt-out namespace modes via ConfigMap

### Managed resources

No custom CRDs. Works with standard Kubernetes and KubeVirt types:
- **Pods** — MAC set via `k8s.v1.cni.cncf.io/networks` annotation
- **VirtualMachines** — MAC set in `domain.devices.interfaces[].macAddress`
- **VirtualMachineInstances** — monitored for collision detection

## Build and test

Go toolchain is auto-installed to `build/_output/bin/go/` by `hack/install-go.sh`. No system Go required.

```bash
make test               # Unit tests with coverage (pkg/ and cmd/)
make functest           # E2E tests against a running cluster (Ginkgo/Gomega)
make generate           # Full code generation (deepcopy, manifests, monitoring, docs)
make generate-go        # Go-only: deepcopy, fmt, vet, manifests
make generate-monitoring # Regenerate PrometheusRule YAML
make generate-doc       # Regenerate metrics documentation
make container          # Build multi-arch container image
make deploy-test        # Deploy to cluster (test overlay)
make fmt                # Run gofmt
make vet                # Run go vet
make lint               # golangci-lint v2 (10min timeout)
make lint-metrics       # Prometheus metric naming linter
make check              # Full CI check script
make vendor             # Tidy and vendor Go modules
```

### Verification targets

```bash
make verify-monitoring  # Check PrometheusRule is up to date
make verify-doc         # Check metrics docs are up to date
make prom-rules-verify  # Validate Prometheus rules with prom-rule-ci
```

## Go conventions

- **Module:** `github.com/k8snetworkplumbingwg/kubemacpool`
- **Go version:** 1.25
- **Build flags:** `GOFLAGS=-mod=vendor GO111MODULE=on`
- **Dependencies are vendored.** After modifying `go.mod`, run `make vendor`.
- **Tests:** Ginkgo v2 + Gomega
- **Linting:** golangci-lint v2. Run `make lint` before submitting.

## Local development cluster

Uses kubevirtci to spin up a local Kubernetes cluster.

```bash
make cluster-up         # Start local cluster
make cluster-sync       # Build and deploy kubemacpool to cluster
make cluster-down       # Tear down cluster
make cluster-clean      # Remove kubemacpool from cluster
./cluster/kubectl.sh    # kubectl with correct kubeconfig
```

## Container image

- **Registry:** `quay.io/kubevirt/kubemacpool`
- **Multi-arch:** amd64, arm64, s390x
- **Container runtime:** Auto-detects podman or docker (`OCI_BIN`)

## Environment variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `RANGE_START` | Start of MAC address pool range | — |
| `RANGE_END` | End of MAC address pool range | — |
| `RUN_CERT_MANAGER` | Run in cert-manager mode instead of webhook | `false` |
| `METRICS_PORT` | Metrics server port | `:8443` |
| `E2E_TEST_TIMEOUT` | Functional test timeout | `100m` |
| `OCI_BIN` | Container runtime (podman/docker) | auto-detected |
| `REGISTRY` | Container image registry | `quay.io` |
| `IMAGE_TAG` | Container image tag | `latest` |
| `KUBECONFIG` | Path to kubeconfig | — |

## Git

- **Always sign off commits with `git commit -s`.** This project requires DCO (Developer Certificate of Origin) sign-off.
