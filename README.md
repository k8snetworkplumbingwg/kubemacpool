# kubemacpool

## About

This project allow to allocate mac addresses from a pool to secondary interfaces using 
[Network Plumbing Working Group de-facto standard](https://github.com/K8sNetworkPlumbingWG/multi-net-spec).

## Usage

For test environment you can use the [development environment](#Develop)

For Production deployment:

Install any supported [Network Plumbing Working Group de-facto standard](https://github.com/K8sNetworkPlumbingWG/multi-net-spec) implementation.

For example [Multus](https://github.com/intel/multus-cni).
To deploy multus on a kubernetes cluster with flannel cni.
```bash
kubectl apply -f https://raw.githubusercontent.com/SchSeba/kubemacpool/master/hack/multus/kubernetes-multus.yaml
kubectl apply -f https://raw.githubusercontent.com/SchSeba/kubemacpool/master/hack/multus/multus.yaml
```

[CNI plugins](https://github.com/containernetworking/plugins) must be installed in the cluster.
For CNI plugins you can use the follow command to deploy them inside your cluster.

```bash
kubectl apply -f https://raw.githubusercontent.com/SchSeba/kubemacpool/master/hack/cni-plugins/cni-plugins.yaml
```


Download the project yaml and apply it.

**note:** default mac range is from 02:00:00:00:00:00 to FD:FF:FF:FF:FF:FF the can be edited in the configmap
```bash
wget https://raw.githubusercontent.com/SchSeba/kubemacpool/master/config/release/kubemacpool.yaml
kubectl apply -f ./kubemacpool.yaml
``` 


### Check deployment

Configmap:
```bash
[root]# kubectl -n kubemacpool-system describe configmaps

Name:         kubemacpool-mac-range-config
Namespace:    kubemacpool-system
Data
====
END_POOL_RANGE:
----
FD:FF:FF:FF:FF:FE

START_POOL_RANGE:
----
02:00:00:00:00:11
```

pods:
```bash
kubectl -n kubemacpool-system get po                
NAME                                   READY   STATUS    RESTARTS   AGE
kubemacpool-mac-controller-manager-0   1/1     Running   0          107s
kubemacpool-mac-controller-manager-1   1/1     Running   0          2m19s
```

### Example

Create a network-attachment-definition:

The 'NetworkAttachmentDefinition' is used to setup the network attachment, i.e. secondary interface for the pod.
This is follows the [Kubernetes Network Custom Resource Definition De-facto Standard](https://docs.google.com/document/d/1Ny03h6IDVy_e_vmElOqR7UdTPAG_RNydhVE1Kx54kFQ/edit) 
to provide a standardized method by which to specify the configurations for additional network interfaces. 
This standard is put forward by the Kubernetes [Network Plumbing Working Group](https://docs.google.com/document/d/1oE93V3SgOGWJ4O1zeD1UmpeToa0ZiiO6LqRAmZBPFWM/edit).

```yaml
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
```

This example used [ovs-cni](https://github.com/kubevirt/ovs-cni/).

**note** the tuning plugin change the mac address after the main plugin was executed so 
network connectivity will not work if the main plugin configure mac filter on the interface.


**note** the project supports only json configuration for `k8s.v1.cni.cncf.io/networks`, network list will be ignored

Create the pod definition:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: samplepod
  annotations:
    k8s.v1.cni.cncf.io/networks: '[{ "name": "ovs-conf"}]'
spec:
  containers:
  - name: samplepod
    image: quay.io/schseba/kubemacpool-test:latest
    imagePullPolicy: "IfNotPresent"
```

Check pod deployment:
```yaml
Name:               samplepod
Namespace:          default
Priority:           0
PriorityClassName:  <none>
Node:               node01/192.168.66.101
Start Time:         Thu, 14 Feb 2019 13:36:23 +0200
Labels:             <none>
Annotations:        k8s.v1.cni.cncf.io/networks: [{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:02"}]
                    k8s.v1.cni.cncf.io/networks-status:
                      [{
                          "name": "flannel.1",
                          "ips": [
                              "10.244.0.6"
                          ],
                          "default": true,
                          "dns": {}
                      },{
                          "name": "ovs-conf",
                          "interface": "net1",
                          "mac": "02:00:00:00:00:02",
                          "dns": {}
                      }]
                    kubectl.kubernetes.io/last-applied-configuration:
                      {"apiVersion":"v1","kind":"Pod","metadata":{"annotations":{"k8s.v1.cni.cncf.io/networks":"[{ \"name\": \"ovs-conf\"}]"},"name":"samplepod"...
....
```

The networks annotation need to contains now a mac field
```yaml
k8s.v1.cni.cncf.io/networks: [{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:02"}]
```

MAC address can be also set manually by the user using the MAC field in the annotation.
If the mac is already in used the system will reject it even if the MAC address is outside of the range.
 

## Develop

This project uses [kubevirtci](https://github.com/kubevirt/kubevirtci) to
deploy local cluster.

### Dockerized Kubernetes Provider

Refer to the [kubernetes 1.13.3 with multus document](cluster/k8s-multus-1.13.3/README.md)

### Usage

Use following commands to control it.

*note:* Default Provider is one node (master + worker) of kubernetes 1.13.3
with multus cni plugin.

```shell
# Deploy local Kubernetes cluster
export MACPOOL_PROVIDER=k8s-multus-1.13.3 # choose this provider
export MACPOOL_NUM_NODES=3 # master + two nodes
make cluster-up

# SSH to node01 and open interactive shell
./cluster/cli.sh ssh node01

# SSH to node01 and run command
./cluster/cli.sh ssh node01 echo 'Hello World'

# Communicate with the Kubernetes cluster using kubectl
./cluster/kubectl.sh

# Build project, build images, push them to cluster's registry and install them
make cluster-sync

# Destroy the cluster
make cluster-down
```
