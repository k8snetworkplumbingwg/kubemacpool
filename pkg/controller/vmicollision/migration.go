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
	"fmt"

	"github.com/go-logr/logr"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

// filterOutDecentralizedMigrations filters out VMIs that share MACs due to decentralized migration, returning only real collisions
func (r *VMICollisionReconciler) filterOutDecentralizedMigrations(reconciledVMI *kubevirtv1.VirtualMachineInstance, duplicates []*kubevirtv1.VirtualMachineInstance, logger logr.Logger) []*kubevirtv1.VirtualMachineInstance {
	reconciledSourceUID, reconciledTargetUID := extractMigrationUIDs(reconciledVMI)
	if reconciledSourceUID == "" && reconciledTargetUID == "" {
		// No active migration on reconciled VMI, all duplicates are real collisions
		return duplicates
	}

	var actualCollisions []*kubevirtv1.VirtualMachineInstance
	for _, duplicate := range duplicates {
		if sharesActiveMigration(reconciledSourceUID, reconciledTargetUID, duplicate) {
			logger.V(1).Info("Filtering out VMI sharing migration",
				"vmi", fmt.Sprintf("%s/%s", duplicate.Namespace, duplicate.Name),
				"sourceUID", reconciledSourceUID,
				"targetUID", reconciledTargetUID)
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

func extractMigrationUIDs(vmi *kubevirtv1.VirtualMachineInstance) (sourceUID, targetUID string) {
	if vmi.Status.MigrationState == nil {
		return "", ""
	}

	if vmi.Status.MigrationState.SourceState != nil {
		sourceUID = string(vmi.Status.MigrationState.SourceState.MigrationUID)
	}

	if vmi.Status.MigrationState.TargetState != nil {
		targetUID = string(vmi.Status.MigrationState.TargetState.MigrationUID)
	}

	return sourceUID, targetUID
}

// sharesActiveMigration checks if a VMI shares the same migration by matching UIDs in both states
// Returns true only if EITHER source or target migration UIDs match
// This ensures they are part of the exact same active migration
func sharesActiveMigration(reconciledSourceUID, reconciledTargetUID string, vmi *kubevirtv1.VirtualMachineInstance) bool {
	if vmi.Status.MigrationState == nil {
		return false
	}

	duplicateSourceUID, duplicateTargetUID := extractMigrationUIDs(vmi)

	if reconciledSourceUID != "" && reconciledSourceUID == duplicateSourceUID {
		return true
	}
	if reconciledTargetUID != "" && reconciledTargetUID == duplicateTargetUID {
		return true
	}

	return false
}
