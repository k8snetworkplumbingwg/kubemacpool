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
	"encoding/json"
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

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger := parentLogger.WithName("AllocateVirtualMachineMac")
	logger.Info("data before allocation", "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap)

	if len(virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
		logger.Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", virtualMachine)
		return nil
	}

	if len(virtualMachine.Spec.Template.Spec.Networks) == 0 {
		logger.Info("no networks found for virtual machine, skipping mac allocation",
			"virtualMachineName", virtualMachine.Name,
			"virtualMachineNamespace", virtualMachine.Namespace)
		return nil
	}

	networks := map[string]kubevirt.Network{}
	for _, network := range virtualMachine.Spec.Template.Spec.Networks {
		networks[network.Name] = network
	}

	logger.V(1).Info("virtual machine data", "virtualMachineInterfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)

	copyVM := virtualMachine.DeepCopy()
	macChanges := macChanges{allocations: map[string]string{}}
	for idx, iface := range copyVM.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
			logger.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
			continue
		}

		if iface.MacAddress != "" {
			if err := p.allocateRequestedVirtualMachineInterfaceMac(copyVM, iface, logger); err != nil {
				p.revertAllocationOnVm(vmNamespaced(copyVM), macChanges.allocations)
				return err
			}
			macChanges.allocations[iface.Name] = iface.MacAddress
		} else {
			macAddr, err := p.allocateFromPoolForVirtualMachine(copyVM, logger)
			if err != nil {
				p.revertAllocationOnVm(vmNamespaced(copyVM), macChanges.allocations)
				return err
			}
			copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
			macChanges.allocations[iface.Name] = macAddr
		}
	}

	err := p.AddMacToWaitingConfig(macChanges, vmNamespaced(copyVM), logger)
	err = p.updateVmToMacPoolMap(macChanges, vmNamespaced(copyVM))
	if err != nil {
		return err
	}

	logger.Info("data after allocation", "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap)
	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces

	return nil
}

func (p *PoolManager) updateVmToMacPoolMap(macChanges macChanges, vmNameNamespaced string) error {
	log.V(1).Info("updating VmToMacPoolMap", "allocations", macChanges.allocations, "releases", macChanges.releases)
	var vmToMacPoolMap map[string]string

	if macChanges.allocations != nil {
		for iface, macAddress := range macChanges.allocations {
			if vmToMacPoolMap = p.vmToMacPoolMap[vmNameNamespaced]; vmToMacPoolMap == nil {
				vmToMacPoolMap = map[string]string{}
				p.vmToMacPoolMap[vmNameNamespaced] = vmToMacPoolMap
			}
			vmToMacPoolMap[iface] = macAddress
		}
	}
	if macChanges.releases != nil {
		vmToMacPoolMap = p.vmToMacPoolMap[vmNameNamespaced]
		for iface, _ := range macChanges.releases {
			if vmToMacPoolMap != nil {
				delete(vmToMacPoolMap, iface)
			}
		}
		if vmToMacPoolMap != nil && len(vmToMacPoolMap) == 0 {
			delete(p.vmToMacPoolMap, vmNameNamespaced)
		}
	}
	return nil
}

// ReleaseVirtualMachineMacOnVmDelete release all macs from virtual machine due to vm deletion
func (p *PoolManager) ReleaseVirtualMachineMacOnVmDelete(vm *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("ReleaseVirtualMachineMacOnVmDelete")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger.V(1).Info("data",
		"macmap", p.macPoolMap,
		"vmToMacPoolMap", p.vmToMacPoolMap[vmNamespaced(vm)])

	for _, macAddress := range p.vmToMacPoolMap[vmNamespaced(vm)] {
		logger.V(1).Info("released mac from macPoolMap", "macAddress", macAddress)
		delete(p.macPoolMap, macAddress)
	}

	err := p.updateVmToMacPoolMap(macChanges{releases: p.vmToMacPoolMap[vmNamespaced(vm)]}, vmNamespaced(vm))
	if err != nil {
		return err
	}

	logger.Info("released macs in virtual machine", "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap[vmNamespaced(vm)])

	return nil
}

func (p *PoolManager) UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("UpdateMacAddressesForVirtualMachine")
	logger.Info("data before allocation", "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap)
	p.poolMutex.Lock()
	if previousVirtualMachine == nil {
		p.poolMutex.Unlock()
		return p.AllocateVirtualMachineMac(virtualMachine, logger)
	}

	defer p.poolMutex.Unlock()
	// This map is for revert if the allocation failed
	copyInterfacesMap := make(map[string]string)
	// This map is for deltas
	deltaInterfacesMap := make(map[string]string)
	for _, iface := range previousVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces {
		copyInterfacesMap[iface.Name] = iface.MacAddress
		deltaInterfacesMap[iface.Name] = iface.MacAddress
	}

	copyVM := virtualMachine.DeepCopy()
	newAllocations := map[string]string{}
	releaseOldAllocations := map[string]string{}
	for idx, iface := range virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces {
		allocatedMacAddress, ifaceExist := copyInterfacesMap[iface.Name]
		// The interface was configured before check if we need to update the mac or assign the existing one
		if ifaceExist {
			if iface.MacAddress == "" {
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = allocatedMacAddress
			} else if iface.MacAddress != allocatedMacAddress {
				// Specific mac address was requested
				err := p.allocateRequestedVirtualMachineInterfaceMac(copyVM, iface, logger)
				if err != nil {
					p.revertAllocationOnVm(vmNamespaced(copyVM), newAllocations)
					return err
				}
				releaseOldAllocations[iface.Name] = allocatedMacAddress
				newAllocations[iface.Name] = iface.MacAddress
			}
			delete(deltaInterfacesMap, iface.Name)

		} else {
			if iface.MacAddress != "" {
				if err := p.allocateRequestedVirtualMachineInterfaceMac(copyVM, iface, logger); err != nil {
					p.revertAllocationOnVm(vmNamespaced(copyVM), newAllocations)
					return err
				}
				newAllocations[iface.Name] = iface.MacAddress
			} else {
				macAddr, err := p.allocateFromPoolForVirtualMachine(copyVM, logger)
				if err != nil {
					p.revertAllocationOnVm(vmNamespaced(copyVM), newAllocations)
					return err
				}
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
				newAllocations[iface.Name] = macAddr
			}
		}
	}

	// Update only new macs in VmToMacPoolMap. we must do this in the webhook context to avoid allocation races.
	err := p.updateVmToMacPoolMap(macChanges{allocations: newAllocations}, vmNamespaced(copyVM))
	if err != nil {
		return err
	}

	// released macs are marked here for deletion in macmap, and will be deleted in the controller should the webhook chain is persisted.
	// Until then, the to-be released macs are kept in the VmToMacPoolMap.
	newReleases := mergeMaps([]map[string]string{deltaInterfacesMap, releaseOldAllocations})
	if len(newReleases) != 0 {
		// Mark macs for deletion, will be released in the controller reconcile
		logger.V(1).Info("macs marked for release",
			"delta interfaces", deltaInterfacesMap, "old interfaces", releaseOldAllocations)
		p.markMacAddressesForDeletion(newReleases)
	}

	// Add updated macs (both added and marked for removal) for WaitingConfig for monitoring
	err = p.AddMacToWaitingConfig(macChanges{allocations: newAllocations, releases: newReleases}, vmNamespaced(copyVM), logger)
	if err != nil {
		return err
	}

	logger.Info("data after allocation", "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap)
	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	return nil
}

func (p *PoolManager) allocateFromPoolForVirtualMachine(virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) (string, error) {
	logger := parentLogger.WithName("allocateFromPoolForVirtualMachine")
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	p.macPoolMap[macAddr.String()] = AllocationStatusWaitingForPod
	logger.V(1).Info("mac from pool was allocated for virtual machine",
		"allocatedMac", macAddr.String())
	return macAddr.String(), nil
}

func (p *PoolManager) allocateRequestedVirtualMachineInterfaceMac(virtualMachine *kubevirt.VirtualMachine, iface kubevirt.Interface, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("allocateRequestedVirtualMachineInterfaceMac")
	requestedMac := iface.MacAddress
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	if _, exist := p.macPoolMap[requestedMac]; exist {
		// check if mac already belongs to the vm
		if macFromVmMap, _ := p.vmToMacPoolMap[vmNamespaced(virtualMachine)][iface.Name]; macFromVmMap != requestedMac {
			err := fmt.Errorf("failed to allocate requested mac address")
			logger.Error(err, "mac address already allocated to another vm")
			return err
		}
	}

	p.macPoolMap[requestedMac] = AllocationStatusWaitingForPod
	logger.V(1).Info("requested mac was allocated for virtual machine",
		"requestedMap", requestedMac)

	return nil
}

func (p *PoolManager) initVirtualMachineMap() error {
	logger := log.WithName("initVirtualMachineMap")
	if !p.isKubevirt {
		return nil
	}

	var result = p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI("apis/kubevirt.io/v1alpha3/virtualmachines").Do(context.TODO())
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
		allocations := map[string]string{}
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
				allocations[iface.Name] = iface.MacAddress
			}
			p.updateVmToMacPoolMap(macChanges{allocations: allocations}, vmNamespaced(&vm))
		}
	}

	waitingMac, err := p.getOrCreateVmMacWaitMap()
	if err != nil {
		return err
	}

	for macAddressDashes, cmEntry := range waitingMac {
		var dataEntry configMapEntry
		err := json.Unmarshal([]byte(cmEntry), &dataEntry)
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal configmap Entry")
		}

		if dataEntry.MacStatus == AllocationStatusWaitingForPod {
			macAddress := strings.Replace(macAddressDashes, "-", ":", 5)
			if _, exist := p.macPoolMap[macAddress]; !exist {
				p.macPoolMap[macAddress] = AllocationStatusWaitingForPod
			}
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
			requestUrl := fmt.Sprintf("apis/kubevirt.io/v1alpha3/namespaces/%s/virtualmachines/%s", pod.Namespace, ref.Name)
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

func (p *PoolManager) markMacAddressesForDeletion(releases map[string]string) {
	for _, macAddress := range releases {
		if _, exist := p.macPoolMap[macAddress]; exist {
			p.macPoolMap[macAddress] = AllocationStatusWaitingForDeletion
		}
	}
}

// Revert allocation if one of the requested mac addresses fails to be allocated
func (p *PoolManager) revertAllocationOnVm(vmName string, allocations map[string]string) {
	log.V(1).Info("Revert vm allocation", "vmName", vmName, "allocations to revert", allocations)
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
func (p *PoolManager) AddMacToWaitingConfig(macChanges macChanges, namespacedVmName string, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("AddMacToWaitingConfig")
	logger.V(1).Info("populating vmiWaitingConfig", "allocations", macChanges.allocations, "releases", macChanges.releases)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh ConfigMaps instance
		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}

		if macChanges.allocations != nil {
			for iface, macAddress := range macChanges.allocations {
				logger.V(1).Info("add mac address addition command to waiting config", "macAddress", macAddress)
				macAddressDashes := strings.Replace(macAddress, ":", "-", 5)
				configMap.Data[macAddressDashes], err = p.createConfigMapEntry(namespacedVmName, iface, AllocationStatusWaitingForPod)
				if err != nil {
					return errors.Wrapf(err, "Failed to create configmap Entry mac %s addition", macAddressDashes)
				}
			}
		}

		if macChanges.releases != nil {
			for iface, macAddress := range macChanges.releases {
				logger.V(1).Info("add mac address removal command to waiting config", "macAddress", macAddress)
				macAddressDashes := strings.Replace(macAddress, ":", "-", 5)
				configMap.Data[macAddressDashes], err = p.createConfigMapEntry(namespacedVmName, iface, AllocationStatusWaitingForDeletion)
				if err != nil {
					return errors.Wrapf(err, "Failed to create configmap Entry for mac %s removal", macAddressDashes)
				}
			}
		}

		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})

		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to update manager's configmap with allocated macs waiting for approval")
	}

	logger.V(1).Info("Successfully updated manager's configmap with allocated macs waiting for change approval")

	return err
}

func (p *PoolManager) createConfigMapEntry(namespacedVmName, iface string, macStatus AllocationStatus) (string, error) {
	entry := configMapEntry{
		NamespacedVmName: namespacedVmName,
		Iface:            iface,
		MacStatus:        macStatus,
		TimeStamp:        p.now().Format(time.RFC3339),
	}

	jsonEntry, err := json.Marshal(entry)
	return string(jsonEntry), err
}

// Remove all the mac addresses from the waiting configmap this mean the vm was saved in the etcd and pass validations
func (p *PoolManager) MarkVMAsReady(vm *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("MarkVMAsReady")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	logger.Info("updating macpoolMap according to vmToMacPoolMap", "macmap", p.macPoolMap, "vm's vmToMacPoolMap", p.vmToMacPoolMap[vmNamespaced(vm)])
	releases := map[string]string{}
	var macsUpdated []string
	for iface, macAddress := range p.vmToMacPoolMap[vmNamespaced(vm)] {
		switch p.macPoolMap[macAddress] {
		case AllocationStatusWaitingForPod:
			{
				logger.V(1).Info("set vm's mac to status allocated", "macAddress", macAddress)
				p.macPoolMap[macAddress] = AllocationStatusAllocated
			}
		case AllocationStatusWaitingForDeletion:
			{
				logger.V(1).Info("removing mac from macPoolMap", "macAddress", macAddress)
				delete(p.macPoolMap, macAddress)
				releases[iface] = macAddress
			}
		default:
			continue
		}

		// remember for later update of configMap
		macsUpdated = append(macsUpdated, macAddress)
	}

	//update released macs after macPoolMap was updated in the controller context
	err := p.updateVmToMacPoolMap(macChanges{releases: releases}, vmNamespaced(vm))
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh ConfigMaps instance
		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			return errors.Wrap(err, "Failed to refresh manager's configmap instance")
		}

		logger.V(1).Info("updating configMap according to updated macs", "configMap.Data", configMap.Data, "macsUpdated", macsUpdated)
		for _, macAddress := range macsUpdated {
			macAddressDashes := strings.Replace(macAddress, ":", "-", 5)
			delete(configMap.Data, macAddressDashes)
		}

		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to update manager's configmap with approved allocated macs")
	}

	logger.Info("marked virtual machine as ready", "macmap", p.macPoolMap, "vm's vmToMacPoolMap", p.vmToMacPoolMap[vmNamespaced(vm)])

	return nil
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
		p.updateVmWaitingConfigMap(logger)
	}
}

func (p *PoolManager) updateVmWaitingConfigMap(logger logr.Logger) {
	p.poolMutex.Lock()

	var configMapUpdateNeeded bool
	vmtoMacChanges := map[string]macChanges{}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		configMapUpdateNeeded = false
		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "failed to get config map", "configMapName", names.WAITING_VMS_CONFIGMAP)
			return nil
		}

		if configMap.Data == nil {
			logger.V(1).Info("the configMap is empty", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
			return nil
		}

		logger.Info("data:", "configMapName", names.WAITING_VMS_CONFIGMAP, "configMap.Data", configMap.Data, "macPoolMap", p.macPoolMap)
		for macAddressDashes, cmEntry := range configMap.Data {
			var dataEntry configMapEntry
			var timeStamp time.Time
			if err := json.Unmarshal([]byte(cmEntry), &dataEntry); err == nil {
				err = p.handleConfigMapEntry(macAddressDashes, dataEntry, configMap.Data, vmtoMacChanges, &configMapUpdateNeeded, logger)
				if err != nil {
					logger.Error(err, "failed to handle new configmap Entry")
				}
			} else if timeStamp, err = time.Parse(time.RFC3339, cmEntry); err == nil {
				err = p.handleConfigMapEntryLegacy(macAddressDashes, timeStamp, configMap.Data, &configMapUpdateNeeded, logger)
				if err != nil {
					logger.Error(err, "failed to handle legacy configmap Entry")
				}
			} else {
				// TODO: remove the mac from the wait map??
				logger.Error(err, "failed to parse configmap Entry")
			}
		}

		if configMapUpdateNeeded {
			_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		}

		return err
	})

	if configMapUpdateNeeded {
		if err == nil {
			// if configMap was updated with no errors then update VmToMacPoolMap
			for namespacedVmName, macChanges := range vmtoMacChanges {
				p.updateVmToMacPoolMap(macChanges, namespacedVmName)
			}
			logger.Info("the configMap successfully updated", "configMapName", names.WAITING_VMS_CONFIGMAP, "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap)
		} else {
			logger.Info("the configMap failed to update. err: %w", "configMapName", names.WAITING_VMS_CONFIGMAP, "macmap", p.macPoolMap, "vmToMacPoolMap", p.vmToMacPoolMap, err)
		}
	}

	p.poolMutex.Unlock()
}

// handleConfigMapEntryLegacy handles removal of mac addresses to macs added to cache but not properly handled in the controller reconcile,
// suggesting that their webhook chain got rejected. Removal occurs if entry is not handled by reconciler after waitTime.
func (p *PoolManager) handleConfigMapEntryLegacy(macAddressDashes string, timeStamp time.Time, configMapData map[string]string, configMapUpdateNeeded *bool, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("handleConfigMapEntryLegacy")
	if p.now().After(timeStamp.Add(time.Duration(p.waitTime) * time.Second)) {
		*configMapUpdateNeeded = true
		delete(configMapData, macAddressDashes)
		macAddress := strings.Replace(macAddressDashes, "-", ":", 5)
		delete(p.macPoolMap, macAddress)
		logger.V(1).Info("released mac address in waiting loop", "macAddress", macAddress)
	}
	return nil
}

// handleConfigMapEntry handles restoration of mac addresses to macs added/removed to cache but not properly handled in the controller reconcile,
// suggesting that their webhook chain got rejected. restoration occurs if entry is not handled by reconciler after waitTime.
func (p *PoolManager) handleConfigMapEntry(macAddressDashes string, dataEntry configMapEntry, configMapData map[string]string, vmtoMacChanges map[string]macChanges, configMapUpdateNeeded *bool, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("handleConfigMapEntry")
	t, err := time.Parse(time.RFC3339, dataEntry.TimeStamp)
	if err != nil {
		// TODO: remove the mac from the wait map??
		return errors.Wrap(err, "failed to parse allocation time")
	}

	if p.now().After(t.Add(time.Duration(p.waitTime) * time.Second)) {
		*configMapUpdateNeeded = true
		delete(configMapData, macAddressDashes)
		macAddress := strings.Replace(macAddressDashes, "-", ":", 5)

		if _, exist := vmtoMacChanges[dataEntry.NamespacedVmName]; !exist {
			vmtoMacChanges[dataEntry.NamespacedVmName] = macChanges{allocations: map[string]string{}, releases: map[string]string{}}
		}
		if dataEntry.MacStatus == AllocationStatusWaitingForDeletion {
			p.macPoolMap[macAddress] = AllocationStatusRestored

			vmtoMacChanges[dataEntry.NamespacedVmName].allocations[dataEntry.Iface] = macAddress
			logger.V(1).Info("restored mac address in waiting loop", "macAddress", macAddress)
		} else {
			delete(p.macPoolMap, macAddress)
			vmtoMacChanges[dataEntry.NamespacedVmName].releases[dataEntry.Iface] = macAddress
			logger.V(1).Info("released mac address in waiting loop", "macAddress", macAddress)
		}
	}
	return nil
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

func vmNamespaced(machine *kubevirt.VirtualMachine) string {
	return fmt.Sprintf("%s/%s", machine.Namespace, machine.Name)
}

// mergeMaps merges between map[string]string maps.
// Note that this does a simple merge, assuming there are no conflicting
// key values in the input maps.
func mergeMaps(maps []map[string]string) map[string]string {
	mergedMap := map[string]string{}

	for _, mapObj := range maps {
		for key, value := range mapObj {
			mergedMap[key] = value
		}
	}

	return mergedMap
}
