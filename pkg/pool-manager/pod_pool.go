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
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const tempPodName = "tempPodName"

func (p *PoolManager) AllocatePodMac(pod *corev1.Pod) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	log.V(1).Info("AllocatePodMac: Data",
		"macmap", p.macPoolMap,
		"currentMac", p.currentMac.String())

	networkValue, ok := pod.Annotations[NetworksAnnotation]
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
	podFullName := podNamespaced(pod)
	newAllocations := map[string]string{}
	networkList := []*multus.NetworkSelectionElement{}
	for _, network := range networks {
		if network.MacRequest != "" {
			if err := p.allocatePodRequestedMac(network, pod); err != nil {
				p.revertAllocationOnPod(podFullName, newAllocations)
				return err
			}
			newAllocations[network.Name] = network.MacRequest
		} else {
			macAddr, err := p.allocatePodFromPool(network, pod)
			if err != nil {
				p.revertAllocationOnPod(podFullName, newAllocations)
				return err
			}

			network.MacRequest = macAddr
			newAllocations[network.Name] = macAddr
		}
		networkList = append(networkList, network)
	}

	networkListJson, err := json.Marshal(networkList)
	if err != nil {
		return err
	}
	pod.Annotations[NetworksAnnotation] = string(networkListJson)

	return nil
}

func (p *PoolManager) ReleaseAllPodMacs(podFullName string) error {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	log.V(1).Info("ReleaseAllPodMacs: Data",
		"macmap", p.macPoolMap,
		"podFullName", podFullName)

	vmMacMap, err := p.macPoolMap.filterInByInstanceName(podFullName)

	if err != nil {
		log.Error(fmt.Errorf("not found"), "pod not found in the map",
			"podName", podFullName)
		return nil
	}

	if len(*vmMacMap) == 0 {
		return nil
	}

	for macAddr, _ := range *vmMacMap {
		delete(p.macPoolMap, macAddr)
		log.Info("released mac from pod", "mac", macAddr, "pod", podFullName)
	}

	return nil
}

func (p *PoolManager) allocatePodRequestedMac(network *multus.NetworkSelectionElement, pod *corev1.Pod) error {
	requestedMac := network.MacRequest

	if _, err := net.ParseMAC(requestedMac); err != nil {
		return err
	}

	podFullName := podNamespaced(pod)
	if pod.Name == "" {
		// the pod name may have not been updated in the webhook context yet,
		// so we use a temp pod name and update it the the controller
		podFullName = tempPodName
		p.macPoolMap.createOrUpdateEntry(requestedMac, podFullName, network.Name)
		return nil
	}
	if macEntry, exist := p.macPoolMap[requestedMac]; exist {
		if !macAlreadyBelongsToPodAndNetwork(podFullName, network.Name, macEntry) {
			err := fmt.Errorf("failed to allocate requested mac address")
			log.Error(err, "mac address already allocated")

			return err
		}
	}

	p.macPoolMap.createOrUpdateEntry(requestedMac, podFullName, network.Name)

	log.Info("requested mac was allocated for pod",
		"requestedMap", requestedMac,
		"podFullName", podFullName)

	return nil
}

func (p *PoolManager) allocatePodFromPool(network *multus.NetworkSelectionElement, pod *corev1.Pod) (string, error) {
	macAddr, err := p.getFreeMac()
	if err != nil {
		return "", err
	}

	podFullName := podNamespaced(pod)
	if pod.Name == "" {
		// the pod name may have not been updated in the webhook context yet,
		// so we use a temp pod name and update it the the controller
		podFullName = tempPodName
		p.macPoolMap.createOrUpdateEntry(macAddr.String(), podFullName, network.Name)
		return macAddr.String(), nil
	}

	p.macPoolMap.createOrUpdateEntry(macAddr.String(), podFullName, network.Name)

	log.Info("mac from pool was allocated to the pod",
		"allocatedMac", macAddr.String(),
		"podFullName", podFullName)
	return macAddr.String(), nil
}

// paginatePodsWithLimit performs a pods list request with pagination, to limit the amount of pods received at a time
// and prevent exceeding the memory limit.
func (p *PoolManager) paginatePodsWithLimit(limit int64, f func(pods *corev1.PodList) error) error {
	continueFlag := ""
	for {
		pods, err := p.kubeClient.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{Limit: limit, Continue: continueFlag})
		if err != nil {
			return err
		}

		err = f(pods)
		if err != nil {
			return err
		}

		continueFlag = pods.GetContinue()
		log.V(1).Info("limit Pod list", "pods len", len(pods.Items), "remaining", pods.GetRemainingItemCount(), "continue", continueFlag)
		if continueFlag == "" {
			break
		}
	}
	return nil
}

func (p *PoolManager) initPodMap() error {
	log.V(1).Info("start InitMaps to reserve existing mac addresses before allocation new ones")
	err := p.paginatePodsWithLimit(100, func(pods *corev1.PodList) error {
		for _, pod := range pods.Items {
			log.V(1).Info("InitMaps for pod", "podName", pod.Name, "podNamespace", pod.Namespace)
			instanceManaged, err := p.IsPodManaged(pod.GetNamespace())
			if err != nil {
				continue
			}
			if !instanceManaged {
				continue
			}

			if pod.Annotations == nil {
				continue
			}

			networkValue, ok := pod.Annotations[NetworksAnnotation]
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
	})
	if err != nil {
		return errors.Wrap(err, "failed iterating over all cluster pods")
	}

	return nil
}

func macAlreadyBelongsToPodAndNetwork(podFullName, networkName string, macEntry macEntry) bool {
	if macEntry.instanceName == tempPodName {
		// do not block macs with not yet updated pod names.
		return false
	}
	if macEntry.instanceName == podFullName && macEntry.macInstanceKey == networkName {
		return true
	}
	return false
}

// Revert allocation if one of the requested mac addresses fails to be allocated
func (p *PoolManager) revertAllocationOnPod(podFullName string, allocations map[string]string) {
	log.V(1).Info("Revert vm allocation", "podFullName", podFullName, "allocations", allocations)
	for _, macAddress := range allocations {
		delete(p.macPoolMap, macAddress)
	}
}

// IsPodManaged checks if the namespace of a pod instance is opted in for kubemacpool
func (p *PoolManager) IsPodManaged(namespaceName string) (bool, error) {
	return p.IsNamespaceManaged(namespaceName, podsWebhookName)
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
