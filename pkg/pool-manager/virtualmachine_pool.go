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

	corev1 "k8s.io/api/core/v1"
	kubevirt "kubevirt.io/kubevirt/pkg/api/v1"
)

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	p.setAsLeader()
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

	for idx, iface := range virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
			log.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
			continue
		}

		if iface.MacAddress != "" {
			if err := p.allocateRequestedVirtualMachineInterfaceMac(iface.MacAddress, virtualMachine); err != nil {
				return err
			}
		} else {
			macAddr, err := p.allocateFromPoolForVirtualMachine(virtualMachine)
			if err != nil {
				return err
			}
			virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
		}
	}
	return nil
}

func (p *PoolManager) ReleaseVirtualMachineMac(virtualMachineName string) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	p.setAsLeader()

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
	log.V(1).Info("removed virtual machine from vmToMacPoolMap", "virtualMachineName", virtualMachineName)
	return nil
}

func (p *PoolManager) allocateFromPoolForVirtualMachine(virtualMachine *kubevirt.VirtualMachine) (string, error) {
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	p.macPoolMap[macAddr.String()] = true
	if p.vmToMacPoolMap[vmNamespaced(virtualMachine)] == nil {
		p.vmToMacPoolMap[vmNamespaced(virtualMachine)] = []string{}
	}
	p.vmToMacPoolMap[vmNamespaced(virtualMachine)] = append(p.vmToMacPoolMap[vmNamespaced(virtualMachine)], macAddr.String())
	log.Info("mac from pool was allocated for virtual machine",
		"allocatedMac", macAddr.String(),
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace)
	return macAddr.String(), nil
}

func (p *PoolManager) allocateRequestedVirtualMachineInterfaceMac(requestedMac string, virtualMachine *kubevirt.VirtualMachine) error {
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	if _, exist := p.macPoolMap[requestedMac]; exist {
		err := fmt.Errorf("failed to allocate requested mac address")
		log.Error(err, "mac address already allocated")

		return err
	}

	p.macPoolMap[requestedMac] = true
	if p.vmToMacPoolMap[vmNamespaced(virtualMachine)] == nil {
		p.vmToMacPoolMap[vmNamespaced(virtualMachine)] = []string{}
	}

	p.vmToMacPoolMap[vmNamespaced(virtualMachine)] = append(p.vmToMacPoolMap[vmNamespaced(virtualMachine)], requestedMac)
	log.Info("requested mac was allocated for virtual machine",
		"requestedMap", requestedMac,
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace)

	return nil
}

func (p *PoolManager) IsKubevirtEnabled() bool {
	return p.isKubevirt
}

func isRelatedToKubevirt(pod *corev1.Pod) bool {
	if pod.ObjectMeta.OwnerReferences == nil {
		return false
	}

	for _, ref := range pod.OwnerReferences {
		if ref.Kind == kubevirt.VirtualMachineInstanceGroupVersionKind.Kind {
			return true
		}
	}

	return false
}

func vmNamespaced(machine *kubevirt.VirtualMachine) string {
	return fmt.Sprintf("%s/%s", machine.Namespace, machine.Name)
}
