# Kubemacpool Opt-Modes

Kubemacpool supports Opt-In and Opt-Out opt-modes, in order to control whether MAC address will 
be allocated to all VirtualMachines and Pods by default or not.
- Opt-Out mode means that MAC addresses for VirtualMachines/Pods are allocated by default, unless specifically chosen by adding a namespace label to exclude it.
- Opt-In mode means that MAC addresses for VirtualMachines/Pods are not allocated by default, unless specifically chosen by adding a namespace label to include it.

The opt-mode can be set for Pods and VirtualMachines separately.

**Note:** The default opt-mode for [testing manifests](../config/test/kubemacpool.yaml) is opt-out for VirtualMachines and Pods. 

**Note:** The default opt-mode for [release manifests](../config/release/kubemacpool.yaml) is opt-out for VirtualMachines and opt-out for Pods.

To set Kubemacpool to allocate all VirtualMachines and Pods by default (For more information on  [opt-out Mode](#How-to-opt-out-kubemacpool-MAC-assignment-for-a-namespace-in-Opt-out-mode)):
```bash
cp config/default/mutatepods_opt_out_patch.yaml config/test/mutatepods_opt_mode_patch.yaml
cp config/default/mutatevirtualmachines_opt_out_patch.yaml config/test/mutatevirtualmachines_opt_mode_patch.yaml
make generate-test
```

To set kubemacpool to not allocate all VirtualMachines and Pods by default (see [opt-in Mode](#How-to-opt-in-kubemacpool-MAC-assignment-for-a-namespace-in-Opt-in-mode))):
```bash
cp config/default/mutatepods_opt_in_patch.yaml config/test/mutatepods_opt_mode_patch.yaml
cp config/default/mutatevirtualmachines_opt_in_patch.yaml config/test/mutatevirtualmachines_opt_mode_patch.yaml
make generate-test
```

**Note:** The User can of course set Pods and VirtualMachines to separate opt-modes by sub-setting the above snippets. 

## Opt-out namespace when in Opt-out mode

You can opt-out MAC address assignment on your namespace by adding the following labels:
- `mutatepods.kubemacpool.io=ignore` - to opt-out MAC assignment for Pods in your namespace
- `mutatevirtualmachines.kubemacpool.io=ignore` - to opt-out MAC assignment for VMs in your namespace

To disable Kubemacpool MAC address assignment on a specific namespace:
```bash
kubectl label namespace example-namespace mutatepods.kubemacpool.io=ignore mutatevirtualmachines.kubemacpool.io=ignore
namespace/example-namespace labeled
```

To re-enable Kubemacpool MAC address assignment in a namespace:
```bash
kubectl label namespace example-namespace mutatepods.kubemacpool.io- mutatevirtualmachines.kubemacpool.io-
namespace/example-namespace labeled
```

## Opt-in namespace when in Opt-in mode

You can opt-in Kubemacpool MAC address assignment on your namespace by adding the following labels:
- `mutatepods.kubemacpool.io=allocate` - to opt-in MAC assignment for Pods in your namespace
- `mutatevirtualmachines.kubemacpool.io=allocate` - to opt-in MAC assignment for VMs in your namespace

To disable Kubemacpool MAC address assignment on a specific namespace:
```bash
kubectl label namespace example-namespace mutatepods.kubemacpool.io- mutatevirtualmachines.kubemacpool.io-
namespace/example-namespace labeled
```

To re-enable Kubemacpool MAC address assignment in a namespace:
```bash
kubectl label namespace example-namespace mutatepods.kubemacpool.io=allocate mutatevirtualmachines.kubemacpool.io=allocate
namespace/example-namespace labeled
```

**Note:** If a VMI is created directly and not through a VM, then it is handled in Kubemacpool by the pod handler.
