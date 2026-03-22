/*
Copyright 2025 The KubeMacPool Authors.

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

package maccollision

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netutils "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/utils"
)

var cacheLog = logf.Log.WithName("MACCollision Cache")

const (
	// MacAddressIndexName is the index name for MAC address lookups
	MacAddressIndexName = "status.interfaces.mac"

	// PodMacAddressIndexName is the index name for Pod MAC address lookups.
	PodMacAddressIndexName = "annotations.networks.mac"
)

// StripVMIForCollisionDetection keeps only:
// metadata (minimal), status.interfaces, status.phase, status.migrationState
// Everything else is stripped to optimize cache memory usage.
func StripVMIForCollisionDetection(obj interface{}) (interface{}, error) {
	vmi, ok := obj.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return obj, nil
	}

	// Create minimal VMI with only fields needed for collision detection
	stripped := &kubevirtv1.VirtualMachineInstance{
		TypeMeta: vmi.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:              vmi.Name,
			Namespace:         vmi.Namespace,
			UID:               vmi.UID,
			DeletionTimestamp: vmi.DeletionTimestamp,
		},
		Status: kubevirtv1.VirtualMachineInstanceStatus{
			Phase:          vmi.Status.Phase,
			Interfaces:     vmi.Status.Interfaces,
			MigrationState: vmi.Status.MigrationState,
		},
	}

	return stripped, nil
}

// IndexVMIByMAC returns all MAC addresses from a VMI's status for indexing.
// A VMI with multiple interfaces will be indexed under each MAC address.
// This enables O(1) lookups of all VMIs that have a given MAC address.
func IndexVMIByMAC(obj client.Object) []string {
	vmi, ok := obj.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return nil
	}

	macs := []string{}
	for _, iface := range vmi.Status.Interfaces {
		if iface.MAC != "" {
			normalizedMAC, err := NormalizeMacAddress(iface.MAC)
			if err != nil {
				cacheLog.Error(err, "failed to normalize MAC address", "mac", iface.MAC, "vmi", vmi.Name, "namespace", vmi.Namespace)
				continue
			}
			macs = append(macs, normalizedMAC)
		}
	}

	return macs
}

// StripPodForCollisionDetection keeps only fields needed for collision detection,
// reducing memory for cached Pod objects.
func StripPodForCollisionDetection(obj interface{}) (interface{}, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil
	}

	stripped := &corev1.Pod{
		TypeMeta: pod.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:        pod.Name,
			Namespace:   pod.Namespace,
			UID:         pod.UID,
			Labels:      pod.Labels,
			Annotations: pod.Annotations,
		},
		Status: corev1.PodStatus{
			Phase: pod.Status.Phase,
		},
	}

	return stripped, nil
}

// IndexPodByMAC returns all requested MAC addresses from a Pod's multus
// network-attachment annotation for indexing.
// Returns nil if multus has not yet processed the Pod (no network-status annotation).
func IndexPodByMAC(obj client.Object) []string {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}

	if _, hasStatus := pod.Annotations[networkv1.NetworkStatusAnnot]; !hasStatus {
		return nil
	}

	networks, err := netutils.ParsePodNetworkAnnotation(pod)
	if err != nil {
		return nil
	}

	seen := map[string]struct{}{}
	var macs []string
	for _, net := range networks {
		if net.MacRequest == "" {
			continue
		}
		normalizedMAC, err := NormalizeMacAddress(net.MacRequest)
		if err != nil {
			cacheLog.Error(err, "failed to normalize MAC address", "mac", net.MacRequest, "pod", pod.Name, "namespace", pod.Namespace)
			continue
		}
		if _, dup := seen[normalizedMAC]; dup {
			continue
		}
		seen[normalizedMAC] = struct{}{}
		macs = append(macs, normalizedMAC)
	}

	return macs
}

// IsKubevirtOwned returns true if the Pod is a virt-launcher Pod.
// These Pods' MACs are already tracked through the VMI collision controller.
func IsKubevirtOwned(pod *corev1.Pod) bool {
	return pod.Labels[kubevirtv1.AppLabel] == "virt-launcher"
}
