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
	"fmt"
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	kubevirt "kubevirt.io/kubevirt/pkg/api/v1"
)

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	log.V(1).Info("AllocateVirtualMachineMac: data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"vmmap", p.vmToMacPoolMap,
		"currentMac", p.currentMac.String())

	if len(virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
		log.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", virtualMachine)
		return nil
	}

	if len(virtualMachine.Spec.Template.Spec.Networks) == 0 {
		log.V(1).Info("no networks found for virtual machine, skipping mac allocation", "name", virtualMachine.Name,
			"namespace", virtualMachine.Namespace)
		return nil
	}

	networks := map[string]kubevirt.Network{}
	for _, network := range virtualMachine.Spec.Template.Spec.Networks {
		networks[network.Name] = network
	}

	log.V(1).Info("virtual machine data",
		"name", virtualMachine.Name,
		"namespace", virtualMachine.Namespace,
		"interfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)

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
			allocations[iface.Name] = iface.MacAddress
		}
	}

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	p.vmCreationWaiting[vmNamespaced(virtualMachine)] = 0
	return nil
}

func (p *PoolManager) ReleaseVirtualMachineMac(virtualMachineName string) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	log.V(1).Info("ReleaseVirtualMachineMac: data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"vmmap", p.vmToMacPoolMap,
		"currentMac", p.currentMac.String())

	macList, ok := p.vmToMacPoolMap[virtualMachineName]

	if !ok {
		log.Error(fmt.Errorf("not found"), "virtual machine not found in the map", "virtualMachineName", virtualMachineName)
		return nil
	}

	if macList == nil {
		log.Error(fmt.Errorf("list empty"), "failed to get mac address list")
		return nil
	}

	for _, macAddr := range macList {
		delete(p.macPoolMap, macAddr)
		log.Info("released mac from virtual machine", "mac", macAddr, "virtualMachineName", virtualMachineName)
	}

	delete(p.vmToMacPoolMap, virtualMachineName)
	delete(p.vmCreationWaiting, virtualMachineName)
	log.V(1).Info("removed virtual machine from vmToMacPoolMap", "virtualMachineName", virtualMachineName)
	return nil
}

func (p *PoolManager) UpdateMacAddressesForVirtualMachine(virtualMachine *kubevirt.VirtualMachine) error {
	p.poolMutex.Lock()

	log.V(1).Info("UpdateMacAddressesForVirtualMachine: data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"vmmap", p.vmToMacPoolMap,
		"currentMac", p.currentMac.String())

	existInterfacesMap, isVirtualMachineExist := p.vmToMacPoolMap[vmNamespaced(virtualMachine)]
	if !isVirtualMachineExist {
		p.poolMutex.Unlock()
		return p.AllocateVirtualMachineMac(virtualMachine)
	}

	defer p.poolMutex.Unlock()
	// This map is for revert if the allocation failed
	copyInterfacesMap := make(map[string]string)
	// This map is for deltas
	deltaInterfacesMap := make(map[string]string)
	for key, value := range existInterfacesMap {
		copyInterfacesMap[key] = value
		deltaInterfacesMap[key] = value
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
					p.vmToMacPoolMap[vmNamespaced(copyVM)] = copyInterfacesMap
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
					p.vmToMacPoolMap[vmNamespaced(copyVM)] = copyInterfacesMap
					return err
				}
				newAllocations[iface.Name] = iface.MacAddress
			} else {
				macAddr, err := p.allocateFromPoolForVirtualMachine(copyVM, iface)
				if err != nil {
					p.revertAllocationOnVm(vmNamespaced(copyVM), newAllocations)
					p.vmToMacPoolMap[vmNamespaced(copyVM)] = copyInterfacesMap
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
	p.MarkVMAsReady(vmNamespaced(virtualMachine))
	return nil
}

func (p *PoolManager) allocateFromPoolForVirtualMachine(virtualMachine *kubevirt.VirtualMachine, iface kubevirt.Interface) (string, error) {
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	p.macPoolMap[macAddr.String()] = AllocationStatusAllocated
	if p.vmToMacPoolMap[vmNamespaced(virtualMachine)] == nil {
		p.vmToMacPoolMap[vmNamespaced(virtualMachine)] = make(map[string]string)
	}
	p.vmToMacPoolMap[vmNamespaced(virtualMachine)][iface.Name] = macAddr.String()
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

	p.macPoolMap[requestedMac] = AllocationStatusAllocated
	if p.vmToMacPoolMap[vmNamespaced(virtualMachine)] == nil {
		p.vmToMacPoolMap[vmNamespaced(virtualMachine)] = make(map[string]string)
	}

	p.vmToMacPoolMap[vmNamespaced(virtualMachine)][iface.Name] = requestedMac
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

	result := p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI("apis/kubevirt.io/v1alpha3/virtualmachines").Do()
	if result.Error() != nil {
		return result.Error()
	}

	vms := &kubevirt.VirtualMachineList{}
	err := result.Into(vms)
	if err != nil {
		return err
	}

	for _, vm := range vms.Items {
		log.V(1).Info("InitMaps for virtual machine", "vmName", vm.Name, "vmNamespace", vm.Namespace)
		if len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
			log.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", vm)
			continue
		}

		if len(vm.Spec.Template.Spec.Networks) == 0 {
			log.V(1).Info("no networks found for virtual machine, skipping mac allocation", "name", vm.Name,
				"namespace", vm.Namespace)
			continue
		}

		networks := map[string]kubevirt.Network{}
		for _, network := range vm.Spec.Template.Spec.Networks {
			networks[network.Name] = network
		}

		log.V(1).Info("virtual machine data",
			"name", vm.Name,
			"namespace", vm.Namespace,
			"interfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)

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
						"namespace", vm.Namespace,
						"name", vm.Name,
						"mac", iface.MacAddress)
					continue
				}
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
			result := p.kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI(requestUrl).Do()

			data, err := result.Raw()
			log.V(1).Info("get kubevirt virtual machine object response", "err", err, "response", string(data))
			if err != nil && errors.IsNotFound(err) {
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
	// If the vm is in the vm creation waiting skip the revert
	// this vm will be clean in the waiting clean loop
	if _, isExist := p.vmCreationWaiting[vmName]; isExist {
		return
	}

	log.V(1).Info("Revert vm allocation", "vmName", vmName, "allocations", allocations)
	p.releaseMacAddressesFromInterfaceMap(allocations)
	delete(p.vmToMacPoolMap, vmName)
}

// This function remove the virtual machine from the waiting list
// this mean that we got an event about a virtual machine in vm controller loop
func (p *PoolManager) MarkVMAsReady(vmName string) {
	p.vmMutex.Lock()
	defer p.vmMutex.Unlock()

	delete(p.vmCreationWaiting, vmName)
}

// This function check if there are virtual machines that hits the create
// mutating webhook but we didn't get the creation event in the controller loop
// this mean the create was failed by some other mutating or validating webhook
// so we release the virtual machine
func (p *PoolManager) vmWaitingCleanupLook() {
	c := time.Tick(3 * time.Second)
	for _ = range c {
		p.vmMutex.Lock()

		for vmName, waitingLookCount := range p.vmCreationWaiting {
			if waitingLookCount < 2 {
				p.vmCreationWaiting[vmName] += 1
				continue
			}

			log.V(1).Info("found a non existing vm, start releasing", "vmName", vmName)
			err := p.ReleaseVirtualMachineMac(vmName)
			if err != nil {
				log.Error(err, "failed to release virtual machine mac addresses in cleanup look")
			}
			delete(p.vmCreationWaiting, vmName)
		}

		p.vmMutex.Unlock()
	}
}

func vmNamespaced(machine *kubevirt.VirtualMachine) string {
	return fmt.Sprintf("%s/%s", machine.Namespace, machine.Name)
}
