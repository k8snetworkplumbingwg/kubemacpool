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

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	log.V(1).Info("AllocateVirtualMachineMac: data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"currentMac", p.currentMac.String())

	if len(virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
		log.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", virtualMachine)
		return nil
	}

	if len(virtualMachine.Spec.Template.Spec.Networks) == 0 {
		log.V(1).Info("no networks found for virtual machine, skipping mac allocation",
			"virtualMachineName", virtualMachine.Name,
			"virtualMachineNamespace", virtualMachine.Namespace)
		return nil
	}

	networks := map[string]kubevirt.Network{}
	for _, network := range virtualMachine.Spec.Template.Spec.Networks {
		networks[network.Name] = network
	}

	log.V(1).Info("virtual machine data",
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace,
		"virtualMachineInterfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)

	copyVM := virtualMachine.DeepCopy()
	allocations := map[string]string{}
	for idx, iface := range copyVM.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
			log.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
			continue
		}

		if iface.MacAddress != "" {
			if err := p.allocateRequestedVirtualMachineInterfaceMac(copyVM, iface); err != nil {
				p.revertAllocationOnVm(vmNamespaced(copyVM), allocations)
				return err
			}
			allocations[iface.Name] = iface.MacAddress
		} else {
			macAddr, err := p.allocateFromPoolForVirtualMachine(copyVM, iface)
			if err != nil {
				p.revertAllocationOnVm(vmNamespaced(copyVM), allocations)
				return err
			}
			copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
			allocations[iface.Name] = macAddr
		}
	}

	err := p.AddMacToWaitingConfig(allocations)
	if err != nil {
		return err
	}

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces

	return nil
}

func (p *PoolManager) ReleaseVirtualMachineMac(vm *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("ReleaseVirtualMachineMac")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger.V(1).Info("data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"currentMac", p.currentMac.String())

	if len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
		logger.V(1).Info("no interfaces found for virtual machine, skipping mac release")
		return nil
	}

	logger.V(1).Info("virtual machine data", "interfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)
	for _, iface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.MacAddress != "" {
			delete(p.macPoolMap, iface.MacAddress)
			logger.Info("released mac from virtual machine",
				"mac", iface.MacAddress)
		}
	}

	logger.V(1).Info("released macs in virtua machine", "macmap", p.macPoolMap)

	return nil
}

func (p *PoolManager) UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine) error {
	p.poolMutex.Lock()
	log.V(1).Info("UpdateMacAddressesForVirtualMachine: data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"currentMac", p.currentMac.String())
	if previousVirtualMachine == nil {
		p.poolMutex.Unlock()
		return p.AllocateVirtualMachineMac(virtualMachine)
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
				err := p.allocateRequestedVirtualMachineInterfaceMac(copyVM, iface)
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
				if err := p.allocateRequestedVirtualMachineInterfaceMac(copyVM, iface); err != nil {
					p.revertAllocationOnVm(vmNamespaced(copyVM), newAllocations)
					return err
				}
				newAllocations[iface.Name] = iface.MacAddress
			} else {
				macAddr, err := p.allocateFromPoolForVirtualMachine(copyVM, iface)
				if err != nil {
					p.revertAllocationOnVm(vmNamespaced(copyVM), newAllocations)
					return err
				}
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
				newAllocations[iface.Name] = iface.MacAddress
			}
		}
	}

	// Release delta interfaces
	log.V(1).Info("UpdateMacAddressesForVirtualMachine: delta interfaces to release",
		"interfaces Map", deltaInterfacesMap)
	p.releaseMacAddressesFromInterfaceMap(deltaInterfacesMap)

	// Release old allocations
	log.V(1).Info("UpdateMacAddressesForVirtualMachine: old interfaces to release",
		"interfaces Map", releaseOldAllocations)
	p.releaseMacAddressesFromInterfaceMap(releaseOldAllocations)

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	return nil
}

func (p *PoolManager) allocateFromPoolForVirtualMachine(virtualMachine *kubevirt.VirtualMachine, iface kubevirt.Interface) (string, error) {
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	p.macPoolMap[macAddr.String()] = AllocationStatusWaitingForPod
	log.Info("mac from pool was allocated for virtual machine",
		"allocatedMac", macAddr.String(),
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace)
	return macAddr.String(), nil
}

func (p *PoolManager) allocateRequestedVirtualMachineInterfaceMac(virtualMachine *kubevirt.VirtualMachine, iface kubevirt.Interface) error {
	requestedMac := iface.MacAddress
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	if _, exist := p.macPoolMap[requestedMac]; exist {
		err := fmt.Errorf("failed to allocate requested mac address")
		log.Error(err, "mac address already allocated")

		return err
	}

	p.macPoolMap[requestedMac] = AllocationStatusWaitingForPod
	log.Info("requested mac was allocated for virtual machine",
		"requestedMap", requestedMac,
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace)

	return nil
}

func (p *PoolManager) initVirtualMachineMap() error {
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
		log.V(1).Info("InitMaps for virtual machine",
			"virtualMachineName", vm.Name,
			"virtualMachineNamespace", vm.Namespace)
		if len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
			log.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", vm)
			continue
		}

		if len(vm.Spec.Template.Spec.Networks) == 0 {
			log.V(1).Info("no networks found for virtual machine, skipping mac allocation",
				"virtualMachineName", vm.Name,
				"virtualMachineNamespace", vm.Namespace)
			continue
		}

		networks := map[string]kubevirt.Network{}
		for _, network := range vm.Spec.Template.Spec.Networks {
			networks[network.Name] = network
		}

		log.V(1).Info("virtual machine data",
			"virtualMachineName", vm.Name,
			"virtualMachineNamespace", vm.Namespace,
			"virtualMachineInterfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)

		for _, iface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
			if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
				log.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
				continue
			}

			if iface.MacAddress != "" {
				if err := p.allocateRequestedVirtualMachineInterfaceMac(&vm, iface); err != nil {
					// Dont return an error here if we can't allocate a mac for a configured vm
					log.Error(fmt.Errorf("failed to parse mac address for virtual machine"),
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

func (p *PoolManager) releaseMacAddressesFromInterfaceMap(allocations map[string]string) {
	for _, value := range allocations {
		delete(p.macPoolMap, value)
	}
}

// Revert allocation if one of the requested mac addresses fails to be allocated
func (p *PoolManager) revertAllocationOnVm(vmName string, allocations map[string]string) {
	log.V(1).Info("Revert vm allocation", "vmName", vmName, "allocations", allocations)
	p.releaseMacAddressesFromInterfaceMap(allocations)
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
func (p *PoolManager) AddMacToWaitingConfig(allocations map[string]string) error {
	logger := log.WithName("AddMacToWaitingConfig").WithValues("macPoolMap", p.macPoolMap, "allocations", allocations)

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

	logger.V(1).Info("Successfuly updated manager's configmap with allocated macs waiting for approval")

	return err
}

// Remove all the mac addresses from the waiting configmap this mean the vm was saved in the etcd and pass validations
func (p *PoolManager) MarkVMAsReady(vm *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("MarkVMAsReady")

	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh ConfigMaps instance
		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			return errors.Wrap(err, "Failed to refresh manager's configmap instance")
		}

		if configMap.Data == nil {
			logger.Info("the configMap is empty")
			return nil
		}

		if len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
			logger.Info("interface list is empty")
			return nil
		}

		logger.V(1).Info("set vm's mac to status allocated", "vm interfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)
		for _, vmInterface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
			if vmInterface.MacAddress != "" {
				if _, exist := p.macPoolMap[vmInterface.MacAddress]; exist {
					p.macPoolMap[vmInterface.MacAddress] = AllocationStatusAllocated
				}
				macAddress := strings.Replace(vmInterface.MacAddress, ":", "-", 5)
				delete(configMap.Data, macAddress)
			}
		}
		logger.V(1).Info("set virtual machine's macs as ready", "macPoolMap", p.macPoolMap)

		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})

		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to update manager's configmap with approved allocated macs")
	}

	logger.V(1).Info("marked virtual machine as ready")

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
		p.poolMutex.Lock()

		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "failed to get config map", "configMapName", names.WAITING_VMS_CONFIGMAP)
			p.poolMutex.Unlock()
			continue
		}

		configMapUpdateNeeded := false
		if configMap.Data == nil {
			logger.Info("the configMap is empty", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
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
				logger.V(1).Info("released mac address in waiting loop", "macAddress", macAddress)
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

// Checks if the namespace of a vm instance is opted in for kubemacpool
func (p *PoolManager) IsVmInstanceOptedIn(namespaceName string) (bool, error) {
	return p.isInstanceOptedIn(namespaceName, "kubemacpool-mutator", "mutatevirtualmachines.kubemacpool.io")
}

func vmNamespaced(machine *kubevirt.VirtualMachine) string {
	return fmt.Sprintf("%s/%s", machine.Namespace, machine.Name)
}
