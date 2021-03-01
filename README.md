# Kubemacpool

Kubemacpool provides collision-aware MAC address allocation for Pods and VirtualMachines in a Kubernetes cluster operating 
with multiple CNI plugins, using [Network Plumbing Working Group de-facto standard](https://github.com/k8snetworkplumbingwg/multi-net-spec).

## Deployment

See the deployment guide to learn how to install Kubemacpool on your [arbitrary cluster](doc/deployment-on-arbitrary-cluster.md).

In case you just want to try it out, consider spinning up a [local virtualized cluster](doc/deployment-on-local-cluster.md).

**Note:** For VirtualMachines, Kubemacpool supports primary interface MAC address allocation for [masquerade](https://kubevirt.io/user-guide/virtual_machines/interfaces_and_networks/#masquerade) or [slirp](https://kubevirt.io/user-guide/virtual_machines/interfaces_and_networks/#slirp) binding mechanisms.

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
