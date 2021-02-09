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
	"reflect"
	"sync"
	"time"

	"github.com/pkg/errors"
	"k8s.io/api/admissionregistration/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

const (
	RangeStartEnv              = "RANGE_START"
	RangeEndEnv                = "RANGE_END"
	RuntimeObjectFinalizerName = "k8s.v1.cni.cncf.io/kubeMacPool"
	networksAnnotation         = "k8s.v1.cni.cncf.io/networks"
	networksStatusAnnotation   = "k8s.v1.cni.cncf.io/networks-status"
)

var log = logf.Log.WithName("PoolManager")

// now is an artifact to do some unit testing without waiting for expiration time.
var now = func() time.Time { return time.Now() }

type PoolManager struct {
	kubeClient       kubernetes.Interface // kubernetes client
	rangeStart       net.HardwareAddr     // fist mac in range
	rangeEnd         net.HardwareAddr     // last mac in range
	currentMac       net.HardwareAddr     // last given mac
	managerNamespace string
	macPoolMap       map[string]AllocationStatus  // allocated mac map and status
	podToMacPoolMap  map[string]map[string]string // map allocated mac address by networkname and namespace/podname: {"namespace/podname: {"network name": "mac address"}}
	poolMutex        sync.Mutex                   // mutex for allocation an release
	isKubevirt       bool                         // bool if kubevirt virtualmachine crd exist in the cluster
	waitTime         int                          // Duration in second to free macs of allocated vms that failed to start.
}

type OptMode string

const (
	OptInMode  OptMode = "Opt-in"
	OptOutMode OptMode = "Opt-out"
)

type AllocationStatus string

const (
	AllocationStatusAllocated     AllocationStatus = "Allocated"
	AllocationStatusWaitingForPod AllocationStatus = "WaitingForPod"
)

func NewPoolManager(kubeClient kubernetes.Interface, rangeStart, rangeEnd net.HardwareAddr, managerNamespace string, kubevirtExist bool, waitTime int) (*PoolManager, error) {
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

	currentMac := make(net.HardwareAddr, len(rangeStart))
	copy(currentMac, rangeStart)

	poolManger := &PoolManager{kubeClient: kubeClient,
		isKubevirt:       kubevirtExist,
		rangeEnd:         rangeEnd,
		rangeStart:       rangeStart,
		currentMac:       currentMac,
		managerNamespace: managerNamespace,
		podToMacPoolMap:  map[string]map[string]string{},
		macPoolMap:       map[string]AllocationStatus{},
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
	return nil
}

func (p *PoolManager) getFreeMac() (net.HardwareAddr, error) {
	// this look will ensure that we check all the range
	// first iteration from current mac to last mac in the range
	// second iteration from first mac in the range to the latest one
	for idx := 0; idx <= 1; idx++ {

		// This loop runs from the current mac to the last one in the range
		for {
			if _, ok := p.macPoolMap[p.currentMac.String()]; !ok {
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

	return nil, fmt.Errorf("the range is full")
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

	ns, err := p.kubeClient.CoreV1().Namespaces().Get(context.TODO(), namespaceName, metav1.GetOptions{})
	if err != nil {
		return false, errors.Wrap(err, "Failed to get Namespace")
	}
	namespaceLabelMap := ns.GetLabels()
	log.V(1).Info("namespaceName Labels", "vm instance namespaceName", namespaceName, "Labels", namespaceLabelMap)

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

	return isNamespaceManaged, nil
}

func (p *PoolManager) lookupWebhookInMutatingWebhookConfig(mutatingWebhookConfigName, webhookName string) (*v1beta1.MutatingWebhook, error) {
	mutatingWebhookConfiguration, err := p.kubeClient.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Get(context.TODO(), mutatingWebhookConfigName, metav1.GetOptions{})
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

// isNamespaceManagedByWebhookNamespaceSelector checks if namespace managed by webhook namespaceSelector and opt-mode
func isNamespaceManagedByWebhookNamespaceSelector(webhook *v1beta1.MutatingWebhook, vmOptMode OptMode, namespaceLabelSet labels.Set, defaultIsManaged bool) (bool, error) {
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
