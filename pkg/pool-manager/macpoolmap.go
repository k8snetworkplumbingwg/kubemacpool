package pool_manager

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	kubevirt "kubevirt.io/api/core/v1"
)

// clearMacTransactionFromMacEntry clears mac entry's transactionTimestamp, signalling that no further update is needed
func (m *macMap) clearMacTransactionFromMacEntry(macAddress string) {
	macEntry, exist := m.findByMacAddress(macAddress)
	if exist {
		(*m)[NewMacKey(macAddress)] = macEntry.resetTransaction()
	}
}

func (m macMap) findByMacAddress(macAddress string) ([]macEntry, bool) {
	entries, exist := m[NewMacKey(macAddress)]
	return entries, exist
}

func (m macMap) findByMacAddressAndInstanceName(macAddress, instanceFullName string) (macEntry, int, error) {
	if entries, exist := m.findByMacAddress(macAddress); exist {
		for i, entry := range entries {
			if entry.instanceName == instanceFullName {
				return entry, i, nil
			}
		}
		err := errors.New("updateMacTransactionTimestampForUpdatedMacs failed")
		log.Error(err, "mac address does not contain instance", "instanceFullName", instanceFullName, "macmap", m)
		return macEntry{}, -1, err
	} else {
		err := errors.New("updateMacTransactionTimestampForUpdatedMacs failed")
		log.Error(err, "mac address does not exist in macPoolMap", "instanceFullName", instanceFullName, "macmap", m)
		return macEntry{}, -1, err
	}
}

// removeMacEntry deletes a macEntry from macPoolMap
func (m *macMap) removeMacEntry(macAddress string) {
	delete(*m, NewMacKey(macAddress))
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
	newEntry := macEntry{
		instanceName:         instanceFullName,
		macInstanceKey:       macInstanceKey,
		transactionTimestamp: nil,
	}

	entries := (*m)[NewMacKey(macAddress)]
	// Check if entry for this VM+interface already exists
	for i, entry := range entries {
		if entry.instanceName == instanceFullName && entry.macInstanceKey == macInstanceKey {
			// Update existing entry (reset transaction timestamp)
			entries[i] = newEntry
			(*m)[NewMacKey(macAddress)] = entries
			return
		}
	}

	// No existing entry, append new one
	(*m)[NewMacKey(macAddress)] = append(entries, newEntry)
}

// updateMacTransactionTimestampForUpdatedMacs updates the macEntry with the current transactionTimestamp
func (m *macMap) updateMacTransactionTimestampForUpdatedMacs(instanceFullName string, transactionTimestamp *time.Time, macByInterfaceUpdated map[string]string) error {
	for _, macAddress := range macByInterfaceUpdated {
		entry, index, err := m.findByMacAddressAndInstanceName(macAddress, instanceFullName)
		if err != nil {
			return err
		}
		entries := (*m)[NewMacKey(macAddress)]
		entries[index] = entry.setTransaction(transactionTimestamp)
		(*m)[NewMacKey(macAddress)] = entries
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
		if NewMacKey(iface.MacAddress).String() == macAddress {
			if iface.Name == macEntry.macInstanceKey {
				logger.Info("marked mac as allocated", "macAddress", macAddress)
				m.clearMacTransactionFromMacEntry(macAddress)
				return
			}
		}
	}

	// if not match found, then it means that the mac was removed. also remove from macPoolMap
	logger.Info("released a mac from macMap", "macAddress", macAddress)
	m.removeMacEntry(macAddress)
}
