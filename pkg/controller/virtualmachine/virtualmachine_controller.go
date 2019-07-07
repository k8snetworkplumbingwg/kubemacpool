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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/intel/multus-cni/logging"
	kubevirt "kubevirt.io/kubevirt/pkg/api/v1"

	pool_manager "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
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

	log.V(1).Info("got a virtual machine event in the controller")
	instance := &kubevirt.VirtualMachine{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {

			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	myFinalizerName := "VMFinalizer"
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object.
		logging.Printf(logging.DebugLevel, "The object is not being deleted")

		if !containsString(instance.ObjectMeta.Finalizers, myFinalizerName) {
			logging.Printf(logging.DebugLevel, "The object does not have a the finalizer")
			instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, myFinalizerName)
			if err = r.Update(context.Background(), instance); err != nil {
				logging.Printf(logging.ErrorLevel, "failed to update the VM %s with the new finalizer: %v", instance.GetName(), err)
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		logging.Printf(logging.DebugLevel, "The VM is being deleted")
		if containsString(instance.ObjectMeta.Finalizers, myFinalizerName) {
			logging.Printf(logging.DebugLevel, "The VM contains the finalizer")
			// our finalizer is present, so lets handle our external dependency
			logging.Printf(logging.DebugLevel, "Releasing the mac")
			err := r.poolManager.ReleaseVirtualMachineMac(fmt.Sprintf("%s/%s", request.Namespace, request.Name))
			if err != nil {
				logging.Printf(logging.ErrorLevel, "failed to release mac from VM %s: %v", request.NamespacedName, err)
			}

			// remove our finalizer from the list and update it.
			logging.Printf(logging.DebugLevel, "removing finalizer")
			instance.ObjectMeta.Finalizers = removeString(instance.ObjectMeta.Finalizers, myFinalizerName)
			if err = r.Update(context.Background(), instance); err != nil {
				logging.Printf(logging.ErrorLevel, "failed to remove finalizer from VM %s : %v ", instance.GetName(), err)
				return reconcile.Result{}, err
			}
		}
		// Our finalizer has finished, so the reconciler can do nothing.
		return reconcile.Result{}, nil
	}

	return reconcile.Result{}, nil
}

//helper functions
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
