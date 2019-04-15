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

	log.V(1).Info("AllocatePodMac: Data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"vmmap", p.vmToMacPoolMap,
		"currentMac", p.currentMac.String())

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

	networkList := []*multus.NetworkSelectionElement{}
	for _, network := range networks {
		if network.MacRequest != "" {
			if err := p.allocatePodRequestedMac(network, pod); err != nil {
				return err
			}
		} else {
			macAddr, err := p.allocatePodFromPool(network, pod)
			if err != nil {
				return err
			}

			network.MacRequest = macAddr
			networkList = append(networkList, network)
		}
	}

	networkListJson, err := json.Marshal(networkList)
	if err != nil {
		return err
	}
	pod.Annotations[networksAnnotation] = string(networkListJson)

	return nil
}

func (p *PoolManager) ReleasePodMac(podName string) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	log.V(1).Info("ReleasePodMac: Data",
		"macmap", p.macPoolMap,
		"podmap", p.podToMacPoolMap,
		"vmmap", p.vmToMacPoolMap,
		"currentMac", p.currentMac.String())

	macList, ok := p.podToMacPoolMap[podName]

	if !ok {
		log.Error(fmt.Errorf("not found"), "pod not found in the map",
			"podName", podName)
		return nil
	}

	if macList == nil {
		log.Error(fmt.Errorf("list empty"), "failed to get mac address list")
		return nil
	}

	for _, macAddr := range macList {
		delete(p.macPoolMap, macAddr)
		log.Info("released mac from pod", "mac", macAddr, "pod", podName)
	}

	delete(p.podToMacPoolMap, podName)
	log.V(1).Info("removed pod from podToMacPoolMap", "pod", podName)
	return nil
}

func (p *PoolManager) allocatePodRequestedMac(network *multus.NetworkSelectionElement, pod *corev1.Pod) error {
	requestedMac := network.MacRequest
	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	if macAllocationStatus, exist := p.macPoolMap[requestedMac]; exist &&
		macAllocationStatus == AllocationStatusAllocated &&
		!p.allocatedToCurrentPod(podNamespaced(pod), network) {

		err := fmt.Errorf("failed to allocate requested mac address")
		log.Error(err, "mac address already allocated")

		return err
	}

	if pod.Name == "" {
		// we are going to create the podToMacPoolMap in the controller call
		// because we don't have the pod name in the webhook
		p.macPoolMap[requestedMac] = AllocationStatusWaitingForPod
		return nil
	}

	p.macPoolMap[requestedMac] = AllocationStatusAllocated
	if p.podToMacPoolMap[podNamespaced(pod)] == nil {
		p.podToMacPoolMap[podNamespaced(pod)] = map[string]string{}
	}
	p.podToMacPoolMap[podNamespaced(pod)][network.Name] = requestedMac
	log.Info("requested mac was allocated for pod",
		"requestedMap", requestedMac,
		"podName", pod.Name,
		"podNamespace", pod.Namespace)

	return nil
}

func (p *PoolManager) allocatePodFromPool(network *multus.NetworkSelectionElement, pod *corev1.Pod) (string, error) {
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	if pod.Name == "" {
		// we are going to create the podToMacPoolMap in the controller call
		// because we don't have the pod name in the webhook
		p.macPoolMap[macAddr.String()] = AllocationStatusWaitingForPod
		return macAddr.String(), nil
	}

	p.macPoolMap[macAddr.String()] = AllocationStatusAllocated
	if p.podToMacPoolMap[podNamespaced(pod)] == nil {
		p.podToMacPoolMap[podNamespaced(pod)] = map[string]string{}
	}
	p.podToMacPoolMap[podNamespaced(pod)][network.Name] = macAddr.String()
	log.Info("mac from pool was allocated to the pod",
		"allocatedMac", macAddr.String(),
		"podName", pod.Name,
		"podNamespace", pod.Namespace)
	return macAddr.String(), nil
}

func (p *PoolManager) initPodMap() error {
	log.V(1).Info("start InitMaps to reserve existing mac addresses before allocation new ones")
	pods, err := p.kubeClient.CoreV1().Pods("").List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		log.V(1).Info("InitMaps for pod", "podName", pod.Name, "podNamespace", pod.Namespace)
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

		log.V(1).Info("pod meta data", "podMetaData", pod.ObjectMeta)
		if len(networks) == 0 {
			continue
		}

		// validate if the pod is related to kubevirt
		if p.isRelatedToKubevirt(&pod) {
			// nothing to do here. the mac is already by allocated by the virtual machine webhook
			log.V(1).Info("This pod have ownerReferences from kubevirt skipping")
			continue
		}

		for _, network := range networks {
			if network.MacRequest == "" {
				continue
			}

			if err := p.allocatePodRequestedMac(network, &pod); err != nil {
				// Dont return an error here if we can't allocate a mac for a running pod
				log.Error(fmt.Errorf("failed to parse mac address for pod"),
					"Invalid mac address for pod",
					"namespace", pod.Namespace,
					"name", pod.Name,
					"mac", network.MacRequest)
				continue
			}
		}
	}

	return nil
}

func (p *PoolManager) allocatedToCurrentPod(podname string, network *multus.NetworkSelectionElement) bool {
	networks, exist := p.podToMacPoolMap[podname]
	if !exist {
		return false
	}

	allocatedMac, exist := networks[network.Name]

	if !exist {
		return false
	}

	if allocatedMac == network.MacRequest {
		return true
	}

	return false
}

func podNamespaced(pod *corev1.Pod) string {
	return fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
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
