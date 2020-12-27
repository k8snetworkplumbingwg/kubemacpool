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
	"math/rand"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	kubevirt "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
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
func (r *ReconcilePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	//used for multi thread log separation
	reconcileRequestId := rand.Int()
	logger := log.WithName("Reconcile").WithValues("RequestId", reconcileRequestId, "virtualMachineName", request.Name, "virtualMachineNamespace", request.Namespace)
	logger.Info("got a virtual machine event in the controller")

	instance := &kubevirt.VirtualMachine{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("vm not found. aborting reconcile")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, errors.Wrap(err, "Failed to read the request object")
	}

	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		vmShouldBeManaged, err := r.poolManager.IsNamespaceManaged(instance.GetNamespace())
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "Failed to check if vm is managed")
		}
		if !vmShouldBeManaged {
			logger.Info("vm is not managed by kubemacpool")
			return reconcile.Result{}, nil
		}

		logger.V(1).Info("vm create/update event")
		// The object is not being deleted, so we can set the macs to allocated
		err = r.poolManager.MarkVMAsReady(instance, logger)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "Failed to reconcile kubemacpool after virtual machine's creation event")
		}
	} else {
		// The object is being deleted
		logger.V(1).Info("The VM is being marked for deletion")
		err = r.removeFinalizerAndReleaseMac(&request, logger)

		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "Failed to reconcile kubemacpool after virtual machine's deletion event")
		}
	}

	return reconcile.Result{}, err
}

func (r *ReconcilePolicy) removeFinalizerAndReleaseMac(request *reconcile.Request, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("removeFinalizerAndReleaseMac")

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		virtualMachine := &kubevirt.VirtualMachine{}
		err := r.Get(context.TODO(), request.NamespacedName, virtualMachine)
		if err != nil {
			// Error reading the object - requeue the request.
			return errors.Wrap(err, "Failed to refresh the vm object")
		}

		if !helper.ContainsString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
			return nil
		}

		// our finalizer is present, so lets handle our external dependency
		logger.Info("The VM contains the finalizer. Releasing mac")
		err = r.poolManager.ReleaseVirtualMachineMac(virtualMachine, parentLogger)
		if err != nil {
			return errors.Wrap(err, "failed to release mac")
		}

		// remove our finalizer from the list and update it.
		virtualMachine.ObjectMeta.Finalizers = helper.RemoveString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)
		logger.V(1).Info("Removed the finalizer from the VM instance")

		err = r.Update(context.Background(), virtualMachine)

		return err
	})

	if err != nil {
		return errors.Wrap(err, "Failed to updated VM instance with finalizer removal")
	}

	logger.Info("Successfully updated VM instance with finalizer removal")

	return nil
}
