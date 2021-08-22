/*
Copyright 2019 The KubeMacPool Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pool_manager

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kubevirt "kubevirt.io/client-go/api/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/gauges"
)

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine, transactionTimestamp *time.Time, parentLogger logr.Logger) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger := parentLogger.WithName("AllocateVirtualMachineMac")

	if len(virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
		logger.Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", virtualMachine)
		return nil
	}

	vmFullName := VmNamespaced(virtualMachine)
	if len(getVirtualMachineNetworks(virtualMachine)) == 0 {
		logger.Info("no networks found for virtual machine, skipping mac allocation",
			"vmFullName", vmFullName)
		return nil
	}

	networks := map[string]kubevirt.Network{}
	for _, network := range getVirtualMachineNetworks(virtualMachine) {
		networks[network.Name] = network
	}

	logger.V(1).Info("data before update", "macPoolMap", p.macPoolMap, "requestInterfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)
	copyVM := virtualMachine.DeepCopy()
	newAllocations := map[string]string{}
	for idx, iface := range copyVM.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
			logger.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
			continue
		}

		if iface.MacAddress != "" {
			if err := p.allocateRequestedVirtualMachineInterfaceMac(vmFullName, iface, logger); err != nil {
				p.revertAllocationOnVm(vmFullName, newAllocations)
				return err
			}
			newAllocations[iface.Name] = iface.MacAddress
		} else {
			macAddr, err := p.allocateFromPoolForVirtualMachine(vmFullName, iface, logger)
			if err != nil {
				p.revertAllocationOnVm(vmFullName, newAllocations)
				return err
			}
			copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
			newAllocations[iface.Name] = macAddr
		}
	}

	p.macPoolMap.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, newAllocations)
	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	logger.Info("data after allocation", "Allocations", newAllocations, "updated vm Interfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)

	return nil
}

func (p *PoolManager) ReleaseAllVirtualMachineMacs(vmFullName string, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("ReleaseAllVirtualMachineMacs")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger.V(1).Info("data", "macmap", p.macPoolMap)
	vmMacMap, err := p.macPoolMap.filterInByInstanceName(vmFullName)
	if err != nil {
		return errors.Wrapf(err, "Failed to get VmMacMap for vm %s", vmFullName)
	}

	for macAddress := range *vmMacMap {
		delete(p.macPoolMap, macAddress)
	}

	logger.Info("released macs in virtual machine", "vmMacMap", vmMacMap)
	logger.V(1).Info("macmap after release", "macmap", p.macPoolMap)

	return nil
}

func (p *PoolManager) UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine, transactionTimestamp *time.Time, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("UpdateMacAddressesForVirtualMachine")
	p.poolMutex.Lock()
	if previousVirtualMachine == nil {
		p.poolMutex.Unlock()
		return p.AllocateVirtualMachineMac(virtualMachine, transactionTimestamp, logger)
	}
	defer p.poolMutex.Unlock()

	currentInterfaces := getVirtualMachineInterfaces(previousVirtualMachine)
	requestInterfaces := getVirtualMachineInterfaces(virtualMachine)
	logger.V(1).Info("data before update", "macPoolMap", p.macPoolMap, "currentInterfaces", currentInterfaces, "requestInterfaces", requestInterfaces)

	currentInterfacesMap := make(map[string]string)
	// This map is for deltas
	deltaInterfacesMap := make(map[string]string)
	for _, iface := range currentInterfaces {
		currentInterfacesMap[iface.Name] = iface.MacAddress
		deltaInterfacesMap[iface.Name] = iface.MacAddress
	}

	vmFullName := VmNamespaced(virtualMachine)
	copyVM := virtualMachine.DeepCopy()
	newAllocations := map[string]string{}
	releaseOldAllocations := map[string]string{}
	for idx, requestIface := range requestInterfaces {
		currentlyAllocatedMacAddress, ifaceExistsInCurrentInterfaces := currentInterfacesMap[requestIface.Name]
		if ifaceExistsInCurrentInterfaces {
			if requestIface.MacAddress == "" {
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = currentlyAllocatedMacAddress
				newAllocations[requestIface.Name] = currentlyAllocatedMacAddress
			} else if requestIface.MacAddress != currentlyAllocatedMacAddress {
				// Specific mac address was requested
				err := p.allocateRequestedVirtualMachineInterfaceMac(vmFullName, requestIface, logger)
				if err != nil {
					p.revertAllocationOnVm(vmFullName, newAllocations)
					return err
				}
				releaseOldAllocations[requestIface.Name] = currentlyAllocatedMacAddress
				newAllocations[requestIface.Name] = requestIface.MacAddress
			}
			delete(deltaInterfacesMap, requestIface.Name)

		} else {
			if requestIface.MacAddress != "" {
				if err := p.allocateRequestedVirtualMachineInterfaceMac(vmFullName, requestIface, logger); err != nil {
					p.revertAllocationOnVm(vmFullName, newAllocations)
					return err
				}
				newAllocations[requestIface.Name] = requestIface.MacAddress
			} else {
				macAddr, err := p.allocateFromPoolForVirtualMachine(vmFullName, requestIface, logger)
				if err != nil {
					p.revertAllocationOnVm(vmFullName, newAllocations)
					return err
				}
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
				newAllocations[requestIface.Name] = macAddr
			}
		}
	}

	logger.Info("updating updated mac's transaction timestamp", "newAllocations", newAllocations, "deltaInterfacesMap", deltaInterfacesMap, "releaseOldAllocations", releaseOldAllocations)
	p.macPoolMap.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, newAllocations)
	p.macPoolMap.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, deltaInterfacesMap)
	p.macPoolMap.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, releaseOldAllocations)

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = getVirtualMachineInterfaces(copyVM)
	logger.V(1).Info("data after update", "macmap", p.macPoolMap, "updated interfaces", getVirtualMachineInterfaces(virtualMachine))
	return nil
}

func getVirtualMachineInterfaces(virtualMachine *kubevirt.VirtualMachine) []kubevirt.Interface {
	return virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces
}

func getVirtualMachineNetworks(virtualMachine *kubevirt.VirtualMachine) []kubevirt.Network {
	return virtualMachine.Spec.Template.Spec.Networks
}

func (p *PoolManager) allocateFromPoolForVirtualMachine(vmFullName string, iface kubevirt.Interface, parentLogger logr.Logger) (string, error) {
	logger := parentLogger.WithName("allocateFromPoolForVirtualMachine")
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	p.macPoolMap.createOrUpdateEntry(macAddr.String(), vmFullName, iface.Name)
	logger.V(1).Info("mac from pool was allocated for virtual machine", "allocatedMac", macAddr.String())
	return macAddr.String(), nil
}

func (p *PoolManager) allocateRequestedVirtualMachineInterfaceMac(vmFullName string, iface kubevirt.Interface, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("allocateRequestedVirtualMachineInterfaceMac")
	requestedMac := iface.MacAddress
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	if macEntry, exist := p.macPoolMap[requestedMac]; exist {
		if !macAlreadyBelongsToVmAndInterface(vmFullName, iface.Name, macEntry) {
			err := fmt.Errorf("failed to allocate requested mac address")
			logger.Error(err, fmt.Sprintf("mac address %s already allocated to %s, %s, conflict with: %s, %s",
				iface.MacAddress, macEntry.instanceName, macEntry.macInstanceKey, vmFullName, iface.Name))

			return err
		}
	}

	p.macPoolMap.createOrUpdateEntry(requestedMac, vmFullName, iface.Name)

	logger.V(1).Info("requested mac was allocated for virtual machine", "requestedMap", requestedMac)

	return nil
}

func macAlreadyBelongsToVmAndInterface(vmFullName, interfaceName string, macEntry macEntry) bool {
	if macEntry.instanceName == vmFullName && macEntry.macInstanceKey == interfaceName {
		return true
	}
	return false
}

func (p *PoolManager) initVirtualMachineMap() error {
	logger := log.WithName("initVirtualMachineMap")
	if !p.isKubevirt {
		return nil
	}

	err := p.initMacMapFromCluster(logger)
	if err != nil {
		return errors.Wrap(err, "failed to init MacPoolMap From Cluster")
	}

	err = p.initMacMapFromLegacyConfigMap()
	if err != nil {
		return err
	}
	return nil
}

func (p *PoolManager) initMacMapFromCluster(parentLogger logr.Logger) error {
	err := p.forEachManagedVmInterfaceInClusterRunFunction(func(vmFullName string, iface kubevirt.Interface, networks map[string]kubevirt.Network) error {
		if !validateInterfaceSupported(iface, networks) {
			return nil
		}

		if iface.MacAddress != "" {
			if err := p.allocateRequestedVirtualMachineInterfaceMac(vmFullName, iface, parentLogger); err != nil {
				if strings.Contains(err.Error(), "failed to allocate requested mac address") {
					gauges.DuplicateMacGauge.Inc()
				}
				// Dont return an error here if we can't allocate a mac for a configured vm
				parentLogger.Error(err, "Invalid/Duplicate mac address for virtual machine",
					"virtualMachineFullName", vmFullName,
					"virtualMachineInterfaceMac", iface.MacAddress)
			}
		}
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to iterate the cluster vm interfaces to recreate the macPoolMap")
	}
	return nil
}

// paginateVmsWithLimit performs a vm list request with pagination, to limit the amount of vms received at a time
// and prevent taking too much memory.
func (p *PoolManager) paginateVmsWithLimit(limit int64, vmsFunc func(pods *kubevirt.VirtualMachineList) error) error {
	continueFlag := ""
	for {
		//FIXME we should use the internal limit using ListOptions after we implement using the kubevirt client.
		var result = p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI(fmt.Sprintf("apis/kubevirt.io/v1/virtualmachines?limit=%v&continue=%v", limit, continueFlag)).Do(context.TODO())
		if result.Error() != nil {
			return result.Error()
		}

		vms := &kubevirt.VirtualMachineList{}
		err := result.Into(vms)
		if err != nil {
			return err
		}

		err = vmsFunc(vms)
		if err != nil {
			return err
		}

		continueFlag = vms.GetContinue()
		log.V(1).Info("limit vms list", "vms len", len(vms.Items), "remaining", vms.GetRemainingItemCount(), "continue", continueFlag)
		if continueFlag == "" {
			break
		}
	}
	return nil
}

// forEachManagedVmInterfaceInClusterRunFunction gets all the macs from all the supported interfaces in all the managed cluster vms, and runs
// a function vmInterfacesFunc on it
func (p *PoolManager) forEachManagedVmInterfaceInClusterRunFunction(vmInterfacesFunc func(vmFullName string, iface kubevirt.Interface, networks map[string]kubevirt.Network) error) error {
	err := p.paginateVmsWithLimit(100, func(vms *kubevirt.VirtualMachineList) error {
		logger := log.WithName("forEachManagedVmInterfaceInClusterRunFunction")
		for _, vm := range vms.Items {
			vmNamespace := vm.GetNamespace()
			isNamespaceManaged, err := p.IsVirtualMachineManaged(vmNamespace)
			if err != nil {
				return errors.Wrap(err, fmt.Sprintf("failed to check if namespace %s is managed in current opt-mode", vmNamespace))
			}
			if !isNamespaceManaged {
				logger.V(1).Info("skipping vm in loop iteration, namespace not managed", "vmNamespace", vmNamespace)
				continue
			}
			vmFullName := VmNamespaced(&vm)
			vmInterfaces := getVirtualMachineInterfaces(&vm)
			vmNetworks := getVirtualMachineNetworks(&vm)
			if len(vmInterfaces) == 0 {
				logger.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", vm)
				continue
			}

			if len(vmNetworks) == 0 {
				logger.V(1).Info("no networks found for virtual machine, skipping mac allocation",
					"virtualMachineName", vm.Name,
					"virtualMachineNamespace", vm.Namespace)
				continue
			}

			networks := map[string]kubevirt.Network{}
			for _, network := range vmNetworks {
				networks[network.Name] = network
			}

			logger.V(1).Info("virtual machine data",
				"vmFullName", vmFullName,
				"virtualMachineInterfaces", vmInterfaces)

			for _, iface := range vmInterfaces {
				err := vmInterfacesFunc(vmFullName, iface, networks)
				if err != nil {
					return errors.Wrapf(err, "failed vm interface loop on vm %s", vmFullName)
				}
			}
		}
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "failed iterating over all cluster vms")
	}
	return nil
}

func (p *PoolManager) IsKubevirtEnabled() bool {
	return p.isKubevirt
}

func (p *PoolManager) isRelatedToKubevirt(pod *corev1.Pod) bool {
	if pod.ObjectMeta.OwnerReferences == nil {
		return false
	}

	for _, ref := range pod.OwnerReferences {
		if ref.Kind == kubevirt.VirtualMachineInstanceGroupVersionKind.Kind {
			requestUrl := fmt.Sprintf("apis/kubevirt.io/v1/namespaces/%s/virtualmachines/%s", pod.Namespace, ref.Name)
			log.V(1).Info("test", "requestURI", requestUrl)
			result := p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI(requestUrl).Do(context.TODO())

			data, err := result.Raw()
			log.V(1).Info("get kubevirt virtual machine object response", "err", err, "response", string(data))
			if err != nil && apierrors.IsNotFound(err) {
				log.V(1).Info("this pod is an ephemeral vmi object allocating mac as a regular pod")
				return false
			}

			return true
		}
	}

	return false
}

// Revert allocation if one of the requested mac addresses fails to be allocated
func (p *PoolManager) revertAllocationOnVm(vmName string, allocations map[string]string) {
	log.V(1).Info("Revert vm allocation", "vmName", vmName, "allocations", allocations)
	for _, macAddress := range allocations {
		delete(p.macPoolMap, macAddress)
	}
}

// Remove all the mac addresses from the waiting configmap this mean the vm was saved in the etcd and pass validations
func (p *PoolManager) MarkVMAsReady(vm *kubevirt.VirtualMachine, latestPersistedTransactionTimeStamp *time.Time, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("MarkVMAsReady")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	vmFullName := VmNamespaced(vm)

	vmMacMap, err := p.macPoolMap.filterInByInstanceName(vmFullName)
	if err != nil {
		return errors.Wrapf(err, "Failed to get VmMacMap for vm %s", vmFullName)
	}
	logger.Info("Macs currently set on vm", "vmMacMap", vmMacMap)

	err = vmMacMap.filterMacsThatRequireCommit(latestPersistedTransactionTimeStamp, logger)
	if err != nil {
		return errors.Wrapf(err, "Failed to filter macs that need commit on vm %s", vmFullName)
	}
	if len(*vmMacMap) != 0 {
		p.commitChangesToMacPoolMap(vmMacMap, vm, logger)
	}

	logger.V(1).Info("marked virtual machine as ready", "macPoolMap", p.macPoolMap)
	return nil
}

// This function check if there are virtual machines that hits the create
// mutating webhook but we didn't get the creation event in the controller loop
func (p *PoolManager) vmWaitingCleanupLook() {
	logger := log.WithName("vmWaitingCleanupLook")
	c := time.Tick(3 * time.Second)
	logger.Info("starting cleanup loop for waiting mac addresses")
	for _ = range c {
		p.healStaleMacEntries(logger)
	}
}

// healStaleMacEntries looks for stale mac entries, and once find one, heals it by comparing to the real state in the
// cluster: if the vm still there, and the mac attached to it) then it allocates it, otherwise removes from the macMap
func (p *PoolManager) healStaleMacEntries(parentLogger logr.Logger) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger := parentLogger.WithName("healStaleMacEntries")
	var macsToRemove []string
	macsToAlign := map[string]*kubevirt.VirtualMachine{}
	for macAddress, macEntry := range p.macPoolMap {
		isEntryStale, err := macEntry.hasExpiredTransaction(p.waitTime)
		if err == nil && isEntryStale {
			logger.Info("entry is stale", "macAddress", macAddress, "vmFullName", macEntry.instanceName, "interfaceName", macEntry.macInstanceKey, "stale TS", macEntry.transactionTimestamp)

			var vm *kubevirt.VirtualMachine
			var err error
			if macEntry.isDummyEntry() {
				vm, err = p.recoverVmFromCluster(macAddress)
			} else {
				vm, err = p.getvmInstance(macEntry.instanceName)
			}
			if err != nil {
				if apierrors.IsNotFound(err) {
					logger.Info("vm no longer exists. Removing mac from pool", "macAddress", macAddress, "entry", macEntry)
					macsToRemove = append(macsToRemove, macAddress)
					continue
				} else {
					return err
				}
			}
			macsToAlign[macAddress] = vm
		}
	}

	if len(macsToRemove) == 0 && len(macsToAlign) == 0 {
		return nil
	}
	logger.Info("macMap is self healing", "macsToRemove", macsToRemove, "macsToAlign", macsToAlign)
	for _, macAddress := range macsToRemove {
		p.macPoolMap.removeMacEntry(macAddress)
	}
	for macAddress, vm := range macsToAlign {
		p.macPoolMap.alignMacEntryAccordingToVmInterface(macAddress, VmNamespaced(vm), getVirtualMachineInterfaces(vm))
	}

	logger.V(1).Info("macMap is updated", "macPoolMap", p.macPoolMap)
	return nil
}

func (p *PoolManager) recoverVmFromCluster(macAddress string) (*kubevirt.VirtualMachine, error) {
	log.V(1).Info("recoverVmFromCluster", "macAddress", macAddress)
	foundVmName := ""
	err := p.forEachManagedVmInterfaceInClusterRunFunction(func(vmFullName string, iface kubevirt.Interface, networks map[string]kubevirt.Network) error {
		if !validateInterfaceSupported(iface, networks) {
			return nil
		}

		if iface.MacAddress != "" && iface.MacAddress == macAddress {
			foundVmName = vmFullName
		}
		return nil
	})

	if err != nil {
		return nil, errors.Wrap(err, "failed to search for vm that holds the mac address in cluster")
	}
	if foundVmName != "" {
		return p.getvmInstance(foundVmName)
	} else {
		return nil, apierrors.NewNotFound(schema.GroupResource{
			Group:    "kubevirt.io",
			Resource: "virtualmachine",
		}, foundVmName)
	}
}

func (p *PoolManager) getvmInstance(vmFullName string) (*kubevirt.VirtualMachine, error) {
	vmFullNameSplit := strings.Split(vmFullName, "/")
	vmNamespace := vmFullNameSplit[1]
	vmName := vmFullNameSplit[2]
	log.V(1).Info("getvmInstance", "vmNamespace", vmNamespace, "vmName", vmName)
	if vmNamespace == "" || vmName == "" {
		return nil, errors.New("failed to extract vm namespace and name")
	}

	requestUrl := fmt.Sprintf("apis/kubevirt.io/v1/namespaces/%s/virtualmachines/%s", vmNamespace, vmName)
	log.V(1).Info("getvmInstance", "requestURI", requestUrl)

	result := p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI(requestUrl).Do(context.TODO())
	if result.Error() != nil {
		return nil, result.Error()
	}
	vm := &kubevirt.VirtualMachine{}
	err := result.Into(vm)
	if err != nil {
		return nil, err
	}

	return vm, nil
}

func validateInterfaceSupported(iface kubevirt.Interface, networks map[string]kubevirt.Network) bool {
	if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
		log.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
		return false
	}
	return true
}

// IsVirtualMachineManaged checks if the namespace of a VirtualMachine instance is managed by kubemacpool
func (p *PoolManager) IsVirtualMachineManaged(namespaceName string) (bool, error) {
	return p.IsNamespaceManaged(namespaceName, virtualMachnesWebhookName)
}

func VmNamespaced(machine *kubevirt.VirtualMachine) string {
	return fmt.Sprintf("vm/%s/%s", machine.Namespace, machine.Name)
}

func VmNamespacedFromRequest(request *reconcile.Request) string {
	vm := &kubevirt.VirtualMachine{ObjectMeta: metav1.ObjectMeta{
		Name:      request.Name,
		Namespace: request.Namespace,
	}}
	return VmNamespaced(vm)
}

func IsVirtualMachineDeletionInProgress(vm *kubevirt.VirtualMachine) bool {
	return !vm.ObjectMeta.DeletionTimestamp.IsZero()
}
