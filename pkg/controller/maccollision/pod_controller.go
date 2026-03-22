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
	"context"
	"math/rand"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var podLog = logf.Log.WithName("MACCollision Pod Controller")

// PodReconciler watches Pod objects and detects MAC address collisions.
type PodReconciler struct {
	client.Client
	poolManager PoolManagerInterface
}

// SetupPodControllerWithManager sets up the Pod collision controller with the Manager.
func SetupPodControllerWithManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	r := &PodReconciler{
		Client:      mgr.GetClient(),
		poolManager: poolManager,
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.Pod{},
		PodMacAddressIndexName,
		IndexPodByMAC,
	); err != nil {
		return errors.Wrap(err, "failed to setup Pod MAC address indexer")
	}

	podLog.Info("Successfully registered MAC address indexer for Pod collision detection")

	// TODO: Build controller with watches and event handlers
	// This will be implemented in subsequent commits

	_ = r
	return nil
}

// Reconcile handles Pod reconciliation for collision detection.
func (r *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	reconcileRequestId := rand.Intn(100000)
	logger := podLog.WithName("Reconcile").WithValues("RequestId", reconcileRequestId, "pod-collision-detect", req.NamespacedName)
	logger.V(1).Info("Reconciling Pod for collision detection")

	pod := &corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("Pod not found, assuming deleted")
			// TODO: Handle Pod deletion (remove from collision tracking)
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return reconcile.Result{}, errors.Wrapf(err, "failed to get Pod %s", req.NamespacedName)
	}

	if IsKubevirtOwned(pod) {
		logger.V(1).Info("Pod is virt-launcher, skipping (tracked via VMI controller)")
		return reconcile.Result{}, nil
	}

	isManaged, err := r.poolManager.IsPodManaged(pod.Namespace)
	if err != nil {
		logger.Error(err, "Failed to check if namespace is managed")
		return reconcile.Result{}, errors.Wrapf(err, "failed to check if namespace %s is managed", pod.Namespace)
	}
	if !isManaged {
		logger.V(1).Info("Pod namespace not managed by kubemacpool, skipping")
		return reconcile.Result{}, nil
	}

	// TODO: Implement collision detection logic
	// This will be implemented in subsequent commits:
	// - Check if Pod is Running
	// - Find other Pods with same MAC addresses
	// - Update collision tracking in PoolManager
	// - Emit collision events

	r.checkMACCollisions(ctx, pod, logger)

	return reconcile.Result{}, nil
}

func (r *PodReconciler) checkMACCollisions(ctx context.Context, pod *corev1.Pod, logger logr.Logger) {
	// TODO: Implement collision detection logic
	logger.V(1).Info("Collision detection not yet implemented")
}
