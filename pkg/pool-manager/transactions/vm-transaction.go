package transactions

import (
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	kubevirt "kubevirt.io/client-go/api/v1"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

const (
	TransactionTimestampAnnotation = "kubemacpoolTransactionTimestamp"
)

var log = logf.Log.WithName("vm-transactions")

type VmTransaction struct {
	transactionTimestamp string
	currentVm            *kubevirt.VirtualMachine
	requestVm            *kubevirt.VirtualMachine
	now                  func() time.Time
}

func (t *VmTransaction) Start(currentVm, requestVm *kubevirt.VirtualMachine, now func() time.Time) {
	t.transactionTimestamp = t.createTransactionTimestamp()
	t.currentVm = currentVm
	t.requestVm = requestVm
	t.now = now
}

func (t *VmTransaction) GetLastPersisted(requestVm *kubevirt.VirtualMachine) {
	t.requestVm = requestVm
	t.transactionTimestamp = t.currentVm.GetAnnotations()[TransactionTimestampAnnotation]
}

func (t *VmTransaction) ProcessCreateRequest(poolManager *pool_manager.PoolManager, parentLogger logr.Logger) error {
	poolManager.PoolMutex.Lock()
	defer poolManager.PoolMutex.Unlock()

	parentLogger.Info("processing create request", "transaction timestamp", t.transactionTimestamp)
	t.setTransactionTimestampAnnotationToVm()

	err := t.allocateVmMacs(poolManager, parentLogger)
	if err != nil {
		return errors.Wrap(err, "Failed to allocate mac to the vm object")
	}

	return t.addFinalizer(parentLogger)
}

func (t *VmTransaction) ProcessUpdateRequest(poolManager *pool_manager.PoolManager, parentLogger logr.Logger) error {
	parentLogger.Info("processing update request", "transaction timestamp", t.transactionTimestamp)
	t.setTransactionTimestampAnnotationToVm()

	if reflect.DeepEqual(utils.GetVirtualMachineInterfaces(t.requestVm), utils.GetVirtualMachineInterfaces(t.currentVm)) {
		return nil
	}

	return t.updateMacsForVm(poolManager, parentLogger)
}

func (t *VmTransaction) allocateVmMacs(poolManager *pool_manager.PoolManager, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("allocateVmMacs")

	requestVmInterfaces := utils.GetVirtualMachineInterfaces(t.requestVm)
	requestVmNetworks := utils.GetVirtualMachineNetworks(t.requestVm)
	vmFullName := utils.VmNamespaced(t.requestVm)
	if len(requestVmInterfaces) == 0 {
		logger.Info("no interfaces found for virtual machine, skipping mac allocation")
		return nil
	}

	if len(requestVmNetworks) == 0 {
		logger.Info("no networks found for virtual machine, skipping mac allocation")
		return nil
	}

	networks := map[string]kubevirt.Network{}
	for _, network := range requestVmNetworks {
		networks[network.Name] = network
	}

	logger.V(1).Info("data before update", "requestInterfaces", requestVmInterfaces)
	copyVM := t.requestVm.DeepCopy()
	newAllocations := map[string]string{}
	for idx, iface := range copyVM.Spec.Template.Spec.Domain.Devices.Interfaces {
		if iface.Masquerade == nil && iface.Slirp == nil && networks[iface.Name].Multus == nil {
			logger.Info("mac address can be set only for interface of type masquerade and slirp on the pod network")
			continue
		}

		if iface.MacAddress != "" {
			if err := poolManager.AllocateRequestedVmMac(vmFullName, iface, logger); err != nil {
				poolManager.RevertAllocationOnVm(vmFullName, newAllocations)
				return err
			}
			newAllocations[iface.Name] = iface.MacAddress
		} else {
			macAddr, err := poolManager.AllocateFromPoolForVm(vmFullName, iface, logger)
			if err != nil {
				poolManager.RevertAllocationOnVm(vmFullName, newAllocations)
				return err
			}
			copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
			newAllocations[iface.Name] = macAddr
		}
	}

	err := poolManager.UpdateMacTransactionTimestampForUpdatedMacs(vmFullName, t.transactionTimestamp, newAllocations)
	if err != nil {
		return err
	}
	t.requestVm.Spec.Template.Spec.Domain.Devices.Interfaces = utils.GetVirtualMachineInterfaces(copyVM)
	logger.Info("data after allocation", "Allocations", newAllocations, "updated vm Interfaces", utils.GetVirtualMachineInterfaces(t.requestVm))
	return nil
}

func (t *VmTransaction) updateMacsForVm(poolManager *pool_manager.PoolManager, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("updateMacsForVm")
	poolManager.PoolMutex.Lock()
	if t.currentVm == nil {
		poolManager.PoolMutex.Unlock()
		return t.allocateVmMacs(poolManager, logger)
	}
	defer poolManager.PoolMutex.Unlock()

	currentInterfaces := utils.GetVirtualMachineInterfaces(t.currentVm)
	requestInterfaces := utils.GetVirtualMachineInterfaces(t.requestVm)
	logger.V(1).Info("data before update", "currentInterfaces", currentInterfaces, "requestInterfaces", requestInterfaces)

	currentInterfacesMap := make(map[string]string)
	// This map is for deltas
	deltaInterfacesMap := make(map[string]string)
	for _, iface := range currentInterfaces {
		currentInterfacesMap[iface.Name] = iface.MacAddress
		deltaInterfacesMap[iface.Name] = iface.MacAddress
	}

	vmFullName := utils.VmNamespaced(t.requestVm)
	copyVM := t.requestVm.DeepCopy()
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
				err := poolManager.AllocateRequestedVmMac(vmFullName, requestIface, logger)
				if err != nil {
					poolManager.RevertAllocationOnVm(vmFullName, newAllocations)
					return err
				}
				releaseOldAllocations[requestIface.Name] = currentlyAllocatedMacAddress
				newAllocations[requestIface.Name] = requestIface.MacAddress
			}
			delete(deltaInterfacesMap, requestIface.Name)

		} else {
			if requestIface.MacAddress != "" {
				if err := poolManager.AllocateRequestedVmMac(vmFullName, requestIface, logger); err != nil {
					poolManager.RevertAllocationOnVm(vmFullName, newAllocations)
					return err
				}
				newAllocations[requestIface.Name] = requestIface.MacAddress
			} else {
				macAddr, err := poolManager.AllocateFromPoolForVm(vmFullName, requestIface, logger)
				if err != nil {
					poolManager.RevertAllocationOnVm(vmFullName, newAllocations)
					return err
				}
				copyVM.Spec.Template.Spec.Domain.Devices.Interfaces[idx].MacAddress = macAddr
				newAllocations[requestIface.Name] = macAddr
			}
		}
	}

	logger.Info("updating updated mac's transaction timestamp", "newAllocations", newAllocations, "deltaInterfacesMap", deltaInterfacesMap, "releaseOldAllocations", releaseOldAllocations)
	poolManager.UpdateMacTransactionTimestampForUpdatedMacs(vmFullName, t.transactionTimestamp, newAllocations)
	poolManager.UpdateMacTransactionTimestampForUpdatedMacs(vmFullName, t.transactionTimestamp, deltaInterfacesMap)
	poolManager.UpdateMacTransactionTimestampForUpdatedMacs(vmFullName, t.transactionTimestamp, releaseOldAllocations)

	t.requestVm.Spec.Template.Spec.Domain.Devices.Interfaces = utils.GetVirtualMachineInterfaces(copyVM)
	logger.Info("data after update", "updated interfaces", utils.GetVirtualMachineInterfaces(t.requestVm))
	return nil
}

func (t *VmTransaction) addFinalizer(parentLogger logr.Logger) error {
	if utils.ContainsString(t.requestVm.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
		return nil
	}

	t.requestVm.ObjectMeta.Finalizers = append(t.requestVm.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)
	parentLogger.Info("Finalizer was added to the VM instance")

	return nil
}

func (t *VmTransaction) CommitTransactionsToPool(poolManager *pool_manager.PoolManager, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("Commit")

	poolManager.PoolMutex.Lock()
	defer poolManager.PoolMutex.Unlock()
	vmFullName := utils.VmNamespaced(t.requestVm)
	latestPersistedTransactionTimeStamp := t.transactionTimestamp
	var updatedMacsList []string

	vmMacMap, err := poolManager.GetInstanceMacMap(vmFullName)
	if err != nil {
		return errors.Wrapf(err, "Failed to get VmMacMap for vm %s", vmFullName)
	}

	vmPersistedInterfaceList := utils.GetVirtualMachineInterfaces(t.requestVm)
	logger.V(1).Info("checking macMap Alignment", "vmMacMap", vmMacMap, "interfaces", vmPersistedInterfaceList, "latestPersistedTransactionTimeStamp", latestPersistedTransactionTimeStamp)

	for macAddress, macEntry := range vmMacMap {
		if poolManager.IsMacPendingTransaction(macAddress) {
			isMacConflict, err := poolManager.IsMacTransactionConflictWithVmTransaction(macAddress, latestPersistedTransactionTimeStamp)
			if err != nil {
				return errors.Wrapf(err, "Failed to check mac entry readiness")
			}
			if isMacConflict {
				// do not commit mac transaction to macPoolMap
				logger.V(1).Info("change for mac Address did not persist yet", "macAddress", macAddress)
				continue
			} else {
				logger.V(1).Info("macAddress ready for commit")
				poolManager.AlignMacEntryAccordingToVmInterface(macAddress, macEntry, vmPersistedInterfaceList)
				updatedMacsList = append(updatedMacsList, macAddress)
			}
		}
	}

	poolManager.RemoveCommitedMacsFromLegacyConfigMap(updatedMacsList)

	logger.Info("marked virtual machine as ready")

	return nil
}

func (t *VmTransaction) GetUpdatedVm() *kubevirt.VirtualMachine {
	return t.requestVm
}

func (t *VmTransaction) GetTransactionTimestamp() string {
	return t.transactionTimestamp
}

func (t *VmTransaction) IsNeeded() bool {
	if t.IsNotVirtualMachineMarkedForDeletion() {
		if t.isNewVmTimestampAnnotationNeeded() {
			return true
		}
	}
	return false
}

func (t *VmTransaction) setTransactionTimestampAnnotationToVm() {
	t.requestVm.Annotations[TransactionTimestampAnnotation] = t.transactionTimestamp
}

func (t *VmTransaction) IsNotVirtualMachineMarkedForDeletion() bool {
	return t.requestVm.ObjectMeta.DeletionTimestamp.IsZero()
}

// isNewVmTimestampAnnotationNeeded checks if the vm has significantly changed in this webhook update request.
func (t *VmTransaction) isNewVmTimestampAnnotationNeeded() bool {
	// if the vm is only being created.
	if t.currentVm == nil {
		return true
	}
	if !reflect.DeepEqual(t.requestVm.Spec, t.currentVm.Spec) {
		return true
	}
	return false
}

func (t *VmTransaction) createTransactionTimestamp() string {
	return t.now().Format(time.RFC3339Nano)
}
