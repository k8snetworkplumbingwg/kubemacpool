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
	"k8s.io/apimachinery/pkg/util/sets"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	netutils "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/utils"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var cacheLog = logf.Log.WithName("MACCollision Cache")

const (
	// MacAddressIndexName is the index name for MAC address lookups
	MacAddressIndexName = "status.interfaces.mac"

	// PodMacAddressIndexName is the index name for Pod MAC address lookups.
	PodMacAddressIndexName = "annotations.networks.mac"
)

// StripVMIForCollisionDetection keeps only:
// metadata (minimal), spec.networks, spec.domain.devices.interfaces,
// status.interfaces, status.phase, status.migrationState.
// Everything else is stripped to reduce cache memory.
func StripVMIForCollisionDetection(obj interface{}) (interface{}, error) {
	vmi, ok := obj.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return obj, nil
	}

	stripped := &kubevirtv1.VirtualMachineInstance{
		TypeMeta: vmi.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:              vmi.Name,
			Namespace:         vmi.Namespace,
			UID:               vmi.UID,
			DeletionTimestamp: vmi.DeletionTimestamp,
		},
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Networks: vmi.Spec.Networks,
			Domain: kubevirtv1.DomainSpec{
				Devices: kubevirtv1.Devices{
					Interfaces: vmi.Spec.Domain.Devices.Interfaces,
				},
			},
		},
		Status: kubevirtv1.VirtualMachineInstanceStatus{
			Phase:          vmi.Status.Phase,
			Interfaces:     vmi.Status.Interfaces,
			MigrationState: vmi.Status.MigrationState,
		},
	}

	return stripped, nil
}

// IndexVMIByMAC returns MACs from a VMI that KubeMacPool would allocate.
// Unsupported pod-network interfaces are omitted so they are not treated as
// cluster-wide collisions.
func IndexVMIByMAC(obj client.Object) []string {
	vmi, ok := obj.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return nil
	}

	return supportedMACsFromVMI(vmi)
}

// supportedMACsFromVMI returns normalized status MACs for interfaces KubeMacPool allocates for.
func supportedMACsFromVMI(vmi *kubevirtv1.VirtualMachineInstance) []string {
	networks := make(map[string]kubevirtv1.Network, len(vmi.Spec.Networks))
	for _, network := range vmi.Spec.Networks {
		networks[network.Name] = network
	}

	managedInterfaces := sets.New[string]()
	for _, iface := range vmi.Spec.Domain.Devices.Interfaces {
		if pool_manager.IsInterfaceSupported(iface, networks) {
			managedInterfaces.Insert(iface.Name)
		}
	}

	var macs []string
	for _, statusIface := range vmi.Status.Interfaces {
		if !managedInterfaces.Has(statusIface.Name) || statusIface.MAC == "" {
			continue
		}
		normalizedMAC, err := NormalizeMacAddress(statusIface.MAC)
		if err != nil {
			cacheLog.Error(err, "failed to normalize MAC address", "mac", statusIface.MAC, "vmi", vmi.Name, "namespace", vmi.Namespace)
			continue
		}
		macs = append(macs, normalizedMAC)
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

	seen := sets.New[string]()
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
		if seen.Has(normalizedMAC) {
			continue
		}
		seen.Insert(normalizedMAC)
		macs = append(macs, normalizedMAC)
	}

	return macs
}

// IsKubevirtOwned returns true if the Pod is a virt-launcher Pod.
// These Pods' MACs are already tracked through the VMI collision controller.
func IsKubevirtOwned(pod *corev1.Pod) bool {
	return pod.Labels[kubevirtv1.AppLabel] == "virt-launcher"
}
