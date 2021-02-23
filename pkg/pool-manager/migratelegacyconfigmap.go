package pool_manager

import (
	"context"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const (
	tempVmName      = "tempVmName"
	tempVmInterface = "tempInterfaceName"
)

// the legacy configMap is no longer used in KMP, but after upgrade we might find macs in there.
// This can be from either 2 scenarios:
// 1. the webhook that allocated this mac failed
// 2. the webhook succeeded but configMap didn't remove it yet.
// initMacMapFromLegacyConfigMap migrates missing entries to macPoolMap to prevent collisions, and then
// deletes the no longer needed configMap.
func (p *PoolManager) initMacMapFromLegacyConfigMap() error {
	waitingMacData, err := p.getVmMacWaitMap()
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("legacy configMap does not exist")
			return nil
		}
		return err
	}

	err = p.migrateMacsFromConfigMap(waitingMacData)
	if err != nil {
		return err
	}
	p.deleteVmMacWaitConfigMap()

	return nil
}

// migrateMacsFromConfigMap goes over the KMP configMap data and migrates the ones that are not yet
// in the macPoolMap to it to prevent future collisions.
func (p *PoolManager) migrateMacsFromConfigMap(waitingMacData map[string]string) error {
	if len(waitingMacData) == 0 {
		log.V(1).Info("legacy configMap data is empty")
		return nil
	}
	var recreatedMacs []string
	for macAddressDashes, transactionTimestampString := range waitingMacData {
		macAddress := strings.Replace(macAddressDashes, "-", ":", 5)
		if _, exist := p.macPoolMap.findByMacAddress(macAddress); !exist {
			// configMap timestamps are in RFC3339 format, but it is also applicable in the new RFC3339Nano format
			transactionTimestamp, err := parseTransactionTimestamp(transactionTimestampString)
			if err != nil {
				log.Error(err, "failed to parse legacy mac entry", "macAddress", macAddress, "ts", transactionTimestampString)
				continue
			}
			p.macPoolMap.createOrUpdateDummyEntryWithTimestamp(macAddress, &transactionTimestamp)
			recreatedMacs = append(recreatedMacs, macAddress)
		}
	}
	log.Info("migrateMacsFromConfigMap", "recreatedMacs", recreatedMacs)
	return nil
}

// createOrUpdateDummyEntryWithTimestamp adds/updates a Dummy entry in the macPollMap. Since the transaction timestamp,
// is migrated we also copy the timestamp, to signal that the transaction is still pending.
func (m *macMap) createOrUpdateDummyEntryWithTimestamp(macAddress string, timestamp *time.Time) {
	(*m)[macAddress] = macEntry{
		instanceName:         tempVmName,
		macInstanceKey:       tempVmInterface,
		transactionTimestamp: timestamp,
	}
}

// getVmMacWaitMap return a config map that contains mac address and the allocation time.
func (p *PoolManager) getVmMacWaitMap() (map[string]string, error) {
	configMap, err := p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return configMap.Data, nil
}

func (p *PoolManager) deleteVmMacWaitConfigMap() error {
	return p.kubeClient.CoreV1().ConfigMaps(p.managerNamespace).Delete(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.DeleteOptions{})
}
