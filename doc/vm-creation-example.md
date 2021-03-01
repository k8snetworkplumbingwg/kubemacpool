# Example - Allocating MAC addresses to a VirtualMachine

## Create a NetworkAttachmentDefinition

The NetworkAttachmentDefinition is used to setup the network attachment, i.e. secondary interface for the VirtualMachine.
For more information see [Kubernetes Network Custom Resource Definition De-facto Standard](https://docs.google.com/document/d/1Ny03h6IDVy_e_vmElOqR7UdTPAG_RNydhVE1Kx54kFQ/edit).
This example will use a linux-bridge.

```yaml
cat << EOF | kubectl create -f -
apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
 name: br1
spec:
  config: '{
    "cniVersion": "0.3.1",
    "name": "br1",
      "plugins": [
        {
          "type": "bridge",
          "bridge": "br1"
        },
        {
            "type": "tuning"
        }
      ]
    }'
EOF
```

**Note:** the tuning plugin changes the MAC address after the main plugin was executed. Make sure
the main plugin configure does not have MAC filter on the interface.

## Allocate a MAC addresses to a VirtualMachine

Create the VirtualMachine with 1 secondary NIC:

```yaml
cat << EOF | kubectl create -f -
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: samplevm
spec:
  running: false
  template:
    spec:
      domain:
        devices:
          disks:
          - disk:
              bus: virtio
            name: rootfs
          - disk:
              bus: virtio
            name: cloudinit
          interfaces:
          - name: default
            masquerade: {}
          - bridge: {}
            name: br1
        resources:
          requests:
            memory: 64M
      networks:
      - name: default
        pod: {}
      - multus:
          networkName: br1
        name: br1
      volumes:
        - name: rootfs
          containerDisk:
            image: kubevirt/cirros-registry-disk-demo
        - name: cloudinit
          cloudInitNoCloud:
            userDataBase64: SGkuXG4=
EOF
```

Check VirtualMachine:
```bash
kubectl get vm samplevm -oyaml
```

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: samplevm
spec:
  domain:
...
    devices:
...
      interfaces:
      - macAddress: "02:00:00:00:00:09"
        masquerade: {}
        name: default
      - bridge: {}
        macAddress: "02:00:00:00:00:0a"
...
```

**Note:** Due to current [issue](https://github.com/k8snetworkplumbingwg/kubemacpool/issues/140), you may encounter 
issues when starting the VirtualMachine. A suggested workaround is to disable the MAC address allocation for Pods in your namespace. For more information
see [Kubemacpool Opt-Modes](./opt-modes.md)
