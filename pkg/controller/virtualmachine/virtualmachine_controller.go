/*
Copyright 2019 The KubeMacPool Authors.

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

package virtualmachine

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	kubevirt "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	helper "github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

var log = logf.Log.WithName("VirtualMachine Controller")

// Add creates a new Policy Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	if poolManager.IsKubevirtEnabled() {
		return add(mgr, newReconciler(mgr, poolManager))
	}

	return nil
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, poolManager *pool_manager.PoolManager) reconcile.Reconciler {
	return &ReconcilePolicy{Client: mgr.GetClient(), scheme: mgr.GetScheme(), poolManager: poolManager}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("virtualmachine-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to Pod
	err = c.Watch(&source.Kind{Type: &kubevirt.VirtualMachine{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcilePolicy{}

// ReconcilePolicy reconciles a Policy object
type ReconcilePolicy struct {
	client.Client
	scheme      *runtime.Scheme
	poolManager *pool_manager.PoolManager
}

// Reconcile reads that state of the cluster for a virtual machine object and makes changes based on the state
func (r *ReconcilePolicy) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	//used for multi thread log separation
	reconcileRequestId := rand.Intn(100000)
	logger := log.WithName("Reconcile").WithValues("RequestId", reconcileRequestId, "vmFullName", fmt.Sprintf("vm/%s/%s", request.Namespace, request.Name))
	logger.Info("got a virtual machine event in the controller")

	instance := &kubevirt.VirtualMachine{}
	err := r.Get(ctx, request.NamespacedName, instance)
	vmNotFound := apierrors.IsNotFound(err)
	if vmNotFound {
		logger.V(1).Info("vm not found. Assuming vm is deleted")
		err := r.poolManager.ReleaseAllVirtualMachineMacs(pool_manager.VmNamespacedFromRequest(&request), logger)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "failed to release vm macs")
		}
		return reconcile.Result{}, nil
	} else if err != nil {
		// Error reading the object - requeue the request.
		return reconcile.Result{}, errors.Wrap(err, "Failed to read the request object")
	}

	if !pool_manager.IsVirtualMachineDeletionInProgress(instance) {
		vmShouldBeManaged, err := r.poolManager.IsVirtualMachineManaged(request.Namespace)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "Failed to check if vm is managed")
		}
		if !vmShouldBeManaged {
			logger.Info("vm is not managed by kubemacpool")
			return reconcile.Result{}, nil
		}

		logger.V(1).Info("vm create/update event")
		// The object is not being deleted, so we can set the macs to allocated
		latestPersistedTransactionTimeStamp, err := pool_manager.GetTransactionTimestampAnnotationFromVm(instance)
		if err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("TransactionTimestampAnnotation didn't persist yet, aborting reconcile")
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, errors.Wrap(err, "Failed to reconcile kubemacpool after virtual machine's creation/update event")
		}

		err = r.poolManager.MarkVMAsReady(instance, &latestPersistedTransactionTimeStamp, logger)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "Failed to reconcile kubemacpool after virtual machine's creation/update event")
		}
	} else {
		// The object is being deleted
		logger.V(1).Info("The VM is being marked for deletion")
		err = r.freeVmFromMacPool(ctx, &request, logger)

		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "Failed to reconcile kubemacpool after virtual machine's deletion event")
		}
	}

	return reconcile.Result{}, err
}

func (r *ReconcilePolicy) freeVmFromMacPool(ctx context.Context, request *reconcile.Request, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("freeVmFromMacPool")

	err := r.poolManager.ReleaseAllVirtualMachineMacs(pool_manager.VmNamespacedFromRequest(request), parentLogger)
	if err != nil {
		return errors.Wrap(err, "failed to release vm macs")
	}

	virtualMachine := &kubevirt.VirtualMachine{}
	err = r.Get(ctx, request.NamespacedName, virtualMachine)
	if err != nil {
		if isVmDeletionAlreadyPersistedByFormerUpdates(err, logger) {
			return nil
		}
		// Error reading the object - requeue the request.
		return errors.Wrap(err, "Failed to refresh the vm object")
	}

	err = r.removeLegacyFinalizerIfPresent(ctx, virtualMachine, parentLogger)
	if err != nil {
		return errors.Wrap(err, "failed to remove the finalizer")
	}

	return nil
}

// the finalizer is no longer used by kubemacpool, but may exist if the vm was created with old kubemacpool version
// removeLegacyFinalizerIfPresent removes the finalizer if exists to allow the vm to be deleted.
func (r *ReconcilePolicy) removeLegacyFinalizerIfPresent(ctx context.Context, virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("removeLegacyFinalizerIfPresent")
	if helper.ContainsString(virtualMachine.GetFinalizers(), pool_manager.RuntimeObjectFinalizerName) {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// our finalizer is present, so lets handle our external dependency
			logger.Info("The VM contains the finalizer")
			// remove our finalizer from the list and update it.
			virtualMachine.ObjectMeta.Finalizers = helper.RemoveString(virtualMachine.GetFinalizers(), pool_manager.RuntimeObjectFinalizerName)

			return r.Update(ctx, virtualMachine)
		})

		if err != nil {
			return errors.Wrap(err, "Failed to updated VM instance with finalizer removal")
		}

		logger.Info("Successfully updated VM instance with finalizer removal")
	} else {
		logger.V(1).Info("legacy finalizer does not exist on VM instance")
	}
	return nil
}

func isVmDeletionAlreadyPersistedByFormerUpdates(err error, parentLogger logr.Logger) bool {
	if apierrors.IsNotFound(err) {
		parentLogger.V(1).Info("vm not found")
		return true
	}
	return false
}
