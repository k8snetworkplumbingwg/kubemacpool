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

package vmicollision

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var cacheLog = logf.Log.WithName("VMICollision Cache")

const (
	// MacAddressIndexName is the index name for MAC address lookups
	MacAddressIndexName = "status.interfaces.mac"
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
