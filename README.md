# kubemacpool

## About

This project allow to allocate mac addresses from a pool to secondary interfaces using
[Network Plumbing Working Group de-facto standard](https://github.com/k8snetworkplumbingwg/multi-net-spec).
For VirtualMachines, it also allocates a mac address for the primary interface if [masquerade](https://kubevirt.io/user-guide/#/creation/interfaces-and-networks?id=masquerade) or [slirp](https://kubevirt.io/user-guide/#/creation/interfaces-and-networks?id=slirp) binding mechanism is used.

## Usage

For test environment you can use the [development environment](#Develop).

For Production deployment:

* Install any supported [Network Plumbing Working Group de-facto standard](https://github.com/k8snetworkplumbingwg/multi-net-spec) implementation, for example [Multus](https://github.com/intel/multus-cni).
  To deploy multus on a kubernetes cluster with flannel cni:
```bash
kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/kubemacpool/master/hack/multus/kubernetes-multus.yaml
kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/kubemacpool/master/hack/multus/multus.yaml
```

* [CNI plugins](https://github.com/containernetworking/plugins) must be installed in the cluster, for example
```bash
kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/kubemacpool/master/hack/cni-plugins/cni-plugins.yaml
```

* Download the project yaml and modify its mac range to avoid collisions with nearby clusters.
  Finally, deploy the project by applying its yaml.
```bash
wget https://raw.githubusercontent.com/k8snetworkplumbingwg/kubemacpool/master/config/release/kubemacpool.yaml
mac_oui=02:`openssl rand -hex 1`:`openssl rand -hex 1`
sed -i "s/02:00:00:00:00:00/$mac_oui:00:00:00/" kubemacpool.yaml
sed -i "s/02:FF:FF:FF:FF:FF/$mac_oui:FF:FF:FF/" kubemacpool.yaml
kubectl apply -f ./kubemacpool.yaml
```

### Opting-in to kubemacpool service

Kubemacpool is set to allocate mac to [supported interfaces](#About) on pods/vms that reside only on opted-in namespaces. You can opt-in your namespace by adding the following labels:
- `mutatepods.kubemacpool.io=allocate` - to opt in pods mac allocation in your namespace
- `mutatevirtualmachines.kubemacpool.io=allocate` - to opt in vms mac allocation in your namespace

#### How to enable/disable kubemacpool for a namespace

Kubemacpool is disabled by default on a new namespace.
To enable kubemacpool on a specific namespace:
```bash
kubectl label namespace example-namespace mutatepods.kubemacpool.io=allocate mutatevirtualmachines.kubemacpool.io=allocate
namespace/example-namespace labeled
```

To disable kubemacpool in a namespace:
```bash
kubectl label namespace example-namespace mutatepods.kubemacpool.io- mutatevirtualmachines.kubemacpool.io-
namespace/example-namespace labeled
```

**note:** If a VMI is created directly and not through a VM, then it is handled in kubemacpool by the pod handler.

#### How to change the opt-in label value on kubemacpool

The kubemacpool opt-in label and value is set in the mutatingwebhookconfiguration instance called `kubemacpool-mutator` by a `namespaceSelector`.
In order to change the opt-in label value - you need to edit the mutatingwebhookconfiguration instance and change the label value accordingly. This can be done separately for vms and pods.
```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
 ...
  name: kubemacpool-mutator
 ...
webhooks:
- admissionReviewVersions:
 ...
  name: mutatepods.kubemacpool.io
  namespaceSelector:
 ...
    matchLabels:
      mutatepods.kubemacpool.io: allocate
- admissionReviewVersions:
 ...
  name: mutatevirtualmachines.kubemacpool.io
  namespaceSelector:
 ...
    matchLabels:
      mutatevirtualmachines.kubemacpool.io: allocate
 ...
```

**note:** if the kubemacpool's mutatingwebhookconfiguration `kubemacpool-mutator` namespace-selector value per vm/pod is set to `allocate`, then you can also opt-out your namespace by setting the label value to `disable` in your namespace:
```bash
kubectl label namespace example-namespace --overwrite mutatepods.kubemacpool.io=disable mutatevirtualmachines.kubemacpool.io=disable
namespace/example-namespace labeled
```

#### Opt-in Example

```bash
# Add the opt-in labels to namespace using kubectl
kubectl label namespace example-namespace mutatepods.kubemacpool.io=allocate mutatevirtualmachines.kubemacpool.io=allocate
namespace/example-namespace labeled

kubectl get namespaces example-namespace --show-labels
NAME                              STATUS   AGE     LABELS
example-namespace Active   22s     mutatepods.kubemacpool.io=allocate,mutatevirtualmachines.kubemacpool.io=allocate
```

### Check deployment

Configmap:
```bash
[root]# kubectl -n kubemacpool-system describe configmaps

Name:         kubemacpool-mac-range-config
Namespace:    kubemacpool-system
Data
====
RANGE_END:
----
FD:FF:FF:FF:FF:FE

RANGE_START:
----
02:00:00:00:00:11
```

pods:
```bash
kubectl -n kubemacpool-system get po
NAME                                                  READY   STATUS    RESTARTS   AGE
kubemacpool-mac-controller-manager-6894f7785d-t6hf4   1/1     Running   0          107s
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

**note** make sure that the  pod's namespace is opted in for pods.
```yaml
apiVersion: v1
kind: Namespace
metadata:
  labels:
    mutatepods.kubemacpool.io: allocate
  name: default
...
```

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

### Modifing webhook certificate handling

Part of the kubemacpool functionality is implemented as a
[kubernetes mutating admission webhook](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#mutatingadmissionwebhook) to ensure that mac is assigned before vm is created.

Using admission webhooks forces application to handle TLS ca and server
certificates, at kubemacpool this task is delegated to [kube-admission-webook](https://github.com/qinqon/kube-admission-webhook)
library, this library do a mix of controller-runtime webhook server and a
small cert manager so certificates are rotated.

To customaize how the this rotation works some knobs from the library has being
exposed as environment variables at kubemacpool pod:

- `CA_ROTATE_INTERVAL`:  Expiration time for CA certificate, default is one year
- `CA_OVERLAP_INTERVAL`:  Duration the previous to rotate CA certificate live with the new one, defaults to one year
- `CERT_ROTATE_INTERVAL`:  Expiration time for server certificates, defauilt is half a year

## Develop

This project uses [kubevirtci](https://github.com/kubevirt/kubevirtci) to
deploy local cluster.

### Dockerized Kubernetes Provider

Refer to the [kubernetes 1.17](https://github.com/kubevirt/kubevirtci/blob/master/cluster-up/cluster/k8s-1.17/README.md)

### Usage

Use following commands to control it.

*note:* Default Provider is one master + one worker of kubernetes 1.17 nodes
with multus cni plugin.

#### Ephemeral cluster

```shell
# Deploy local Kubernetes cluster
export KUBEVIRT_NUM_NODES=3 # one master + two workers
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

# Run function test
make functest
```
#### External cluster

To deploy at an external cluster not provided by kubevirtci we can to do
the following steps

```bash
# Set env variables
export KUBEVIRT_PROVIDER=external
export KUBECONFIG=[path to the kubeconfig of the cluster]

# Intermediate registry to deploy kubemacpool
export DEV_REGISTRY=docker.io # can be different

# Intermediate repo to deploy kubemacpool
export REPO=$USER # Usually there is a docker.io/$USER this can use

# Deploy kubevirt and multus
make cluster-up

# Build project, build images, push them to $DEV_REGISTRY/$REPO/kubemacpool and
# install it
make cluster-sync

# Destroy the cluster, it will not install kubevirt or multus
make cluster-down

# Run function test
make functest
```
