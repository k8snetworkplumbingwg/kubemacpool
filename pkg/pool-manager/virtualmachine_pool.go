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
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

const tempVmName = "tempVmName"
const tempVmInterface = "tempInterfaceName"

func (p *PoolManager) ReleaseAllMacsOnVirtualMachineDelete(vmFullName string, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("ReleaseAllMacsOnVirtualMachineDelete")

	p.PoolMutex.Lock()
	defer p.PoolMutex.Unlock()
	logger.V(1).Info("data", "macmap", p.macPoolMap)
	vmMacMap, err := p.GetInstanceMacMap(vmFullName)
	if err != nil {
		return errors.Wrapf(err, "Failed to get VmMacMap for vm %s", vmFullName)
	}

	for macAddress := range vmMacMap {
		delete(p.macPoolMap, macAddress)
	}

	logger.Info("released macs in virtual machine", "macmap", p.macPoolMap)

	return nil
}

func (p *PoolManager) AllocateFromPoolForVm(vmFullName string, iface kubevirt.Interface, parentLogger logr.Logger) (string, error) {
	logger := parentLogger.WithName("AllocateFromPoolForVm")
	logger.Info("pool before allocation", "macPoolMap", p.macPoolMap)
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	p.createOrUpdateMacEntryInMacPoolMap(macAddr.String(), vmFullName, iface.Name)
	logger.Info("pool after allocation", "macPoolMap", p.macPoolMap)
	return macAddr.String(), nil
}

func (p *PoolManager) AllocateRequestedVmMac(vmFullName string, iface kubevirt.Interface, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("AllocateRequestedVmMac")
	logger.Info("pool before allocation", "macPoolMap", p.macPoolMap)
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

	logger.Info("pool after allocation", "macPoolMap", p.macPoolMap)
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

	err := p.forEachVmInterfaceInClusterRunFunction(func(vmFullName string, iface kubevirt.Interface) {
		if iface.MacAddress != "" {
			if err := p.AllocateRequestedVmMac(vmFullName, iface, logger); err != nil {
				// Dont return an error here if we can't allocate a mac for a configured vm
				logger.Error(fmt.Errorf("failed to parse mac address for virtual machine"),
					"Invalid mac address for virtual machine",
					"virtualMachineFullName", vmFullName,
					"virtualMachineInterfaceMac", iface.MacAddress)
			}
		}
	})
	if err != nil {
		return err
	}

	err = p.initMacMapFromLegacyConfigMap()
	if err != nil {
		return err
	}
	return nil
}

// forEachMacInClusterRunFunction gets all the macs from all the supported interfaces in all the cluster vms, and runs
// a function f on it
func (p *PoolManager) forEachVmInterfaceInClusterRunFunction(f func(vmFullName string, iface kubevirt.Interface)) error {
	logger := log.WithName("forEachVmInterfaceInClusterRunFunction")
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
		vmFullName := utils.VmNamespaced(&vm)
		vmInterfaces := utils.GetVirtualMachineInterfaces(&vm)
		vmNetworks := utils.GetVirtualMachineNetworks(&vm)
		logger.V(1).Info("InitMaps for virtual machine")
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
			if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
				logger.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
				continue
			}

			f(vmFullName, iface)
		}
	}
	return nil
}

// the legacy configMap is no longer used in KMP, but after upgrade we might find macs in there.
// This can be from either 3 scenarios:
// 1. the webhook that allocated this mac failed
// 2. the webhook succeeded but vm didn't persist yet
// 3. the webhook succeeded but configMap didn't remove it yet.
// initMacMapFromLegacyConfigMap handles these cases by comparing the the current macPoolMap
// and acting accordingly to prevent collisions.
func (p *PoolManager) initMacMapFromLegacyConfigMap() error {
	waitingMacData, err := p.getOrCreateVmMacWaitMap()
	if err != nil {
		return err
	}

	if len(waitingMacData) == 0 {
		return nil
	}
	notYetUpdatedMacMap, alreadyUpdatedMacList, err := p.splitLegacyConfigMapMacsByExistenceInMacPoolMap(waitingMacData)
	if err != nil {
		return err
	}
	p.RemoveCommitedMacsFromLegacyConfigMap(alreadyUpdatedMacList)
	p.addMacToMacPoolWithDummyFieldsToPreventDuplications(notYetUpdatedMacMap)

	return nil
}

// splitLegacyConfigMapMacsByExistenceInMacPoolMap goes over the KMP configMap data splits it to 2 sets according
// to whether they are already updated in the macPoolMap.
func (p *PoolManager) splitLegacyConfigMapMacsByExistenceInMacPoolMap(waitingMacData map[string]string) (map[string]string, []string, error) {
	notYetUpdatedMacMap := map[string]string{}
	alreadyUpdatedMacList := []string{}

	for macAddressDashes, transactionTimestamp := range waitingMacData {
		macAddress := strings.Replace(macAddressDashes, "-", ":", 5)
		if _, exist := p.macPoolMap[macAddress]; !exist {
			notYetUpdatedMacMap[macAddress] = transactionTimestamp
		} else {
			alreadyUpdatedMacList = append(alreadyUpdatedMacList, macAddress)
		}
	}
	log.Info("splitLegacyConfigMapMacsByExistenceInMacPoolMap", "notYetUpdatedMacMap", notYetUpdatedMacMap, "alreadyUpdatedMacList", alreadyUpdatedMacList)
	return notYetUpdatedMacMap, alreadyUpdatedMacList, nil
}

// addMacToMacPoolWithDummyFieldsToPreventDuplications adds not yet persisted mac addresses to macMap, to protect the mac
// from future duplication by another vm trying to set it.
func (p *PoolManager) addMacToMacPoolWithDummyFieldsToPreventDuplications(notPersistedMacList map[string]string) {
	// configMap timestamps are in RFC3339 format, but it is also applicable in the new RFC3339Nano format
	for macAddress, transactionTimestamp := range notPersistedMacList {
		p.recreateLegacyMacEntryInMacPoolMap(macAddress, tempVmName, tempVmInterface, transactionTimestamp)
	}
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
func (p *PoolManager) RevertAllocationOnVm(vmName string, allocations map[string]string) {
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

func (p *PoolManager) RemoveCommitedMacsFromLegacyConfigMap(updatedMacList []string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh ConfigMaps instance
		configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
		if err != nil {
			return errors.Wrap(err, "Failed to refresh manager's configmap instance")
		}

		if configMap.Data == nil {
			return nil
		}

		log.Info("the configMap is not empty", "data", configMap.Data)

		for _, macAddress := range updatedMacList {
			macAddressDashes := strings.Replace(macAddress, ":", "-", 5)
			if _, exists := configMap.Data[macAddressDashes]; exists {
				delete(configMap.Data, macAddressDashes)
			}
		}

		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})

		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to update manager's legacy configmap with approved allocated macs")
	} else {
		log.Info("the configMap successfully updated")
	}
	return nil
}

func (p *PoolManager) AlignMacEntryAccordingToVmInterface(macAddress string, macEntry macEntry, vmInterfaces []kubevirt.Interface) {
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
		p.PoolMutex.Lock()

		p.handleStaleLegacyConfigMapEntries(logger)

		p.handleStaleMacEntries(logger)

		p.PoolMutex.Unlock()
	}
}

func (p *PoolManager) handleStaleMacEntries(parentLogger logr.Logger) error {
	logger := parentLogger.WithName("handleStaleMacEntries")
	var macMapChanged bool
	for macAddress, macEntry := range p.macPoolMap {
		macMapChanged = false
		if !p.IsMacPendingTransaction(macAddress) {
			continue
		}
		isEntryStale, err := p.isMacTransactionStale(macAddress)
		if err == nil && isEntryStale {
			macMapChanged = true
			logger.Info("entry is stale", "macAddress", macAddress, "vmFullName", macEntry.instanceName, "interfaceName", macEntry.macInstanceKey, "stale TS", macEntry.transactionTimestamp)
			vm, err := p.getVmInstance(macEntry.instanceName)
			if err != nil && apierrors.IsNotFound(err) {
				logger.Info("vm no longer exists or does not have Template. removing mac from pool", "macAddress", macAddress, "entry", macEntry)
				p.removeMacEntry(macAddress)
			} else if err == nil {
				p.AlignMacEntryAccordingToVmInterface(macAddress, macEntry, utils.GetVirtualMachineInterfaces(vm))
			} else {
				return err
			}
		}
	}
	if macMapChanged {
		logger.V(1).Info("macMap is updated", "macPoolMap", p.macPoolMap)
	}
	return nil
}

func (p *PoolManager) getVmInstance(vmFullName string) (*kubevirt.VirtualMachine, error) {
	vmFullNameSplit := strings.Split(vmFullName, "/")
	vmNamespace := vmFullNameSplit[1]
	vmName := vmFullNameSplit[2]
	log.V(1).Info("getVmInstance", "vmNamespace", vmNamespace, "vmName", vmName)
	if vmNamespace == "" || vmName == "" {
		return nil, errors.New("failed to extract vm namespace and name")
	}

	requestUrl := fmt.Sprintf("apis/kubevirt.io/v1/namespaces/%s/virtualmachines/%s", vmNamespace, vmName)
	log.V(1).Info("getVmInstance", "requestURI", requestUrl)

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

func (p *PoolManager) handleStaleLegacyConfigMapEntries(parentLogger logr.Logger) error {
	logger := parentLogger.WithName("handleStaleLegacyConfigMapEntries")
	configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "failed to get config map", "configMapName", names.WAITING_VMS_CONFIGMAP)
		return err
	}

	configMapUpdateNeeded := false
	if configMap.Data == nil {
		return nil
	}

	for macAddressDashes, allocationTime := range configMap.Data {
		// legacy Configmap uses time.RFC3339.
		t, err := time.Parse(time.RFC3339, allocationTime)
		if err != nil {
			// TODO: remove the mac from the wait map??
			logger.Error(err, "failed to parse allocation time")
			continue
		}

		logger.Info("data:", "configMapName", names.WAITING_VMS_CONFIGMAP, "configMap.Data", configMap.Data, "macPoolMap", p.macPoolMap)

		if time.Now().After(t.Add(time.Duration(p.waitTime) * time.Second)) {
			configMapUpdateNeeded = true
			delete(configMap.Data, macAddressDashes)
			macAddress := strings.Replace(macAddressDashes, "-", ":", 5)
			// try to find vm that matches mac and allocate it in macPoolMap
			err := p.forEachVmInterfaceInClusterRunFunction(func(vmFullName string, iface kubevirt.Interface) {
				if iface.MacAddress != "" && iface.MacAddress == macAddress {
					p.createOrUpdateMacEntryInMacPoolMap(macAddress, vmFullName, iface.Name)
				}
			})
			if err != nil {
				return err
			}

			// if vm was not found, release from macMap
			if macEntry, exist := p.macPoolMap[macAddress]; exist && macEntry.instanceName == tempVmName {
				p.removeMacEntry(macAddress)
			}
		}
	}

	if configMapUpdateNeeded {
		_, err = p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Update(context.TODO(), configMap, metav1.UpdateOptions{})

		if err == nil {
			logger.Info("the configMap successfully updated", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
		} else {
			logger.Info("the configMap failed to update", "configMapName", names.WAITING_VMS_CONFIGMAP, "macPoolMap", p.macPoolMap)
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
