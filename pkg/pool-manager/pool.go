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
	err := checkRange(rangeStart, rangeEnd)
	if err != nil {
		return nil, err
	}
	err = checkCast(rangeStart)
	if err != nil {
		return nil, fmt.Errorf("RangeStart is invalid: %v", err)
	}
	err = checkCast(rangeEnd)
	if err != nil {
		return nil, fmt.Errorf("RangeEnd is invalid: %v", err)
	}
	currentMac, err := generateRandomMac(rangeStart, rangeEnd)
	if err != nil {
		return nil, fmt.Errorf("first MAC generated randomely from ranges is invalid: %v", err)
	}
	log.Info("pool allocation will start with random MAC", "currentMac", currentMac.String())

	poolManger := &PoolManager{
		cachedKubeClient: cachedKubeClient,
		kubeClient:       kubeClient,
		isKubevirt:       kubevirtExist,
		rangeEnd:         rangeEnd,
		rangeStart:       rangeStart,
		currentMac:       currentMac,
		managerNamespace: managerNamespace,
		podToMacPoolMap:  map[string]map[string]string{},
		macPoolMap:       macMap{},
		poolMutex:        sync.Mutex{},
		waitTime:         waitTime,
	}

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
func (p *PoolManager) getManagedNamespaces(webhookName string) (map[string]struct{}, error) {
	log.V(1).Info("computing managed namespaces for initialization", "webhookName", webhookName)

	webhook, err := p.lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to lookup webhook %s", webhookName)
	}

	vmOptMode, err := getOptModeFromWebhook(webhookName, webhook.NamespaceSelector)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get opt-mode for webhook %s", webhookName)
	}

	managedNamespaces := make(map[string]struct{})
	continueFlag := ""
	const pageSize int64 = 500

	for {
		namespaces := &v1.NamespaceList{}
		err = p.kubeClient.List(context.TODO(), namespaces, &client.ListOptions{
			Limit:    pageSize,
			Continue: continueFlag,
		})
		if err != nil {
			return nil, errors.Wrapf(err, "failed to list namespaces for webhook %s", webhookName)
		}

		for _, ns := range namespaces.Items {
			managed, err := isNamespaceManagedFromObject(&ns, webhook.NamespaceSelector, vmOptMode)
			if err != nil {
				log.Error(err, "failed to check if namespace is managed, skipping", "namespace", ns.Name, "webhookName", webhookName)
				continue
			}
			if managed {
				managedNamespaces[ns.Name] = struct{}{}
			}
		}

		continueFlag = namespaces.GetContinue()
		if continueFlag == "" {
			break
		}
	}

	log.Info("computed managed namespaces", "webhookName", webhookName, "count", len(managedNamespaces))
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

	return fmt.Errorf("Invalid range. rangeStart: %s rangeEnd: %s", startMac.String(), endMac.String())
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
	webhook, err := p.lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName)
	if err != nil {
		return false, errors.Wrap(err, "failed to lookup webhook")
	}

	vmOptMode, err := getOptModeFromWebhook(webhookName, webhook.NamespaceSelector)
	if err != nil {
		return false, errors.Wrap(err, "failed to get opt-Mode")
	}

	ns := v1.Namespace{}
	err = p.cachedKubeClient.Get(context.TODO(), client.ObjectKey{Name: namespaceName}, &ns)
	if err != nil {
		return false, errors.Wrap(err, "failed to get namespace")
	}

	isNamespaceManaged, err := isNamespaceManagedFromObject(&ns, webhook.NamespaceSelector, vmOptMode)
	if err != nil {
		return false, errors.Wrap(err, "failed to check if namespace is managed")
	}

	log.V(1).Info("IsNamespaceManaged", "vmOptMode", vmOptMode, "namespaceName", namespaceName, "is namespace in the game", isNamespaceManaged)
	return isNamespaceManaged, nil
}

// isNamespaceManagedByWebhookNamespaceSelector checks if namespace managed by webhook namespaceSelector and opt-mode
func isNamespaceManagedByWebhookNamespaceSelector(namespaceSelector *metav1.LabelSelector, vmOptMode OptMode, namespaceLabelSet labels.Set, defaultIsManaged bool) (bool, error) {
	webhookNamespaceLabelSelector, err := metav1.LabelSelectorAsSelector(namespaceSelector)
	if err != nil {
		return false, errors.Wrap(err, "failed to convert namespace selector")
	}

	isMatch := webhookNamespaceLabelSelector.Matches(namespaceLabelSet)
	if vmOptMode == OptInMode && isMatch {
		// if we are in opt-in mode then we check that the namespace has the including label
		return true, nil
	} else if vmOptMode == OptOutMode && !isMatch {
		// if we are in opt-out mode then we check that the namespace does not have the excluding label
		return false, nil
	}
	return defaultIsManaged, nil
}

// isNamespaceManagedFromObject checks if a namespace is managed without making additional API calls
// This optimized version uses the namespace object directly instead of fetching it by name
func isNamespaceManagedFromObject(namespace *v1.Namespace, namespaceSelector *metav1.LabelSelector, vmOptMode OptMode) (bool, error) {
	defaultIsManaged, err := isNamespaceManagedByDefault(vmOptMode)
	if err != nil {
		return false, errors.Wrap(err, "failed to determine default managed state")
	}

	namespaceLabelMap := namespace.GetLabels()
	if namespaceLabelMap == nil {
		return defaultIsManaged, nil
	}

	namespaceLabelSet := labels.Set(namespaceLabelMap)
	isManaged, err := isNamespaceManagedByWebhookNamespaceSelector(namespaceSelector, vmOptMode, namespaceLabelSet, defaultIsManaged)
	if err != nil {
		return false, errors.Wrap(err, "failed to check if namespace managed by webhook namespaceSelector")
	}

	return isManaged, nil
}

// getOptMode returns the configured opt-mode
func (p *PoolManager) getOptMode(mutatingWebhookConfigName, webhookName string) (OptMode, error) {
	webhook, err := p.lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName)
	if err != nil {
		return "", errors.Wrap(err, "Failed lookup webhook in MutatingWebhookConfig")
	}
	return getOptModeFromWebhook(webhookName, webhook.NamespaceSelector)
}

// getOptModeFromWebhook extracts the opt-mode from a webhook's namespace selector
// This is a helper function to avoid redundant webhook fetches
func getOptModeFromWebhook(webhookName string, namespaceSelector *metav1.LabelSelector) (OptMode, error) {
	if namespaceSelector == nil {
		return "", fmt.Errorf("webhook %s has no NamespaceSelector", webhookName)
	}

	for _, expression := range namespaceSelector.MatchExpressions {
		if reflect.DeepEqual(expression, metav1.LabelSelectorRequirement{Key: webhookName, Operator: "In", Values: []string{"allocate"}}) {
			return OptInMode, nil
		} else if reflect.DeepEqual(expression, metav1.LabelSelectorRequirement{Key: webhookName, Operator: "NotIn", Values: []string{"ignore"}}) {
			return OptOutMode, nil
		}
	}

	// opt-in can technically also be defined with matchLabels
	if value, ok := namespaceSelector.MatchLabels[webhookName]; ok && value == "allocate" {
		return OptInMode, nil
	}

	return "", fmt.Errorf("no opt mode defined for webhook %s", webhookName)
}
