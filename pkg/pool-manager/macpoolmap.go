package pool_manager

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	kubevirt "kubevirt.io/api/core/v1"
)

// clearMacTransactionFromMacEntry clears mac entry's transactionTimestamp, signalling that no further update is needed
func (m *macMap) clearMacTransactionFromMacEntry(macAddress, instanceFullName string) {
	entry, index, err := m.findByMacAddressAndInstanceName(macAddress, instanceFullName)
	if err != nil {
		log.Error(err, "clearMacTransactionFromMacEntry fail: entry by MAC and instanceName not found", "macAddress", macAddress, "instanceFullName", instanceFullName, "macmap", m)
		return
	}

	entries, _ := m.findByMacAddress(macAddress)
	entries[index] = entry.resetTransaction()
	(*m)[NewMacKey(macAddress)] = entries
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

// removeInstanceFromMac removes a specific instance's entry for a MAC from macPoolMap
// If no entries remain for the MAC, the MAC is deleted entirely
func (m *macMap) removeInstanceFromMac(macAddress, instanceFullName string) {
	_, index, err := m.findByMacAddressAndInstanceName(macAddress, instanceFullName)
	if err != nil {
		// Entry not found, nothing to remove
		return
	}

	entries, _ := m.findByMacAddress(macAddress)

	// Remove the entry at index
	entries = append(entries[:index], entries[index+1:]...)

	if len(entries) == 0 {
		delete(*m, NewMacKey(macAddress))
	} else {
		(*m)[NewMacKey(macAddress)] = entries
	}
}

// filterInByInstanceName creates a subset map from macPoolMap, holding only macs that belongs to a specific instance (pod/vm)
func (m macMap) filterInByInstanceName(instanceName string) (*macMap, error) {
	instanceMacMap := macMap{}

	for macAddress, entries := range m {
		for _, entry := range entries {
			if entry.instanceName == instanceName {
				instanceMacMap[macAddress] = append(instanceMacMap[macAddress], entry)
			}
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
	for macAddress, entries := range *m {
		var filteredEntries []macEntry
		for _, entry := range entries {
			if entry.hasPendingTransaction() {
				parentLogger.V(1).Info("macAddress params:", "interfaceName", entry.macInstanceKey, "transactionTimeStamp", entry.transactionTimestamp)
				if entry.hasReadyTransaction(latestPersistedTransactionTimeStamp) {
					parentLogger.V(1).Info("macAddress ready for update", "macAddress", macAddress, "instanceName", entry.instanceName)
					filteredEntries = append(filteredEntries, entry)
				} else {
					parentLogger.V(1).Info("change for mac Address did not persist yet", "macAddress", macAddress, "instanceName", entry.instanceName)
				}
			}
		}
		if len(filteredEntries) > 0 {
			filteredMap[macAddress] = filteredEntries
		}
	}
	*m = filteredMap
	return nil
}

// alignMacEntryAccordingToVmInterface compares the mac entry with the current vm yaml interface and aligns itself to it
func (m *macMap) alignMacEntryAccordingToVmInterface(macAddress, instanceFullName string, vmInterfaces []kubevirt.Interface) {
	logger := log.WithName("alignMacEntryAccordingToVmInterface")

	vmEntry, _, err := m.findByMacAddressAndInstanceName(macAddress, instanceFullName)
	if err != nil {
		// No entry for this VM, nothing to align
		return
	}

	// Check if the VM's current interfaces still contain this MAC with the same interface name
	for _, iface := range vmInterfaces {
		if NewMacKey(iface.MacAddress).String() == macAddress {
			if iface.Name == vmEntry.macInstanceKey {
				logger.Info("marked mac as allocated", "macAddress", macAddress)
				m.clearMacTransactionFromMacEntry(macAddress, instanceFullName)
				return
			}
		}
	}

	// No match found, the mac was removed from this VM
	logger.Info("released a mac from macMap", "macAddress", macAddress)
	m.removeInstanceFromMac(macAddress, instanceFullName)
}
