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

package controller

import (
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller/vmicollision"
	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

func init() {
	AddToManagerFuncs = append(AddToManagerFuncs, ControllerAdder{
		Name: "vmicollision-controller",
		Add:  addVMICollisionController,
	})
}

func addVMICollisionController(mgr manager.Manager, poolManager *pool_manager.PoolManager) (bool, error) {
	if !poolManager.IsKubevirtEnabled() {
		return false, nil
	}
	if err := vmicollision.SetupWithManager(mgr, poolManager); err != nil {
		return false, err
	}
	return true, nil
}
