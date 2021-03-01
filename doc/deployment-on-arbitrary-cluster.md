# Deployment on Arbitrary Cluster

In this guide we will cover installation of Kubemacpool on your arbitrary cluster.

This guide requires you to have your own Kubernetes cluster. If you
don't have one and just want to try Kubemacpool out, please refer to the [deployment
on local cluster](./deployment-on-local-cluster.md) guide.

# Prerequisites

To deploy Kubemacpool on an external cluster, make sure you also deploy the following:
- [Kubevirt v0.37.1 or above](https://github.com/kubevirt/kubevirt/releases/tag/v0.37.1) - See [QuickStart Guide](https://kubevirt.io/quickstart_cloud/)
- [Multus](https://github.com/intel/multus-cni) - See [QuickStart Guide](https://github.com/intel/multus-cni/blob/master/docs/quickstart.md)
- [OVS CNI](https://github.com/kubevirt/ovs-cni) - See [Deployment Guide](https://github.com/kubevirt/ovs-cni/blob/master/docs/deployment-on-arbitrary-cluster.md)

# Deploy Kubemacpool from release manifest

You can download the project release manifest yaml and modify its MAC range to avoid collisions with nearby clusters:

```bash
wget https://raw.githubusercontent.com/k8snetworkplumbingwg/kubemacpool/master/config/release/kubemacpool.yaml
mac_oui=02:`openssl rand -hex 1`:`openssl rand -hex 1`
sed -i "s/02:00:00:00:00:00/$mac_oui:00:00:00/" kubemacpool.yaml
sed -i "s/02:FF:FF:FF:FF:FF/$mac_oui:FF:FF:FF/" kubemacpool.yaml
kubectl apply -f ./kubemacpool.yaml
```
