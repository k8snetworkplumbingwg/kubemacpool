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
	"context"
	"math/rand"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var log = logf.Log.WithName("VMICollision Controller")

// VMICollisionReconciler watches VirtualMachineInstance objects and detects MAC address collisions
type VMICollisionReconciler struct {
	client.Client
	poolManager *pool_manager.PoolManager
}

// SetupWithManager sets up the controller with the Manager.
func SetupWithManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	// Create the reconciler
	r := &VMICollisionReconciler{
		Client:      mgr.GetClient(),
		poolManager: poolManager,
	}

	// Register the MAC address indexer
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&kubevirtv1.VirtualMachineInstance{},
		MacAddressIndexName,
		IndexVMIByMAC,
	); err != nil {
		return errors.Wrap(err, "failed to setup MAC address indexer")
	}

	log.Info("Successfully registered MAC address indexer for VMI collision detection")

	// TODO: Build controller with watches and event handlers
	// This will be implemented in subsequent commits

	_ = r // Suppress unused warning until we register the controller
	return nil
}

// Reconcile handles VMI reconciliation for collision detection
func (r *VMICollisionReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	reconcileRequestId := rand.Intn(100000)
	logger := log.WithName("Reconcile").WithValues("RequestId", reconcileRequestId, "vmi-collision-detect", req.NamespacedName)
	logger.V(1).Info("Reconciling VMI for collision detection")

	// Fetch the VMI
	vmi := &kubevirtv1.VirtualMachineInstance{}
	err := r.Get(ctx, req.NamespacedName, vmi)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// VMI was deleted
			logger.V(1).Info("VMI not found, assuming deleted")
			// TODO: Handle VMI deletion (remove from duplicate tracking)
			// This will be implemented when PoolManager methods are added
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to get VMI")
		return reconcile.Result{}, errors.Wrapf(err, "failed to get VMI %s", req.NamespacedName)
	}

	// Check if namespace is managed by kubemacpool
	isManaged, err := r.poolManager.IsVirtualMachineManaged(vmi.Namespace)
	if err != nil {
		logger.Error(err, "Failed to check if namespace is managed")
		return reconcile.Result{}, errors.Wrapf(err, "failed to check if namespace %s is managed", vmi.Namespace)
	}
	if !isManaged {
		logger.V(1).Info("VMI namespace not managed by kubemacpool, skipping")
		return reconcile.Result{}, nil
	}

	// TODO: Implement collision detection logic
	// This will be implemented in subsequent commits:
	// - Check if VMI is Running
	// - Find other VMIs with same MAC addresses
	// - Update duplicate tracking in PoolManager
	// - Update metrics

	r.checkMACCollisions(ctx, vmi, logger)

	return reconcile.Result{}, nil
}

// checkMACCollisions detects MAC collisions for the given VMI
func (r *VMICollisionReconciler) checkMACCollisions(ctx context.Context, vmi *kubevirtv1.VirtualMachineInstance, logger logr.Logger) {
	// TODO: Implement collision detection logic
	// This will be implemented in subsequent commits
	logger.V(1).Info("Collision detection not yet implemented")
}
