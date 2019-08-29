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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pool_manager "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
	helper "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/utils"
	kubevirt "kubevirt.io/kubevirt/pkg/api/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
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
	log.V(1).Info("got a virtual machine event in the controller",
		"virtualMachineName", request.Name,
		"virtualMachineNamespace", request.Namespace)
	instance := &kubevirt.VirtualMachine{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		log.V(1).Info("The VM is not being deleted",
			"virtualMachineName", request.Name,
			"virtualMachineNamespace", request.Namespace)
		// The object is not being deleted, so if it does not have a finalizer,
		// then lets add the finalizer and update the object.
		err = r.addFinalizerAndUpdate(instance, &request)
		return reconcile.Result{}, err
	}
	// The object is being deleted
	log.V(1).Info("The VM is being marked for deletion",
		"virtualMachineName", request.Name,
		"virtualMachineNamespace", request.Namespace)
	err = r.removeFinalizerAndReleaseMac(instance, &request)
	return reconcile.Result{}, err
}

func (r *ReconcilePolicy) addFinalizerAndUpdate(virtualMachine *kubevirt.VirtualMachine, request *reconcile.Request) error {
	if helper.ContainsString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
		return nil
	}

	log.V(1).Info("The VM does not have a finalizer",
		"virtualMachineName", request.Name,
		"virtualMachineNamespace", request.Namespace)
	virtualMachine.ObjectMeta.Finalizers = append(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)

	err := r.Update(context.Background(), virtualMachine)
	if err != nil {
		log.Error(err, "failed to update the VM with the new finalizer",
			"virtualMachineName", request.Name,
			"virtualMachineNamespace", request.Namespace)
		return err
	}

	log.V(1).Info("Finalizer was added to the VM",
		"virtualMachineName", request.Name,
		"virtualMachineNamespace", request.Namespace)

	r.poolManager.MarkVMAsReady(virtualMachine)

	return nil
}

func (r *ReconcilePolicy) removeFinalizerAndReleaseMac(virtualMachine *kubevirt.VirtualMachine, request *reconcile.Request) error {
	if !helper.ContainsString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
		return nil
	}
	// our finalizer is present, so lets handle our external dependency
	log.V(1).Info("The VM contains the finalizer. Releasing mac")
	err := r.poolManager.ReleaseVirtualMachineMac(virtualMachine)
	if err != nil {
		log.Error(err, "failed to release mac",
			"virtualMachineName", request.Name,
			"virtualMachineNamespace", request.Namespace)
		return err
	}

	// remove our finalizer from the list and update it.
	log.V(1).Info("removing finalizers",
		"virtualMachineName", request.Name,
		"virtualMachineNamespace", request.Namespace)
	virtualMachine.ObjectMeta.Finalizers = helper.RemoveString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)

	err = r.Update(context.Background(), virtualMachine)
	if err != nil {
		log.Error(err, "failed to remove the finalizer",
			"virtualMachineName", request.Name,
			"virtualMachineNamespace", request.Namespace)
		return err
	}

	log.V(1).Info("Removed the finalizer from the VM",
		"virtualMachineName", request.Name,
		"virtualMachineNamespace", request.Namespace)

	return nil
}
