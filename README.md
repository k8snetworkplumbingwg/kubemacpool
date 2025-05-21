# Kubemacpool

Kubemacpool provides collision-aware MAC address allocation for Pods and VirtualMachines in a Kubernetes cluster operating 
with multiple CNI plugins, using [Network Plumbing Working Group de-facto standard](https://github.com/k8snetworkplumbingwg/multi-net-spec).

## Deployment

See the deployment guide to learn how to install Kubemacpool on your [arbitrary cluster](doc/deployment-on-arbitrary-cluster.md).

In case you just want to try it out, consider spinning up a [local virtualized cluster](doc/deployment-on-local-cluster.md).

**Note:** For VirtualMachines, Kubemacpool supports primary interface MAC address allocation for [masquerade](https://kubevirt.io/user-guide/virtual_machines/interfaces_and_networks/#masquerade) binding mechanism.

## Usage

Once Kubemacpool is deployed, you can simply create a Pod or a VirtualMachine, and Kubemacpool will assign a unique 
MAC address from the pool.

For example, a simple VirtualMachine can be created by issuing the following commands:

```bash
wget https://raw.githubusercontent.com/k8snetworkplumbingwg/kubemacpool/master/doc/simple-vm.yaml
kubectl apply -f simple-vm.yaml
```

After VirtualMachine is created, you can check the MAC address under interfaces section:
```bash
kubectl get vm simplevm -oyaml
```

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: simplevm
...
spec:
  template:
    spec:
      domain:
        devices:
          interfaces:
          - macAddress: "02:00:00:00:00:00"
            masquerade: {}
            name: default
      networks:
      - name: default
        pod: {}
...
```

For more examples see [VirtualMachine Allocation Example Guide](doc/vm-creation-example.md) and [Pod Allocation Example Guide](doc/pod-creation-example.md).

### Kubemacpool Opt-modes

A user can specify whether MAC address will be allocated to all VirtualMachines and Pods by default or not.
For more information, see [Kubemacpool Opt-Modes](doc/opt-modes.md).

### Certificate handling

Part of the Kubemacpool functionality is implemented as a [kubernetes mutating admission webhook](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#mutatingadmissionwebhook) 
to ensure that the MAC address is assigned before VirtualMachine is created.
This webhook uses self signed certificate. For more information see [certificate handling](doc/certificate-handling.md).

### Metrics 
Kubemacpool [Prometheus](https://prometheus.io/) expose the following metric:
- `kubevirt_kmp_duplicate_macs`

The metric is a Gouge, its incremented when Kubemacpool detects MAC address conflict by Kubevirt VirtualMachines.

The metric can be used as a data source for firing alert using [Alertmanager](https://prometheus.io/docs/alerting/latest/alertmanager/).

#### Metrics endpoint deployment
Kubemacpool Deployment consist of two containers `manager` and `kube-rbac-proxy`.

The metrics endpoint runs in the `manager` container and listen to port `8080`.
* The port is controlled by the `manager` app `--metric-addr` flag.
The metric endpoint is not secured. 

The `kube-rbac-proxy` runs an instance of [kube-rbac-proxy](https://github.com/brancz/kube-rbac-proxy).
It proxies the insecure metric endpoint and provide a secured endpoint listening to port `8443`.

The secured metric endpoint port, `8443` is exposed, it allows propitious stack to 
scrap Kubemacpool the metrics.
