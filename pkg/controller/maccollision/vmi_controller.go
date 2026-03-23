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
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var log = logf.Log.WithName("MACCollision Controller")

// PoolManagerInterface defines the methods required by collision reconcilers.
type PoolManagerInterface interface {
	IsKubevirtEnabled() bool
	IsVirtualMachineManaged(namespace string) (bool, error)
	IsPodManaged(namespace string) (bool, error)
	UpdateCollisionsMap(objectRef pool_manager.ObjectReference, collisions map[string][]pool_manager.ObjectReference)
}

// VMIReconciler watches VirtualMachineInstance objects and detects MAC address collisions
type VMIReconciler struct {
	client.Client
	poolManager PoolManagerInterface
	recorder    record.EventRecorder
}

// SetupWithManager sets up the controller with the Manager.
func SetupWithManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	// Create the reconciler
	r := &VMIReconciler{
		Client:      mgr.GetClient(),
		poolManager: poolManager,
		recorder:    mgr.GetEventRecorderFor("kubemacpool"),
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

	c, err := controller.New("maccollision-vmi-controller", mgr, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return fmt.Errorf("failed to create MAC collision VMI controller: %w", err)
	}

	err = c.Watch(
		source.Kind(
			mgr.GetCache(),
			&kubevirtv1.VirtualMachineInstance{},
			&handler.TypedEnqueueRequestForObject[*kubevirtv1.VirtualMachineInstance]{},
			collisionRelevantChanges(),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to watch VMIs: %w", err)
	}

	log.Info("Successfully registered MAC collision VMI controller")
	return nil
}

// Reconcile handles VMI reconciliation for collision detection
func (r *VMIReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	reconcileRequestId := rand.Intn(100000)
	logger := log.WithName("Reconcile").WithValues("RequestId", reconcileRequestId, "vmi-collision-detect", req.NamespacedName)
	logger.V(1).Info("Reconciling VMI for collision detection")

	// Fetch the VMI
	vmi := &kubevirtv1.VirtualMachineInstance{}
	err := r.Get(ctx, req.NamespacedName, vmi)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("VMI not found, assuming deleted, removing from collision tracking")
			r.removeVMIFromAllCollisions(req.Namespace, req.Name)
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

	if err = r.checkMACCollisions(ctx, vmi, logger); err != nil {
		logger.Error(err, "Failed to check MAC collisions")
		return reconcile.Result{}, errors.Wrap(err, "failed to check MAC collisions")
	}

	return reconcile.Result{}, nil
}

// checkMACCollisions detects MAC collisions for the given VMI
func (r *VMIReconciler) checkMACCollisions(ctx context.Context, vmi *kubevirtv1.VirtualMachineInstance, logger logr.Logger) error {
	if vmi.Status.Phase != kubevirtv1.Running {
		logger.V(1).Info("VMI not running, removing from collision tracking", "phase", vmi.Status.Phase)
		r.removeVMIFromAllCollisions(vmi.Namespace, vmi.Name)
		return nil
	}

	macs := r.extractMACsFromVMI(vmi, logger)
	if len(macs) == 0 {
		logger.V(1).Info("VMI has no MAC addresses, skipping collision check")
		return nil
	}

	otherVMIsCollidingPerMAC := make(map[string][]*kubevirtv1.VirtualMachineInstance)
	for mac := range macs {
		collisionCandidates, err := r.filterVMIsWithMAC(ctx, mac, vmi.UID, logger)
		if err != nil {
			return errors.Wrapf(err, "failed to find VMIs with MAC %s", mac)
		}

		collisionCandidates = r.filterOutDecentralizedMigrations(vmi, collisionCandidates, logger)
		collisionCandidates = r.filterOutUnmanagedNamespaces(collisionCandidates, vmi.Namespace, logger)

		actualCollisions := collisionCandidates

		if len(actualCollisions) > 0 {
			logger.Info("Found MAC collision", "mac", mac, "collisionCount", len(actualCollisions))
			otherVMIsCollidingPerMAC[mac] = actualCollisions

			r.emitCollisionEvents(vmi, mac, actualCollisions)
		}
	}

	collisions := convertToObjectReferenceMap(otherVMIsCollidingPerMAC)
	r.poolManager.UpdateCollisionsMap(vmiObjectRef(vmi.Namespace, vmi.Name), collisions)

	return nil
}

// emitCollisionEvents emits Kubernetes events on all VMIs involved in a MAC collision
func (r *VMIReconciler) emitCollisionEvents(vmi *kubevirtv1.VirtualMachineInstance, mac string, collisions []*kubevirtv1.VirtualMachineInstance) {
	allVMIs := []string{fmt.Sprintf("%s/%s", vmi.Namespace, vmi.Name)}
	for _, duplicate := range collisions {
		allVMIs = append(allVMIs, fmt.Sprintf("%s/%s", duplicate.Namespace, duplicate.Name))
	}

	sort.Strings(allVMIs)

	// Universal message - same for all VMIs and better deduplication
	message := fmt.Sprintf("MAC %s: Collision between %s", mac, strings.Join(allVMIs, ", "))

	r.recorder.Event(vmi, corev1.EventTypeWarning, "MACCollision", message)

	for _, duplicate := range collisions {
		r.recorder.Event(duplicate, corev1.EventTypeWarning, "MACCollision", message)
	}
}

// findRunningVMIsWithMAC queries the indexer to find all Running VMIs with the given MAC address
// Excludes the VMI with the given UID (to avoid finding itself)
// Assumes MAC is already normalized
func (r *VMIReconciler) filterVMIsWithMAC(ctx context.Context, normalizedMAC string, excludeUID types.UID, _ logr.Logger) ([]*kubevirtv1.VirtualMachineInstance, error) {
	return listRunningVMIsByMACWithExcludeUID(ctx, r.Client, normalizedMAC, excludeUID)
}

// extractMACsFromVMI returns a set of normalized MAC addresses from a VMI's status interfaces
func (r *VMIReconciler) extractMACsFromVMI(vmi *kubevirtv1.VirtualMachineInstance, logger logr.Logger) map[string]struct{} {
	macs := make(map[string]struct{})
	for _, iface := range vmi.Status.Interfaces {
		if iface.MAC != "" {
			normalizedMAC, err := NormalizeMacAddress(iface.MAC)
			if err != nil {
				logger.Error(err, "failed to normalize MAC address", "mac", iface.MAC, "vmi", vmi.Name, "namespace", vmi.Namespace)
				continue
			}
			macs[normalizedMAC] = struct{}{}
		}
	}
	return macs
}

// filterOutUnmanagedNamespaces filters out VMIs from unmanaged namespaces, returning only collisions in managed namespaces
// reconciledNamespace is the namespace of the VMI being reconciled, which is known to be managed
func (r *VMIReconciler) filterOutUnmanagedNamespaces(collisionCandidates []*kubevirtv1.VirtualMachineInstance, reconciledNamespace string, logger logr.Logger) []*kubevirtv1.VirtualMachineInstance {
	managedNamespaces := map[string]struct{}{
		reconciledNamespace: {},
	}

	managedCollisions := filterManagedVMIs(collisionCandidates, managedNamespaces, r.poolManager, logger)

	if len(managedCollisions) < len(collisionCandidates) {
		logger.Info("Filtered out VMIs from unmanaged namespaces",
			"originalCount", len(collisionCandidates),
			"managedCollisions", len(managedCollisions),
			"filteredOut", len(collisionCandidates)-len(managedCollisions))
	}

	return managedCollisions
}

// convertToObjectReferenceMap converts a map of VMIs to a map of ObjectReferences
func convertToObjectReferenceMap(duplicates map[string][]*kubevirtv1.VirtualMachineInstance) map[string][]pool_manager.ObjectReference {
	collisions := make(map[string][]pool_manager.ObjectReference)
	for mac, vmis := range duplicates {
		refs := make([]pool_manager.ObjectReference, len(vmis))
		for i, v := range vmis {
			refs[i] = vmiObjectRef(v.Namespace, v.Name)
		}
		collisions[mac] = refs
	}
	return collisions
}

// removeVMIFromAllCollisions removes a VMI from all collision tracking
func (r *VMIReconciler) removeVMIFromAllCollisions(namespace, name string) {
	r.poolManager.UpdateCollisionsMap(vmiObjectRef(namespace, name), nil)
}

func vmiObjectRef(namespace, name string) pool_manager.ObjectReference {
	return pool_manager.ObjectReference{
		Type:      pool_manager.ObjectTypeVMI,
		Namespace: namespace,
		Name:      name,
	}
}
