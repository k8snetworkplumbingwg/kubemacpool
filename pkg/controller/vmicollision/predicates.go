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
	"maps"

	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func collisionRelevantChanges() predicate.TypedPredicate[*kubevirtv1.VirtualMachineInstance] {
	return predicate.TypedFuncs[*kubevirtv1.VirtualMachineInstance]{
		CreateFunc: func(e event.TypedCreateEvent[*kubevirtv1.VirtualMachineInstance]) bool {
			return true
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]) bool {
			oldVMI := e.ObjectOld
			newVMI := e.ObjectNew

			if oldVMI.Status.Phase != newVMI.Status.Phase {
				return true
			}

			if macAddressesChanged(oldVMI, newVMI) {
				return true
			}

			if migrationStateChanged(oldVMI, newVMI) {
				return true
			}

			return false
		},
		DeleteFunc: func(e event.TypedDeleteEvent[*kubevirtv1.VirtualMachineInstance]) bool {
			return true
		},
	}
}

func macAddressesChanged(oldVMI, newVMI *kubevirtv1.VirtualMachineInstance) bool {
	oldMACs := extractMACAddresses(oldVMI)
	newMACs := extractMACAddresses(newVMI)

	return !maps.Equal(oldMACs, newMACs)
}

func extractMACAddresses(vmi *kubevirtv1.VirtualMachineInstance) map[string]struct{} {
	macs := make(map[string]struct{})
	for _, iface := range vmi.Status.Interfaces {
		if iface.MAC != "" {
			macs[NormalizeMacAddress(iface.MAC)] = struct{}{}
		}
	}
	return macs
}

func migrationStateChanged(oldVMI, newVMI *kubevirtv1.VirtualMachineInstance) bool {
	oldSourceUID := getSourceMigrationUID(oldVMI)
	newSourceUID := getSourceMigrationUID(newVMI)

	oldTargetUID := getTargetMigrationUID(oldVMI)
	newTargetUID := getTargetMigrationUID(newVMI)

	return oldSourceUID != newSourceUID || oldTargetUID != newTargetUID
}

func getSourceMigrationUID(vmi *kubevirtv1.VirtualMachineInstance) string {
	if vmi.Status.MigrationState != nil && vmi.Status.MigrationState.SourceState != nil {
		return string(vmi.Status.MigrationState.SourceState.MigrationUID)
	}
	return ""
}

func getTargetMigrationUID(vmi *kubevirtv1.VirtualMachineInstance) string {
	if vmi.Status.MigrationState != nil && vmi.Status.MigrationState.TargetState != nil {
		return string(vmi.Status.MigrationState.TargetState.MigrationUID)
	}
	return ""
}
