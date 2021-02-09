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
	"k8s.io/client-go/util/retry"
	kubevirt "kubevirt.io/client-go/api/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine, transactionTimestamp string, parentLogger logr.Logger) error {
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

	err := p.AddMacToWaitingConfig(newAllocations, logger)
	if err != nil {
		return err
	}

	p.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, newAllocations)
	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	logger.Info("data after allocation", "Allocations", newAllocations, "updated vm Interfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)

	return nil
}

func (p *PoolManager) ReleaseAllMacsOnVirtualMachineDelete(vm *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("ReleaseAllMacsOnVirtualMachineDelete")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger.V(1).Info("data", "macmap", p.macPoolMap)
	vmFullName := VmNamespaced(vm)
	vmMacMap, err := p.getInstanceMacMap(vmFullName)
	if err != nil {
		return errors.Wrapf(err, "Failed to get VmMacMap for vm %s", vmFullName)
	}

	for macAddress := range vmMacMap {
		delete(p.macPoolMap, macAddress)
	}

	logger.Info("released macs in virtual machine", "macmap", p.macPoolMap)

	return nil
}

func (p *PoolManager) UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine, transactionTimestamp string, parentLogger logr.Logger) error {
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
	p.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, newAllocations)
	p.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, deltaInterfacesMap)
	p.updateMacTransactionTimestampForUpdatedMacs(vmFullName, transactionTimestamp, releaseOldAllocations)

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = getVirtualMachineInterfaces(copyVM)
	logger.Info("data after update", "macmap", p.macPoolMap, "updated interfaces", getVirtualMachineInterfaces(virtualMachine))
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

	p.createOrUpdateMacEntryInMacPoolMap(macAddr.String(), vmFullName, iface.Name)
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
			logger.Error(err, "mac address already allocated")

			return err
		}
	}

	p.createOrUpdateMacEntryInMacPoolMap(requestedMac, vmFullName, iface.Name)

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

	var result = p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI("apis/kubevirt.io/v1/virtualmachines").Do(context.TODO())
	if result.Error() != nil {
		return result.Error()
	}

	vms := &kubevirt.VirtualMachineList{}
	err := result.Into(vms)
	if err != nil {
		return err
	}

	for _, vm := range vms.Items {
		logger.V(1).Info("InitMaps for virtual machine",
			"virtualMachineName", vm.Name,
			"virtualMachineNamespace", vm.Namespace)
		if len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
			logger.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", vm)
			continue
		}

		if len(vm.Spec.Template.Spec.Networks) == 0 {
			logger.V(1).Info("no networks found for virtual machine, skipping mac allocation",
				"virtualMachineName", vm.Name,
				"virtualMachineNamespace", vm.Namespace)
			continue
		}

		networks := map[string]kubevirt.Network{}
		for _, network := range vm.Spec.Template.Spec.Networks {
			networks[network.Name] = network
		}

		logger.V(1).Info("virtual machine data",
			"virtualMachineName", vm.Name,
			"virtualMachineNamespace", vm.Namespace,
			"virtualMachineInterfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)

		for _, iface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
			if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
				logger.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
				continue
			}

			if iface.MacAddress != "" {
				if err := p.allocateRequestedVirtualMachineInterfaceMac(&vm, iface, logger); err != nil {
					// Dont return an error here if we can't allocate a mac for a configured vm
					logger.Error(fmt.Errorf("failed to parse mac address for virtual machine"),
						"Invalid mac address for virtual machine",
						"virtualMachineNamespace", vm.Namespace,
						"virtualMachineName", vm.Name,
						"virtualMachineInterfaceMac", iface.MacAddress)
					continue
				}

				p.macPoolMap[iface.MacAddress] = AllocationStatusAllocated
			}
		}
	}

	waitingMac, err := p.getOrCreateVmMacWaitMap()
	if err != nil {
		return err
	}

	for macAddress := range waitingMac {
		macAddress = strings.Replace(macAddress, "-", ":", 5)

		if _, exist := p.macPoolMap[macAddress]; !exist {
			p.macPoolMap[macAddress] = AllocationStatusWaitingForPod
		}
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

// This function return or creates a config map that contains mac address and the allocation time.
func (p *PoolManager) getOrCreateVmMacWaitMap() (map[string]string, error) {
	configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = p.kubeClient.CoreV1().
				ConfigMaps(p.managerNamespace).
				Create(context.TODO(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: names.WAITING_VMS_CONFIGMAP, Namespace: p.managerNamespace}}, metav1.CreateOptions{})

			return map[string]string{}, nil
		}

		return nil, err
	}

	return configMap.Data, nil
}

// Add all the allocated mac addresses to the waiting config map with the current time.
func (p *PoolManager) AddMacToWaitingConfig(allocations map[string]string, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("AddMacToWaitingConfig")

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh ConfigMaps instance
		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}

		for _, macAddress := range allocations {
			logger.V(1).Info("add mac address to waiting config", "macAddress", macAddress)
			macAddress = strings.Replace(macAddress, ":", "-", 5)
			configMap.Data[macAddress] = time.Now().Format(time.RFC3339)
		}

		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})

		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to update manager's configmap with allocated macs waiting for approval")
	}

	logger.V(1).Info("Successfully updated manager's configmap with allocated macs waiting for approval")

	return err
}

// Remove all the mac addresses from the waiting configmap this mean the vm was saved in the etcd and pass validations
func (p *PoolManager) MarkVMAsReady(vm *kubevirt.VirtualMachine, latestPersistedTransactionTimeStamp string, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("MarkVMAsReady")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	vmFullName := VmNamespaced(vm)
	var updatedMacsList []string

	vmMacMap, err := p.getInstanceMacMap(vmFullName)
	if err != nil {
		return errors.Wrapf(err, "Failed to get VmMacMap for vm %s", vmFullName)
	}

	vmPersistedInterfaceList := getVirtualMachineInterfaces(vm)
	logger.V(1).Info("checking macMap Alignment", "vmMacMap", vmMacMap, "interfaces", vmPersistedInterfaceList, "latestPersistedTransactionTimeStamp", latestPersistedTransactionTimeStamp)
	for macAddress, macEntry := range vmMacMap {
		logger.V(1).Info("macAddress params:", "interfaceName", macEntry.macInstanceKey, "transactionTimeStamp", macEntry.transactionTimestamp)
		if p.isMacUpdateRequired(macAddress) {
			macReadyForUpdate, err := p.isMacReadyForTransactionUpdate(macAddress, latestPersistedTransactionTimeStamp)
			if err != nil {
				return errors.Wrapf(err, "Failed to check mac entry readiness")
			}
			if macReadyForUpdate {
				logger.V(1).Info("macAddress ready for update")
				p.alignMacEntryAccordingToVmInterface(macAddress, macEntry, vmPersistedInterfaceList)
				updatedMacsList = append(updatedMacsList, macAddress)
			} else {
				logger.V(1).Info("change for mac Address did not persist yet", "macAddress", macAddress)
			}
		}
	}

	logger.Info("marked virtual machine as ready", "macPoolMap", p.macPoolMap)

	return nil
}

func (p *PoolManager) alignMacEntryAccordingToVmInterface(macAddress string, macEntry macEntry, vmInterfaces []kubevirt.Interface) {
	for _, iface := range vmInterfaces {
		if iface.Name == macEntry.macInstanceKey && iface.MacAddress == macAddress {
			log.V(1).Info("alignMacEntryAccordingToVmInterface marked mac as allocated", "macAddress", macAddress)
			p.clearMacTransactionFromMacEntry(macAddress)
			return
		}
	}

	// if not match found, then it means that the mac was removed. also remove from macPoolMap
	log.V(1).Info("alignMacEntryAccordingToVmInterface released a mac from macMap", "macAddress", macAddress)
	p.removeMacEntry(macAddress)
}

// This function check if there are virtual machines that hits the create
// mutating webhook but we didn't get the creation event in the controller loop
// this mean the create was failed by some other mutating or validating webhook
// so we release the virtual machine
func (p *PoolManager) vmWaitingCleanupLook() {
	logger := log.WithName("vmWaitingCleanupLook")
	c := time.Tick(3 * time.Second)
	logger.Info("starting cleanup loop for waiting mac addresses")
	for _ = range c {
		p.poolMutex.Lock()

		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "failed to get config map", "configMapName", names.WAITING_VMS_CONFIGMAP)
			p.poolMutex.Unlock()
			continue
		}

		configMapUpdateNeeded := false
		if configMap.Data == nil {
			logger.V(1).Info("the configMap is empty", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
			p.poolMutex.Unlock()
			continue
		}

		for macAddress, allocationTime := range configMap.Data {
			t, err := time.Parse(time.RFC3339, allocationTime)
			if err != nil {
				// TODO: remove the mac from the wait map??
				logger.Error(err, "failed to parse allocation time")
				continue
			}

			logger.Info("data:", "configMapName", names.WAITING_VMS_CONFIGMAP, "configMap.Data", configMap.Data, "macPoolMap", p.macPoolMap)

			if time.Now().After(t.Add(time.Duration(p.waitTime) * time.Second)) {
				configMapUpdateNeeded = true
				delete(configMap.Data, macAddress)
				macAddress = strings.Replace(macAddress, "-", ":", 5)
				delete(p.macPoolMap, macAddress)
				logger.Info("released mac address in waiting loop", "macAddress", macAddress)
			}
		}

		if configMapUpdateNeeded {
			_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		}

		if err == nil {
			logger.Info("the configMap successfully updated", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
		} else {
			logger.Info("the configMap failed to update", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
		}

		p.poolMutex.Unlock()
	}
}

func SetTransactionTimestampAnnotationToVm(virtualMachine *kubevirt.VirtualMachine, transactionTimestamp string) {
	virtualMachine.Annotations[transactionTimestampAnnotation] = transactionTimestamp
}

func GetTransactionTimestampAnnotationFromVm(virtualMachine *kubevirt.VirtualMachine) string {
	return virtualMachine.GetAnnotations()[transactionTimestampAnnotation]
}

func GetTransactionTimestampAnnotation(virtualMachine *kubevirt.VirtualMachine) map[string]string {
	return map[string]string{transactionTimestampAnnotation: GetTransactionTimestampAnnotationFromVm(virtualMachine)}
}

// Checks if the namespace of the vm instance is managed by kubemacpool in terms of opt-mode
func (p *PoolManager) IsNamespaceManaged(namespaceName string) (bool, error) {
	mutatingWebhookConfigName := "kubemacpool-mutator"
	webhookName := "mutatevirtualmachines.kubemacpool.io"
	vmOptMode, err := p.getOptMode(mutatingWebhookConfigName, webhookName)
	if err != nil {
		return false, errors.Wrap(err, "failed to get opt-Mode")
	}

	isNamespaceManaged, err := p.isNamespaceSelectorCompatibleWithOptModeLabel(namespaceName, mutatingWebhookConfigName, webhookName, vmOptMode)
	if err != nil {
		return false, errors.Wrap(err, "failed to check if namespace is managed according to opt-mode")
	}

	log.V(1).Info("IsNamespaceManaged", "vmOptMode", vmOptMode, "namespaceName", namespaceName, "is namespace in the game", isNamespaceManaged)
	return isNamespaceManaged, nil
}

func IsVirtualMachineNotMarkedForDeletion(vm *kubevirt.VirtualMachine) bool {
	return vm.ObjectMeta.DeletionTimestamp.IsZero()
}

func VmNamespaced(machine *kubevirt.VirtualMachine) string {
	return fmt.Sprintf("vm/%s/%s", machine.Namespace, machine.Name)
}
