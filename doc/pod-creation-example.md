# Example - Allocating MAC addresses to a Pod

## Create a a bridge on the node

Using ovs-vsctl, we will add a bridge br1 on node01

```bash
node=node01
./cluster/cli.sh ssh ${node} -- sudo yum install -y http://cbs.centos.org/kojifiles/packages/openvswitch/2.9.2/1.el7/x86_64/openvswitch-2.9.2-1.el7.x86_64.rpm http://cbs.centos.org/kojifiles/packages/openvswitch/2.9.2/1.el7/x86_64/openvswitch-devel-2.9.2-1.el7.x86_64.rpm http://cbs.centos.org/kojifiles/packages/dpdk/17.11/3.el7/x86_64/dpdk-17.11-3.el7.x86_64.rpm
./cluster/cli.sh ssh ${node} -- sudo systemctl daemon-reload
./cluster/cli.sh ssh ${node} -- sudo systemctl restart openvswitch

./cluster/cli.sh ssh ${node} -- sudo ovs-vsctl add-br br1
```

## Create a NetworkAttachmentDefinition

This example will use [ovs-cni](https://github.com/kubevirt/ovs-cni/).

```yaml
cat << EOF | kubectl create -f -
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: ovs-conf
  annotations:
    k8s.v1.cni.cncf.io/resourceName: ovs-cni.network.kubevirt.io/br1
spec:
  config: '{
      "cniVersion": "0.3.1",
      "name": "ovs-conf",
      "plugins" : [
        {
          "type": "ovs",
          "bridge": "br1",
          "vlan": 100
        },
        {
          "type": "tuning"
        }
      ]
    }'
EOF
```

**Note:** the project supports only json configuration for `k8s.v1.cni.cncf.io/networks`, network list will be ignored

## Allocate a MAC addresses to a Pod

Create the Pod definition:
```yaml
cat <<EOF | ./cluster/kubectl.sh create -f -
apiVersion: v1
kind: Pod
metadata:
  name: samplepod
  annotations:
    k8s.v1.cni.cncf.io/networks: '[{ "name": "ovs-conf"}]'
spec:
  containers:
  - name: samplepod
    command: ["/bin/sh", "-c", "sleep 99999"]
    image: alpine
EOF
```

Check Pod deployment:
```bash
kubectl get pod samplepod -oyaml
```

The networks annotation need to contains now a MAC address field
```yaml
k8s.v1.cni.cncf.io/networks: [{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:02"}]
```

MAC address can be also set manually by the user using the MAC field in the annotation.
If the MAC is already in used the system will reject it even if the MAC address is outside of the range.
