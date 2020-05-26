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
	logger := log.WithName("Reconcile").WithValues("virtualMachineName", request.Name, "virtualMachineNamespace", request.Namespace)
	logger.V(1).Info("got a virtual machine event in the controller")

	instanceOptedIn, err := r.poolManager.IsVmInstanceOptedIn(request.Namespace)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "failed to check opt-in selection for vm")
	}
	if !instanceOptedIn {
		logger.V(1).Info("vm is opted-out from kubemacpool")
		return reconcile.Result{}, nil
	}

	instance := &kubevirt.VirtualMachine{}
	err = r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, errors.Wrap(err, "Failed to read the request object")
	}

	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.V(1).Info("The VM is not being deleted")
		// The object is not being deleted, so if it does not have a finalizer,
		// then lets add the finalizer and update the object.
		if !helper.ContainsString(instance.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
			err = r.addFinalizerAndUpdate(&request)
		}

		return reconcile.Result{}, err
	}
	// The object is being deleted
	logger.V(1).Info("The VM is being marked for deletion")
	err = r.removeFinalizerAndReleaseMac(instance, &request)
	return reconcile.Result{}, err
}

func (r *ReconcilePolicy) addFinalizerAndUpdate(request *reconcile.Request) error {
	logger := log.WithName("addFinalizerAndUpdate").WithValues("virtualMachineName", request.Name, "virtualMachineNamespace", request.Namespace)
	logger.V(1).Info("The VM does not have a finalizer")

	virtualMachine := &kubevirt.VirtualMachine{}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh vm instance
		err := r.Get(context.TODO(), request.NamespacedName, virtualMachine)
		if err != nil {
			return errors.Wrap(err, "Failed to refresh vm instance")
		}
		virtualMachine.ObjectMeta.Finalizers = append(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)

		err = r.Update(context.Background(), virtualMachine)

		return err
	})

	if err != nil {
		return errors.Wrap(err, "failed to add the finalizer from the VM")
	}

	logger.V(1).Info("Finalizer was added to the VM")

	return r.poolManager.MarkVMAsReady(virtualMachine)
}

func (r *ReconcilePolicy) removeFinalizerAndReleaseMac(virtualMachine *kubevirt.VirtualMachine, request *reconcile.Request) error {
	logger := log.WithName("removeFinalizerAndReleaseMac").WithValues("virtualMachineName", request.Name, "virtualMachineNamespace", request.Namespace)

	if !helper.ContainsString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
		return nil
	}
	// our finalizer is present, so lets handle our external dependency
	logger.V(1).Info("The VM contains the finalizer. Releasing mac")
	err := r.poolManager.ReleaseVirtualMachineMac(virtualMachine)
	if err != nil {
		return errors.Wrap(err, "failed to release mac")
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// refresh vm instance
		err = r.Get(context.TODO(), request.NamespacedName, virtualMachine)
		if err != nil {
			return errors.Wrap(err, "Failed to refresh vm instance")
		}

		// remove our finalizer from the list and update it.
		logger.V(1).Info("removing finalizers")
		virtualMachine.ObjectMeta.Finalizers = helper.RemoveString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)

		err = r.Update(context.Background(), virtualMachine)

		return err
	})

	if err != nil {
		return errors.Wrap(err, "failed to remove the finalizer from the VM")
	}

	logger.V(1).Info("Removed the finalizer from the VM")

	return nil
}
