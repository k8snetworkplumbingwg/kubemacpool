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

func (p *PoolManager) AllocateVirtualMachineMac(virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()
	logger := parentLogger.WithName("AllocateVirtualMachineMac")

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

	macMap, err := p.getClusterMacs()
	if err != nil {
		return err
	}

	copyVM := virtualMachine.DeepCopy()
	allocations := map[string]string{}
	for idx, iface := range copyVM.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
			logger.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
			continue
		}

		if iface.MacAddress != "" {
			if err := p.allocateRequestedVirtualMachineInterfaceMac(macMap, vmNamespaced(virtualMachine), iface, logger); err != nil {
				return err
			}
			allocations[iface.Name] = iface.MacAddress
		} else {
			macAddr, err := p.allocateFromPoolForVirtualMachine(macMap, vmNamespaced(virtualMachine), iface, logger)
			if err != nil {
				return err
			}
			copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
			allocations[iface.Name] = macAddr
		}
	}

	err = p.AddMacToWaitingConfig(allocations, logger)
	if err != nil {
		return err
	}

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	logger.Info("data after allocation", "interfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)

	return nil
}

func (p *PoolManager) UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("UpdateMacAddressesForVirtualMachine")
	p.poolMutex.Lock()
	if previousVirtualMachine == nil {
		p.poolMutex.Unlock()
		return p.AllocateVirtualMachineMac(virtualMachine, logger)
	}

	defer p.poolMutex.Unlock()
	macMap, err := p.getClusterMacs()
	if err != nil {
		return err
	}

	vmNamespacedName := vmNamespaced(virtualMachine)
	// This map is for revert if the allocation failed
	copyInterfacesMap := make(map[string]string)
	for _, iface := range previousVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces {
		copyInterfacesMap[iface.Name] = iface.MacAddress
	}

	copyVM := virtualMachine.DeepCopy()
	newAllocations := map[string]string{}
	for idx, iface := range virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces {
		allocatedMacAddress, ifaceExist := copyInterfacesMap[iface.Name]
		// The interface was configured before check if we need to update the mac or assign the existing one
		if ifaceExist {
			if iface.MacAddress == "" {
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = allocatedMacAddress
			} else if iface.MacAddress != allocatedMacAddress {
				// Specific mac address was requested
				err := p.allocateRequestedVirtualMachineInterfaceMac(macMap, vmNamespacedName, iface, logger)
				if err != nil {
					return err
				}
				newAllocations[iface.Name] = iface.MacAddress
			}

		} else {
			if iface.MacAddress != "" {
				if err := p.allocateRequestedVirtualMachineInterfaceMac(macMap, vmNamespacedName, iface, logger); err != nil {
					return err
				}
				newAllocations[iface.Name] = iface.MacAddress
			} else {
				macAddr, err := p.allocateFromPoolForVirtualMachine(macMap, vmNamespacedName, iface, logger)
				if err != nil {
					return err
				}
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
				newAllocations[iface.Name] = iface.MacAddress
			}
		}
	}

	err = p.AddMacToWaitingConfig(newAllocations, logger)
	if err != nil {
		return err
	}

	virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces = copyVM.Spec.Template.Spec.Domain.Devices.Interfaces
	logger.Info("data after allocation", "Interfaces", virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)
	return nil
}

func (p *PoolManager) allocateFromPoolForVirtualMachine(macMap map[string]string, vmNamespacedName string, iface kubevirt.Interface, parentLogger logr.Logger) (string, error) {
	logger := parentLogger.WithName("allocateFromPoolForVirtualMachine")

	macAddr, err := p.getFreeMac(macMap)
	if err != nil {
		return "", err
	}

	macMap[macAddr.String()] = namedInterface(vmNamespacedName, iface.Name)
	logger.V(1).Info("mac from pool was allocated for virtual machine",
		"allocatedMac", macAddr.String())
	return macAddr.String(), nil
}

func (p *PoolManager) allocateRequestedVirtualMachineInterfaceMac(macMap map[string]string, vmNamespacedName string, iface kubevirt.Interface, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("allocateRequestedVirtualMachineInterfaceMac")
	requestedMac := iface.MacAddress
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	occupied, err := p.isMacOccupiedInConfigMap(requestedMac)
	if err != nil {
		return err
	}

	if occupied {
		err := fmt.Errorf("failed to allocate requested mac address")
		logger.Error(err, "mac address already taken in configmap")
		return err
	}
	if instanceName, exist := macMap[requestedMac]; exist {
		if namedInterface(vmNamespacedName, iface.Name) != instanceName {
			err := fmt.Errorf("failed to allocate requested mac address")
			logger.Error(err, "mac address already allocated to instance", "instance", instanceName)

			return err
		}
	}

	macMap[requestedMac] = namedInterface(vmNamespacedName, iface.Name)
	logger.V(1).Info("mac from pool was allocated for virtual machine",
		"allocatedMac", requestedMac)

	return nil
}

func (p *PoolManager) updateVMMacsToMap(macMap map[string]string) error {
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
		vmNamespacedName := vmNamespaced(&vm)
		if len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 0 {
			log.V(1).Info("no interfaces found for virtual machine, skipping mac allocation", "virtualMachine", vmNamespacedName)
			continue
		}

		if len(vm.Spec.Template.Spec.Networks) == 0 {
			log.V(1).Info("no networks found for virtual machine, skipping mac allocation", "virtualMachine", vmNamespacedName)
			continue
		}

		networks := map[string]kubevirt.Network{}
		for _, network := range vm.Spec.Template.Spec.Networks {
			networks[network.Name] = network
		}

		log.V(2).Info("virtual machine data",
			"virtualMachine", vmNamespacedName,
			"virtualMachineInterfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)

		for _, iface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
			if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
				log.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
				continue
			}

			if iface.MacAddress != "" {
				log.V(1).Info("Adding mac", "mac", iface.MacAddress, "vmNamespacedName", vmNamespacedName, "iface.Name", iface.Name)
				macMap[iface.MacAddress] = namedInterface(vmNamespacedName, iface.Name)
			}
		}
	}

	return nil
}

func (p *PoolManager) initVirtualMachineMap() error {
	if !p.isKubevirt {
		return nil
	}

	_, err := p.getOrCreateVmMacWaitMap()
	if err != nil {
		return err
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

// Add all the allocated mac addresses to the waiting config map with the current time.
func (p *PoolManager) isMacOccupiedInConfigMap(macAddress string) (bool, error) {
	configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		return false, err
	}

	if configMap.Data == nil {
		return false, nil
	}

	macAddressDashes := strings.Replace(macAddress, ":", "-", 5)
	if _, exist := configMap.Data[macAddressDashes]; exist {
		return true, nil
	}

	return false, nil
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

		logger.V(1).Info("remove allocated macs from configmap", "vm interfaces", vm.Spec.Template.Spec.Domain.Devices.Interfaces)
		for _, vmInterface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
			if vmInterface.MacAddress != "" {
				macAddressDashes := strings.Replace(vmInterface.MacAddress, ":", "-", 5)
				delete(configMap.Data, macAddressDashes)
			}
		}
		logger.V(1).Info("set virtual machine's macs as ready")

		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})

		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to update manager's configmap with approved allocated macs")
	}

	logger.Info("marked virtual machine as ready")

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
			logger.V(1).Info("the configMap is empty", "configMapName", names.WAITING_VMS_CONFIGMAP)
			p.poolMutex.Unlock()
			continue
		}

		for macAddressDashes, allocationTime := range configMap.Data {
			t, err := time.Parse(time.RFC3339, allocationTime)
			if err != nil {
				// TODO: remove the mac from the wait map??
				logger.Error(err, "failed to parse allocation time")
				continue
			}

			logger.Info("data:", "configMapName", names.WAITING_VMS_CONFIGMAP, "configMap.Data", configMap.Data)

			if time.Now().After(t.Add(time.Duration(p.waitTime) * time.Second)) {
				configMapUpdateNeeded = true
				delete(configMap.Data, macAddressDashes)
				logger.Info("released mac address in waiting loop", "macAddress", macAddressDashes)
			}
		}

		if configMapUpdateNeeded {
			_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		}

		if err == nil {
			logger.Info("the configMap successfully updated", "configMapName", names.WAITING_VMS_CONFIGMAP)
		} else {
			logger.Info("the configMap failed to update", "configMapName", names.WAITING_VMS_CONFIGMAP)
		}

		p.poolMutex.Unlock()
	}
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
	return fmt.Sprintf("vm/%s/%s", machine.Namespace, machine.Name)
}
