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
	"fmt"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

// filterOutDecentralizedMigrations filters out VMIs that share MACs due to decentralized migration, returning only real collisions
func (r *VMIReconciler) filterOutDecentralizedMigrations(reconciledVMI *kubevirtv1.VirtualMachineInstance, duplicates []*kubevirtv1.VirtualMachineInstance, logger logr.Logger) []*kubevirtv1.VirtualMachineInstance {
	migrationUIDs := extractMigrationUIDs(reconciledVMI)
	if migrationUIDs.Len() == 0 {
		return duplicates
	}

	var actualCollisions []*kubevirtv1.VirtualMachineInstance
	for _, duplicate := range duplicates {
		if sharesMigrationUID(migrationUIDs, duplicate) {
			logger.V(1).Info("Filtering out VMI sharing migration",
				"vmi", fmt.Sprintf("%s/%s", duplicate.Namespace, duplicate.Name),
				"migrationUIDs", sets.List(migrationUIDs))
			continue
		}

		actualCollisions = append(actualCollisions, duplicate)
	}

	if len(actualCollisions) < len(duplicates) {
		logger.Info("Filtered out decentralized migrations from duplicates",
			"originalCount", len(duplicates),
			"actualCollisions", len(actualCollisions),
			"filteredOut", len(duplicates)-len(actualCollisions))
	}

	return actualCollisions
}

func extractMigrationUIDs(vmi *kubevirtv1.VirtualMachineInstance) sets.Set[types.UID] {
	uids := sets.New[types.UID]()
	state := vmi.Status.MigrationState
	if state == nil {
		return uids
	}

	// The local migration UID survives successful target cleanup of the nested states.
	uids.Insert(state.MigrationUID)
	if state.SourceState != nil {
		uids.Insert(state.SourceState.MigrationUID)
	}
	if state.TargetState != nil {
		uids.Insert(state.TargetState.MigrationUID)
	}

	uids.Delete(types.UID(""))
	return uids
}

func sharesMigrationUID(uids sets.Set[types.UID], vmi *kubevirtv1.VirtualMachineInstance) bool {
	for uid := range extractMigrationUIDs(vmi) {
		if uids.Has(uid) {
			return true
		}
	}
	return false
}
