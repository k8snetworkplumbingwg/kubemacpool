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
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

const (
	RangeStartEnv                  = "RANGE_START"
	RangeEndEnv                    = "RANGE_END"
	RuntimeObjectFinalizerName     = "k8s.v1.cni.cncf.io/kubeMacPool"
	TransactionTimestampAnnotation = "kubemacpool.io/transaction-timestamp"
	mutatingWebhookConfigName      = "kubemacpool-mutator"
	virtualMachnesWebhookName      = "mutatevirtualmachines.kubemacpool.io"
	podsWebhookName                = "mutatepods.kubemacpool.io"
)

var log = logf.Log.WithName("PoolManager")

// now is an artifact to do some unit testing without waiting for expiration time.
var now = func() time.Time { return time.Now() }

type PoolManager struct {
	cachedKubeClient client.Client
	kubeClient       client.Client
	rangeStart       net.HardwareAddr // fist mac in range
	rangeEnd         net.HardwareAddr // last mac in range
	currentMac       net.HardwareAddr // last given mac
	managerNamespace string
	macPoolMap       macMap                       // allocated mac map and macEntry
	podToMacPoolMap  map[string]map[string]string // map allocated mac address by networkname and namespace/podname: {"namespace/podname: {"network name": "mac address"}}
	poolMutex        sync.Mutex                   // mutex for allocation an release
	rangeMutex       sync.RWMutex                 // mutex for range operations to support dynamic updates
	isKubevirt       bool                         // bool if kubevirt virtualmachine crd exist in the cluster
	waitTime         int                          // Duration in second to free macs of allocated vms that failed to start.
	isPoolReady      atomic.Bool                  // indicates whether the pool manager has completed initialization
}

type OptMode string

const (
	OptInMode  OptMode = "Opt-in"
	OptOutMode OptMode = "Opt-out"
)

var ErrFull = errors.New("the range is full")

type macEntry struct {
	instanceName         string
	macInstanceKey       string // for vms, it holds the interface Name, for pods, it holds the network Name
	transactionTimestamp *time.Time
}

type macMap map[macKey]macEntry

func (m macMap) MarshalJSON() ([]byte, error) {
	mm := make(map[string]macEntry, len(m))
	for k, v := range m {
		mm[k.String()] = v
	}

	return json.Marshal(mm)
}

func NewPoolManager(kubeClient, cachedKubeClient client.Client, rangeStart, rangeEnd net.HardwareAddr, managerNamespace string, kubevirtExist bool, waitTime int) (*PoolManager, error) {
	// Create PoolManager struct with empty ranges initially
	poolManger := &PoolManager{
		cachedKubeClient: cachedKubeClient,
		kubeClient:       kubeClient,
		isKubevirt:       kubevirtExist,
		managerNamespace: managerNamespace,
		podToMacPoolMap:  map[string]map[string]string{},
		macPoolMap:       macMap{},
		poolMutex:        sync.Mutex{},
		rangeMutex:       sync.RWMutex{},
		waitTime:         waitTime,
	}

	// Use UpdateRanges to set initial ranges with proper validation
	if err := poolManger.UpdateRanges(rangeStart, rangeEnd); err != nil {
		return nil, fmt.Errorf("failed to set initial ranges: %w", err)
	}

	log.Info("PoolManager initialized with MAC ranges",
		"rangeStart", rangeStart.String(),
		"rangeEnd", rangeEnd.String(),
		"currentMac", poolManger.getCurrentMAC())

	return poolManger, nil
}

func (p *PoolManager) Start() error {
	err := p.InitMaps()
	if err != nil {
		return errors.Wrap(err, "failed Init pool manager maps")
	}

	if p.isKubevirt {
		go p.vmWaitingCleanupLook()
	}

	log.Info("Pool Manager is ready")
	p.isPoolReady.Store(true)

	return nil
}

// IsReady returns true if the pool manager has completed initialization
func (p *PoolManager) IsReady() bool {
	return p.isPoolReady.Load()
}

// getManagedNamespaces pre-computes which namespaces are managed by kubemacpool for a specific webhook
func (p *PoolManager) getManagedNamespaces(webhookName string) ([]string, error) {
	log.V(1).Info("computing managed namespaces for initialization", "webhookName", webhookName)

	namespaces := &v1.NamespaceList{}
	err := p.kubeClient.List(context.TODO(), namespaces)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to list namespaces for webhook %s", webhookName)
	}

	var managedNamespaces []string
	for _, ns := range namespaces.Items {
		if managed, err := p.IsNamespaceManaged(ns.Name, webhookName); err == nil && managed {
			managedNamespaces = append(managedNamespaces, ns.Name)
		}
	}

	log.Info("computed managed namespaces", "webhookName", webhookName, "count", len(managedNamespaces), "namespaces", managedNamespaces)
	return managedNamespaces, nil
}

func (p *PoolManager) InitMaps() error {
	err := p.initPodMap()
	if err != nil {
		return err
	}

	err = p.initVirtualMachineMap()
	if err != nil {
		return err
	}

	return nil
}

func checkRange(startMac, endMac net.HardwareAddr) error {
	for idx := 0; idx <= 5; idx++ {
		if startMac[idx] < endMac[idx] {
			return nil
		}
	}

	return fmt.Errorf("invalid range. rangeStart: %s rangeEnd: %s", startMac.String(), endMac.String())
}

func GetMacPoolSize(rangeStart, rangeEnd net.HardwareAddr) (int64, error) {
	err := checkRange(rangeStart, rangeEnd)
	if err != nil {
		return 0, errors.Wrap(err, "mac Pool Size  is negative")
	}

	startInt, err := utils.ConvertHwAddrToInt64(rangeStart)
	if err != nil {
		return 0, errors.Wrap(err, "error converting rangeStart to int64")
	}

	endInt, err := utils.ConvertHwAddrToInt64(rangeEnd)
	if err != nil {
		return 0, errors.Wrap(err, "error converting rangeEnd to int64")
	}

	return endInt - startInt + 1, nil
}

// generateRandomMac generates a random MAC address within the specified range using GetMacPoolSize.
func generateRandomMac(rangeStart, rangeEnd net.HardwareAddr) (net.HardwareAddr, error) {
	poolSize, err := GetMacPoolSize(rangeStart, rangeEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate MAC pool size: %w", err)
	}
	if poolSize <= 0 {
		return nil, fmt.Errorf("invalid MAC pool size: %d", poolSize)
	}

	randomOffset, err := rand.Int(rand.Reader, big.NewInt(poolSize))
	if err != nil {
		return nil, fmt.Errorf("failed to generate random mac offset: %w", err)
	}

	startInt, err := utils.ConvertHwAddrToInt64(rangeStart)
	if err != nil {
		return nil, fmt.Errorf("failed to convert rangeStart to int64: %w", err)
	}

	randomMacInt := startInt + randomOffset.Int64()

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(randomMacInt))
	return net.HardwareAddr(buf[2:]), nil // Skip the first two bytes
}

func (p *PoolManager) getFreeMac() (net.HardwareAddr, error) {
	// Acquire read lock to coordinate with UpdateRanges
	p.rangeMutex.RLock()
	defer p.rangeMutex.RUnlock()

	// this loop will ensure that we check all the range
	// first iteration from current mac to last mac in the range
	// second iteration from first mac in the range to the latest one
	for idx := 0; idx <= 1; idx++ {

		// This loop runs from the current mac to the last one in the range
		for {
			if _, ok := p.macPoolMap[NewMacKey(p.currentMac.String())]; !ok {
				log.V(1).Info("found unused mac", "mac", p.currentMac)
				freeMac := make(net.HardwareAddr, len(p.currentMac))
				copy(freeMac, p.currentMac)

				// move to the next mac after we found a free one
				// If we allocate a mac address then release it and immediately allocate the same one to another object
				// we can have issues with dhcp and arp discovery
				if p.currentMac.String() == p.rangeEnd.String() {
					copy(p.currentMac, p.rangeStart)
				} else {
					p.currentMac = getNextMac(p.currentMac)
				}

				return freeMac, nil
			}

			if p.currentMac.String() == p.rangeEnd.String() {
				break
			}
			p.currentMac = getNextMac(p.currentMac)
		}

		copy(p.currentMac, p.rangeStart)
	}

	return nil, ErrFull
}

func checkCast(mac net.HardwareAddr) error {
	// A bitwise AND between 00000001 and the mac address first octet.
	// In case where the LSB of the first octet (the multicast bit) is on, it will return 1, and 0 otherwise.
	multicastBit := 1 & mac[0]
	if multicastBit != 1 {
		return nil
	}
	return fmt.Errorf("invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is %#0X", mac[0])
}

func getNextMac(currentMac net.HardwareAddr) net.HardwareAddr {
	for idx := 5; idx >= 0; idx-- {
		currentMac[idx] += 1
		if currentMac[idx] != 0 {
			break
		}
	}

	return currentMac
}

// isNamespaceSelectorCompatibleWithOptModeLabel decides whether a namespace should be managed
// by comparing the mutating-webhook's namespaceSelector (that defines the opt-mode)
// and its compatibility to the given label in the namespace
func (p *PoolManager) isNamespaceSelectorCompatibleWithOptModeLabel(namespaceName, mutatingWebhookConfigName, webhookName string, vmOptMode OptMode) (bool, error) {
	isNamespaceManaged, err := isNamespaceManagedByDefault(vmOptMode)
	if err != nil {
		return false, errors.Wrap(err, "Failed to check if namespaces are managed by default by opt-mode")
	}
	ns := v1.Namespace{}
	err = p.cachedKubeClient.Get(context.TODO(), client.ObjectKey{Name: namespaceName}, &ns)
	if err != nil {
		return false, errors.Wrap(err, "Failed to get Namespace")
	}
	namespaceLabelMap := ns.GetLabels()
	log.V(1).Info("namespaceName Labels", "vm instance namespaceName", namespaceName, "Labels", namespaceLabelMap)

	if namespaceLabelMap != nil {
		webhook, err := p.lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName)
		if err != nil {
			return false, errors.Wrap(err, "Failed lookup webhook in MutatingWebhookConfig")
		}
		if namespaceLabelSet := labels.Set(namespaceLabelMap); namespaceLabelSet != nil {
			isNamespaceManaged, err = isNamespaceManagedByWebhookNamespaceSelector(webhook, vmOptMode, namespaceLabelSet, isNamespaceManaged)
			if err != nil {
				return false, errors.Wrap(err, "Failed to check if namespace managed by webhook namespaceSelector")
			}
		}
	}

	return isNamespaceManaged, nil
}

func (p *PoolManager) lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName string) (*admissionregistrationv1.MutatingWebhook, error) {
	mutatingWebhookConfiguration := admissionregistrationv1.MutatingWebhookConfiguration{}
	err := p.cachedKubeClient.Get(context.TODO(), client.ObjectKey{Name: mutatingWebhookConfigName}, &mutatingWebhookConfiguration)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get mutatingWebhookConfig")
	}
	for _, webhook := range mutatingWebhookConfiguration.Webhooks {
		if webhook.Name == webhookName {
			return &webhook, nil
		}
	}
	return nil, fmt.Errorf("webhook %s was not found on mutatingWebhookConfig %s", webhookName, mutatingWebhookConfigName)
}

// isNamespaceManagedByDefault checks if namespaces are managed by default by opt-mode
func isNamespaceManagedByDefault(vmOptMode OptMode) (bool, error) {
	switch vmOptMode {
	case OptInMode:
		return false, nil
	case OptOutMode:
		return true, nil
	default:
		return false, fmt.Errorf("opt-mode is not defined: %s", vmOptMode)
	}
}

// IsNamespaceManaged checks if the namespace of the instance is managed by kubemacpool in terms of opt-mode
func (p *PoolManager) IsNamespaceManaged(namespaceName, webhookName string) (bool, error) {
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

// isNamespaceManagedByWebhookNamespaceSelector checks if namespace managed by webhook namespaceSelector and opt-mode
func isNamespaceManagedByWebhookNamespaceSelector(webhook *admissionregistrationv1.MutatingWebhook, vmOptMode OptMode, namespaceLabelSet labels.Set, defaultIsManaged bool) (bool, error) {
	webhookNamespaceLabelSelector, err := metav1.LabelSelectorAsSelector(webhook.NamespaceSelector)
	if err != nil {
		return false, errors.Wrapf(err, "Failed to set webhook Namespace Label Selector for webhook %s", webhook.Name)
	}

	isMatch := webhookNamespaceLabelSelector.Matches(namespaceLabelSet)
	log.V(1).Info("webhook NamespaceLabelSelectors", "webhookName", webhook.Name, "NamespaceLabelSelectors", webhookNamespaceLabelSelector)
	if vmOptMode == OptInMode && isMatch {
		// if we are in opt-in mode then we check that the namespace has the including label
		return true, nil
	} else if vmOptMode == OptOutMode && !isMatch {
		// if we are in opt-out mode then we check that the namespace does not have the excluding label
		return false, nil
	}
	return defaultIsManaged, nil
}

// getOptMode returns the configured opt-mode
func (p *PoolManager) getOptMode(mutatingWebhookConfigName, webhookName string) (OptMode, error) {
	webhook, err := p.lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName)
	if err != nil {
		return "", errors.Wrap(err, "Failed lookup webhook in MutatingWebhookConfig")
	}
	for _, expression := range webhook.NamespaceSelector.MatchExpressions {
		if reflect.DeepEqual(expression, metav1.LabelSelectorRequirement{Key: webhookName, Operator: "In", Values: []string{"allocate"}}) {
			return OptInMode, nil
		} else if reflect.DeepEqual(expression, metav1.LabelSelectorRequirement{Key: webhookName, Operator: "NotIn", Values: []string{"ignore"}}) {
			return OptOutMode, nil
		}
	}

	// opt-in can technically also be defined with matchLabels
	if value, ok := webhook.NamespaceSelector.MatchLabels[webhookName]; ok && value == "allocate" {
		return OptInMode, nil
	}

	return "", fmt.Errorf("No Opt mode defined for webhook %s in mutatingWebhookConfig %s", webhookName, mutatingWebhookConfigName)
}

// UpdateRanges atomically updates the MAC address ranges for allocation
// This method will be used for dynamic range updates without pod restart
func (p *PoolManager) UpdateRanges(newStart, newEnd net.HardwareAddr) error {
	// Validate the new ranges
	if err := checkRange(newStart, newEnd); err != nil {
		return err
	}

	if err := checkCast(newStart); err != nil {
		return fmt.Errorf("invalid range start: %v", err)
	}

	if err := checkCast(newEnd); err != nil {
		return fmt.Errorf("invalid range end: %v", err)
	}

	// Update ranges atomically
	p.rangeMutex.Lock()
	defer p.rangeMutex.Unlock()

	oldStart := p.rangeStart
	oldEnd := p.rangeEnd

	p.rangeStart = newStart
	p.rangeEnd = newEnd

	// Reset current MAC to somewhere in the new range
	newCurrentMac, err := generateRandomMac(newStart, newEnd)
	if err != nil {
		// Rollback on failure
		p.rangeStart = oldStart
		p.rangeEnd = oldEnd
		return fmt.Errorf("failed to generate new current MAC: %v", err)
	}
	p.currentMac = newCurrentMac

	log.Info("Successfully updated MAC allocation ranges",
		"oldStart", oldStart.String(), "oldEnd", oldEnd.String(),
		"newStart", newStart.String(), "newEnd", newEnd.String(),
		"newCurrentMac", newCurrentMac.String())

	return nil
}

// GetCurrentRanges returns the current MAC address ranges
func (p *PoolManager) GetCurrentRanges() (start, end string) {
	p.rangeMutex.RLock()
	defer p.rangeMutex.RUnlock()

	return p.rangeStart.String(), p.rangeEnd.String()
}

// getCurrentMAC returns the current MAC address with proper locking
func (p *PoolManager) getCurrentMAC() string {
	p.rangeMutex.RLock()
	defer p.rangeMutex.RUnlock()

	return p.currentMac.String()
}

// ManagerNamespace returns the manager namespace
func (p *PoolManager) ManagerNamespace() string {
	return p.managerNamespace
}
