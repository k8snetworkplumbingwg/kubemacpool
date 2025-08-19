package pool_manager

import "time"

func (m macEntry) hasPendingTransaction() bool {
	return m.transactionTimestamp != nil
}

// hasReadyTransaction checks if the transaction TS recorded on the mac entry has passed the last
// timestamp annotation that persisted in the vm instance, signalling that we need to update this entry.
func (m macEntry) hasReadyTransaction(lastPersistentTime *time.Time) bool {
	if !m.hasPendingTransaction() {
		return false
	}
	return !m.transactionTimestamp.After(*lastPersistentTime)
}

func (m macEntry) resetTransaction() macEntry {
	return macEntry{
		instanceName:         m.instanceName,
		macInstanceKey:       m.macInstanceKey,
		transactionTimestamp: nil,
	}
}

func (m macEntry) setTransaction(timestamp *time.Time) macEntry {
	return macEntry{
		instanceName:         m.instanceName,
		macInstanceKey:       m.macInstanceKey,
		transactionTimestamp: timestamp,
	}
}

func (m macEntry) hasExpiredTransaction(waitTime int) (bool, error) {
	if m.hasPendingTransaction() {
		macTransactionExpiration := m.transactionTimestamp.Add(time.Duration(waitTime) * time.Second)
		if now().After(macTransactionExpiration) {
			return true, nil
		}
	}
	return false, nil
}
