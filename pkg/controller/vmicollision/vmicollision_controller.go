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

var log = logf.Log.WithName("VMICollision Controller")

// PoolManagerInterface defines the methods required by VMICollisionReconciler
type PoolManagerInterface interface {
	IsVirtualMachineManaged(namespace string) (bool, error)
}

// VMICollisionReconciler watches VirtualMachineInstance objects and detects MAC address collisions
type VMICollisionReconciler struct {
	client.Client
	poolManager PoolManagerInterface
	recorder    record.EventRecorder
}

// SetupWithManager sets up the controller with the Manager.
func SetupWithManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	// Create the reconciler
	r := &VMICollisionReconciler{
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

	c, err := controller.New("vmicollision-controller", mgr, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return fmt.Errorf("failed to create VMI collision controller: %w", err)
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

	log.Info("Successfully registered VMI collision controller")
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

	if err = r.checkMACCollisions(ctx, vmi, logger); err != nil {
		logger.Error(err, "Failed to check MAC collisions")
		return reconcile.Result{}, errors.Wrap(err, "failed to check MAC collisions")
	}

	return reconcile.Result{}, nil
}

// checkMACCollisions detects MAC collisions for the given VMI
func (r *VMICollisionReconciler) checkMACCollisions(ctx context.Context, vmi *kubevirtv1.VirtualMachineInstance, logger logr.Logger) error {
	if vmi.Status.Phase != kubevirtv1.Running {
		logger.V(1).Info("VMI not running, skipping collision check", "phase", vmi.Status.Phase)
		return nil
	}

	macs := r.extractMACsFromVMI(vmi, logger)
	if len(macs) == 0 {
		logger.V(1).Info("VMI has no MAC addresses, skipping collision check")
		return nil
	}

	allDuplicates := make(map[string][]*kubevirtv1.VirtualMachineInstance)
	for mac := range macs {
		collisionCandidates, err := r.findRunningVMIsWithMAC(ctx, mac, vmi.UID, logger)
		if err != nil {
			return errors.Wrapf(err, "failed to find VMIs with MAC %s", mac)
		}

		collisionCandidates = r.filterOutDecentralizedMigrations(vmi, collisionCandidates, logger)
		collisionCandidates = r.filterOutUnmanagedNamespaces(collisionCandidates, vmi.Namespace, logger)

		actualCollisions := collisionCandidates

		if len(actualCollisions) > 0 {
			logger.Info("Found MAC collision", "mac", mac, "collisionCount", len(actualCollisions))
			allDuplicates[mac] = actualCollisions

			r.emitCollisionEvents(vmi, mac, actualCollisions)
		}
	}

	return nil
}

// emitCollisionEvents emits Kubernetes events on all VMIs involved in a MAC collision
func (r *VMICollisionReconciler) emitCollisionEvents(vmi *kubevirtv1.VirtualMachineInstance, mac string, collisions []*kubevirtv1.VirtualMachineInstance) {
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
func (r *VMICollisionReconciler) findRunningVMIsWithMAC(ctx context.Context, NormalizedMAC string, excludeUID types.UID, logger logr.Logger) ([]*kubevirtv1.VirtualMachineInstance, error) {
	// Query the indexer
	vmiList := &kubevirtv1.VirtualMachineInstanceList{}
	if err := r.List(ctx, vmiList, client.MatchingFields{MacAddressIndexName: NormalizedMAC}); err != nil {
		return nil, errors.Wrap(err, "failed to list VMIs by MAC")
	}

	// Filter to only Running VMIs (excluding the given UID)
	var runningVMIs []*kubevirtv1.VirtualMachineInstance
	for i := range vmiList.Items {
		vmi := &vmiList.Items[i]

		// Skip the VMI itself
		if vmi.UID == excludeUID {
			continue
		}

		// Only consider Running VMIs
		if vmi.Status.Phase != kubevirtv1.Running {
			continue
		}

		runningVMIs = append(runningVMIs, vmi)
	}

	return runningVMIs, nil
}

// extractMACsFromVMI returns a set of normalized MAC addresses from a VMI's status interfaces
func (r *VMICollisionReconciler) extractMACsFromVMI(vmi *kubevirtv1.VirtualMachineInstance, logger logr.Logger) map[string]struct{} {
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
func (r *VMICollisionReconciler) filterOutUnmanagedNamespaces(collisionCandidates []*kubevirtv1.VirtualMachineInstance, reconciledNamespace string, logger logr.Logger) []*kubevirtv1.VirtualMachineInstance {
	managedNamespaces := map[string]struct{}{
		reconciledNamespace: {},
	}

	var managedCollisions []*kubevirtv1.VirtualMachineInstance

	for _, candidate := range collisionCandidates {
		// Check if we've already verified this namespace
		if _, alreadyChecked := managedNamespaces[candidate.Namespace]; alreadyChecked {
			managedCollisions = append(managedCollisions, candidate)
			continue
		}

		// Check if namespace is managed
		isManaged, err := r.poolManager.IsVirtualMachineManaged(candidate.Namespace)
		if err != nil {
			logger.Error(err, "Failed to check if namespace is managed, including VMI in collisions",
				"namespace", candidate.Namespace,
				"vmi", candidate.Name)
			// Include the VMI in collisions on error (fail-safe)
			managedCollisions = append(managedCollisions, candidate)
			managedNamespaces[candidate.Namespace] = struct{}{}
			continue
		}

		if !isManaged {
			logger.V(1).Info("Filtering out VMI from unmanaged namespace",
				"namespace", candidate.Namespace,
				"vmi", candidate.Name)
			continue
		}

		// Cache this namespace as managed
		managedNamespaces[candidate.Namespace] = struct{}{}
		managedCollisions = append(managedCollisions, candidate)
	}

	if len(managedCollisions) < len(collisionCandidates) {
		logger.Info("Filtered out VMIs from unmanaged namespaces",
			"originalCount", len(collisionCandidates),
			"managedCollisions", len(managedCollisions),
			"filteredOut", len(collisionCandidates)-len(managedCollisions))
	}

	return managedCollisions
}
