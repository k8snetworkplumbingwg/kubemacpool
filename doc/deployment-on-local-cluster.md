# Deployment on Local Cluster

This project allows you to spin up virtualized Kubernetes cluster, using 
[kubevirtci](https://github.com/kubevirt/kubevirtci).

## Start an ephemeral cluster

```shell
# Deploy local Kubernetes cluster
make cluster-up

# Build project, build images, push them to cluster's registry and install them
make cluster-sync

# SSH to node01 and open interactive shell
./cluster/cli.sh ssh node01

# Run unit-test
make test

# Run function test
make functest

# SSH to node01 and run command
./cluster/cli.sh ssh node01 echo 'Hello World'

# Communicate with the Kubernetes cluster using kubectl
./cluster/kubectl.sh

# Destroy the cluster
make cluster-down
```
