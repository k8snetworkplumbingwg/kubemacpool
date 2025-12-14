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

// filterOutDecentralizedMigrations filters out VMIs that share MACs due to live migration, returning only real collisions
// TODO: Implement Decentralized migration awareness in next commit
func (r *VMICollisionReconciler) filterOutDecentralizedMigrations(reconciledVMI *kubevirtv1.VirtualMachineInstance, duplicates []*kubevirtv1.VirtualMachineInstance, logger logr.Logger) []*kubevirtv1.VirtualMachineInstance {
	logger.V(1).Info("Decentralized Migration filtering not yet implemented, treating all duplicates as collisions",
		"vmi", fmt.Sprintf("%s/%s", reconciledVMI.Namespace, reconciledVMI.Name),
		"duplicateCount", len(duplicates))

	return duplicates
}
