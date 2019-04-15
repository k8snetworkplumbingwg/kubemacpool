# Kubernetes MAC pool address allocation

## Motivation

As of date, there is no way to ensure MAC address collisions do not occur within a Kubernetes cluster. Many CNI plugins allocate MAC addresses randomly, with no assurance of collision prevention.

Even if a CNI plugin ensures it never creates collisions, a cluster operating with multiple CNI plugins or sharing a broadcast domain with another cluster will remain collision-prone.

## Limitations
CNI plugins that do not implement CNI's MAC address specification will not be supported. The cluster must have multus installed and configured.

## Goals

This document proposes a collision-free allocation of MAC addresses within a single cluster.

### Allocation
This specification will describe an intermediate step prior to the initialization of Pods within a kubernetes cluster. During this step, a Pod's template will be read, processed, and modified to specify a MAC address.

It is also to be noted that besides Pods, the proposed behavior will be generic enough to support future entities that may require MAC address allocation. For convenience sake, I will only refer to Pods throughout this document.

### Collision prevention
This specification will describe how the allocation process (as described in collision prevention section) will ensure no collisions may occur.

## Design
Both the act of modifying a Pod's template and deriving available addresses will be done by a new designated admission controller. The admission controller will operate within a predefined address range. A user will be able to pass their own desired range, or let a default range take place.

### Allocation
In order to control the allocation of MAC addresses for network devices, the admission controller will intercept the initialization of a Pod and observe its NetworkAttachmentDefinition's "mac" field.

There are 2 possibilities for the "mac" field:

It is unpopulated. In such case, the CNI assigns a MAC address.

A static MAC address is present. In such case, the CNI assigns the specified address.

We check if that desired address is already allocated and block the request.

With the introduction of the new admission controller, the first (unpopulated scenario) will be evaluated differently:

If the admission controller is enabled, the admission controller will modify the field with an available address.
If the admission controller is disabled, the default (cni fallback) behavior remains.
Additionally, a designated keyword could be introduced to indicate whether the CNI should be used despite the presence of the MAC pool, e.g. the keyword "use-cni" could indicate to the MAC pool admission controller that it should let the CNI allocate an address.

#### Address allocation example, unpopulated "mac" field:
Pre admission control intervention:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: pod0
  annotations:
  k8s.v1.cni.cncf.io/networks: '[
    { "name" : "macvlan-conf-3" }
    ]'
spec:
  containers:
    name: pod0
    image: docker.io/centos/tools:latest
    command:
      /sbin/init
      
# Post admission control intervention:
apiVersion: v1
kind: Pod
metadata:
  name: pod0
  annotations:
    k8s.v1.cni.cncf.io/networks: '[
       { "name" : "macvlan-conf-3","mac": "c2:b0:57:49:47:f1" }
      ]'
spec:
 containers:
   name: pod0
   image: docker.io/centos/tools:latest
   command:
     /sbin/init
```

### Collision prevention
The admission controller will keep track of the allocated addresses from within its range.

Allocated addresses will be stored. Upon address allocation, the admission controller will find an available address from within its range. This address will be allocated in the Pod and added to to the used addresses' store.

Upon address deallocation, the admission controller will delete the used MAC address. After removal from the store, it will be available for allocation once more.

## High availability and scalability
In order to ensure high availability and scalability of the MAC pool admission controller, we suggest 2 possible implementations:

1) Shared DB: run multiple replicas of the admission controller via kubernetes' Deployment, where all the locking and distribution is managed by the DB (e.g. etcd).

2) Range sharding: run multiple replicas of the admission controller via kubernetes' StatefulSet. Each replica will manage a different subset of the whole range.
Shared DB approach may be easier to implement, however writing to a database may introduce a bottleneck.

Range sharding will require additional implementation logic, however addresses will be able to be allocated concurrently.