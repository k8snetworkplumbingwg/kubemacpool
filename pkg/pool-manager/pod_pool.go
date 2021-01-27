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
	"encoding/json"
	"fmt"
	"net"
	"strings"

	multus "github.com/intel/multus-cni/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (p *PoolManager) AllocatePodMac(pod *corev1.Pod) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	networkValue, ok := pod.Annotations[networksAnnotation]
	if !ok {
		return nil
	}

	// allocate only when the network status is no exist
	// we want to connect the allocated mac from the webhook to a pod object in the podToMacPoolMap
	// run it before multus added the status annotation
	// this mean the pod is not ready
	if _, ok := pod.Annotations[networksStatusAnnotation]; ok {
		return nil
	}

	networks, err := parsePodNetworkAnnotation(networkValue, pod.Namespace)
	if err != nil {
		return err
	}

	log.V(1).Info("pod meta data", "podMetaData", (*pod).ObjectMeta)

	if len(networks) == 0 {
		return nil
	}

	// validate if the pod is related to kubevirt
	if p.isRelatedToKubevirt(pod) {
		// nothing to do here. the mac is already by allocated by the virtual machine webhook
		log.V(1).Info("This pod have ownerReferences from kubevirt skipping")
		return nil
	}

	podNamespacedName := podNamespaced(pod)
	if pod.Name == "" {
		// The pod name could temporarily be empty, this will be fixed by the
		// time the pod creation is complete.
		// To avoid collision between two macs inserted in this webhook request,
		// we give the pod a temporary name
		podNamespacedName = fmt.Sprintf("pod/%s/tempName", pod.Namespace)
	}

	macMap, err := p.getClusterMacs()
	if err != nil {
		return err
	}

	allocations := []string{}
	networkList := []*multus.NetworkSelectionElement{}
	for _, network := range networks {
		if network.MacRequest != "" {
			if err := p.allocatePodRequestedMac(macMap, podNamespacedName, network); err != nil {
				return err
			}
			allocations = append(allocations, network.MacRequest)
		} else {
			macAddr, err := p.allocatePodFromPool(macMap, podNamespacedName, network)
			if err != nil {
				return err
			}

			network.MacRequest = macAddr
			allocations = append(allocations, macAddr)
		}
		networkList = append(networkList, network)
	}

	networkListJson, err := json.Marshal(networkList)
	if err != nil {
		return err
	}
	pod.Annotations[networksAnnotation] = string(networkListJson)

	return nil
}

func (p *PoolManager) ReleasePodMac(podName string) error {
	return nil
}

func (p *PoolManager) allocatePodRequestedMac(macMap map[string]string, podNamespacedName string, network *multus.NetworkSelectionElement) error {
	requestedMac := network.MacRequest
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	occupied, err := p.isMacOccupiedInConfigMap(requestedMac)
	if err != nil {
		return err
	}
	if occupied {
		err := fmt.Errorf("failed to allocate requested mac address")
		log.Error(err, "mac address already taken in configmap")
		return err
	}

	if instanceName, exist := macMap[requestedMac]; exist {
		if namedInterface(podNamespacedName, network.Name) != instanceName {
			err := fmt.Errorf("failed to allocate requested mac address")
			log.Error(err, "mac address already allocated to %s", instanceName)

			return err
		}
	}

	macMap[requestedMac] = namedInterface(podNamespacedName, network.Name)

	return nil
}

func (p *PoolManager) allocatePodFromPool(macMap map[string]string, podNamespacedName string, network *multus.NetworkSelectionElement) (string, error) {
	macAddr, err := p.getFreeMac(macMap)
	if err != nil {
		return "", err
	}

	macMap[macAddr.String()] = namedInterface(podNamespacedName, network.Name)
	log.Info("mac from pool was allocated to the pod",
		"allocatedMac", macAddr.String(),
		podNamespacedName)
	return macAddr.String(), nil
}

func (p *PoolManager) updatePodsMacsToMap(macMap map[string]string) error {
	pods, err := p.kubeClient.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		if pod.Annotations == nil {
			continue
		}

		networkValue, ok := pod.Annotations[networksAnnotation]
		if !ok {
			continue
		}

		networks, err := parsePodNetworkAnnotation(networkValue, pod.Namespace)
		if err != nil {
			continue
		}

		if len(networks) == 0 {
			continue
		}

		// validate if the pod is related to kubevirt
		if p.isRelatedToKubevirt(&pod) {
			// nothing to do here. the mac is already by allocated by the virtual machine webhook
			continue
		}

		for _, network := range networks {
			if network.MacRequest == "" {
				continue
			}

			macMap[network.MacRequest] = namedInterface(podNamespaced(&pod), network.Name)
		}
	}

	return nil
}

func (p *PoolManager) initPodMap() error {
	return nil
}

//func (p *PoolManager) allocatedToCurrentPod(macMap map[string]string, podNamespacedName string, network *multus.NetworkSelectionElement) bool {
//	networks, exist := macMap[podNamespacedName]
//	if !exist {
//		return false
//	}
//
//	allocatedMac, exist := networks[network.Name]
//
//	if !exist {
//		return false
//	}
//
//	if allocatedMac == network.MacRequest {
//		return true
//	}
//
//	return false
//}

// Checks if the namespace of a pod instance is opted in for kubemacpool
func (p *PoolManager) IsPodInstanceOptedIn(namespaceName string) (bool, error) {
	return p.isNamespaceSelectorCompatibleWithOptModeLabel(namespaceName, "kubemacpool-mutator", "mutatepods.kubemacpool.io", OptInMode)
}

func podNamespaced(pod *corev1.Pod) string {
	return fmt.Sprintf("pod/%s/%s", pod.Namespace, pod.Name)
}

func parsePodNetworkAnnotation(podNetworks, defaultNamespace string) ([]*multus.NetworkSelectionElement, error) {
	var networks []*multus.NetworkSelectionElement

	if podNetworks == "" {
		return nil, fmt.Errorf("parsePodNetworkAnnotation: pod annotation not having \"network\" as key, refer Multus README.md for the usage guide")
	}

	if strings.IndexAny(podNetworks, "[{\"") >= 0 {
		if err := json.Unmarshal([]byte(podNetworks), &networks); err != nil {
			return nil, fmt.Errorf("parsePodNetworkAnnotation: failed to parse pod Network Attachment Selection Annotation JSON format: %v", err)
		}
	} else {
		log.Info("Only JSON List Format for networks is allowed to be parsed")
		return networks, nil
	}

	for _, network := range networks {
		if network.Namespace == "" {
			network.Namespace = defaultNamespace
		}
	}

	return networks, nil
}
