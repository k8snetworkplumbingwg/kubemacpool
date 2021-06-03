package pool_manager

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	kubevirt "kubevirt.io/client-go/api/v1"
)

// clearMacTransactionFromMacEntry clears mac entry's transactionTimestamp, signalling that no further update is needed
func (m *macMap) clearMacTransactionFromMacEntry(macAddress string) {
	macEntry, exist := m.findByMacAddress(macAddress)
	if exist {
		(*m)[macAddress] = macEntry.resetTransaction()
	}
}

func (m macMap) findByMacAddress(macAddress string) (macEntry, bool) {
	macEntry, exist := m[macAddress]
	return macEntry, exist
}

func (m macMap) findByMacAddressAndInstanceName(macAddress, instanceFullName string) (macEntry, error) {
	if entry, exist := m.findByMacAddress(macAddress); exist {
		if entry.instanceName == instanceFullName {
			return entry, nil
		} else {
			err := errors.New("updateMacTransactionTimestampForUpdatedMacs failed")
			log.Error(err, "mac address does not belong to instance", "instanceFullName", instanceFullName, "macmap", m)
			return macEntry{}, err
		}
	} else {
		err := errors.New("updateMacTransactionTimestampForUpdatedMacs failed")
		log.Error(err, "mac address does not exist in macPoolMap", "instanceFullName", instanceFullName, "macmap", m)
		return macEntry{}, err
	}
}

// removeMacEntry deletes a macEntry from macPoolMap
func (m *macMap) removeMacEntry(macAddress string) {
	delete(*m, macAddress)
}

// filterInByInstanceName creates a subset map from macPoolMap, holding only macs that belongs to a specific instance (pod/vm)
func (m macMap) filterInByInstanceName(instanceName string) (*macMap, error) {
	instanceMacMap := macMap{}

	for macAddress, macEntry := range m {
		if macEntry.instanceName == instanceName {
			instanceMacMap[macAddress] = macEntry
		}
	}
	return &instanceMacMap, nil
}

func (m *macMap) createOrUpdateEntry(macAddress, instanceFullName, macInstanceKey string) {
	(*m)[macAddress] = macEntry{
		instanceName:         instanceFullName,
		macInstanceKey:       macInstanceKey,
		transactionTimestamp: nil,
	}
}

// updateMacTransactionTimestampForUpdatedMacs updates the macEntry with the current transactionTimestamp
func (m *macMap) updateMacTransactionTimestampForUpdatedMacs(instanceFullName string, transactionTimestamp *time.Time, macByInterfaceUpdated map[string]string) error {
	for _, macAddress := range macByInterfaceUpdated {
		entry, err := m.findByMacAddressAndInstanceName(macAddress, instanceFullName)
		if err != nil {
			return err
		}
		(*m)[macAddress] = entry.setTransaction(transactionTimestamp)
	}
	return nil
}

func (m *macMap) filterMacsThatRequireCommit(latestPersistedTransactionTimeStamp *time.Time, parentLogger logr.Logger) error {
	filteredMap := macMap{}
	parentLogger.V(1).Info("checking macMap Alignment", "macMap", *m, "latestPersistedTransactionTimeStamp", latestPersistedTransactionTimeStamp)
	for macAddress, macEntry := range *m {
		if macEntry.hasPendingTransaction() {
			parentLogger.V(1).Info("macAddress params:", "interfaceName", macEntry.macInstanceKey, "transactionTimeStamp", macEntry.transactionTimestamp)
			if macEntry.hasReadyTransaction(latestPersistedTransactionTimeStamp) {
				parentLogger.V(1).Info("macAddress ready for update", "macAddress", macAddress)
				filteredMap[macAddress] = macEntry
			} else {
				parentLogger.V(1).Info("change for mac Address did not persist yet", "macAddress", macAddress)
			}
		}
	}
	*m = filteredMap
	return nil
}

// alignMacEntryAccordingToVmInterface compares the mac entry with the current vm yaml interface and aligns itself to it
func (m *macMap) alignMacEntryAccordingToVmInterface(macAddress, instanceFullName string, vmInterfaces []kubevirt.Interface) {
	logger := log.WithName("alignMacEntryAccordingToVmInterface")
	macEntry, _ := m.findByMacAddress(macAddress)
	for _, iface := range vmInterfaces {
		if iface.MacAddress == macAddress {
			if iface.Name == macEntry.macInstanceKey {
				logger.Info("marked mac as allocated", "macAddress", macAddress)
				m.clearMacTransactionFromMacEntry(macAddress)
				return
			} else if macEntry.isDummyEntry() {
				logger.Info("Dummy entry released a mac from macMap", "macAddress", macAddress)
				m.removeMacEntry(macAddress)
				m.createOrUpdateEntry(macAddress, instanceFullName, iface.Name)
			}
		}
	}

	// if not match found, then it means that the mac was removed. also remove from macPoolMap
	logger.Info("released a mac from macMap", "macAddress", macAddress)
	m.removeMacEntry(macAddress)
}
