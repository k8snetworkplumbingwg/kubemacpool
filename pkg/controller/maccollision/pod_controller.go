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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var podLog = logf.Log.WithName("MACCollision Pod Controller")

// PodReconciler watches Pod objects and detects MAC address collisions.
type PodReconciler struct {
	client.Client
	poolManager PoolManagerInterface
	recorder    record.EventRecorder
}

// SetupPodControllerWithManager sets up the Pod collision controller with the Manager.
func SetupPodControllerWithManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	r := &PodReconciler{
		Client:      mgr.GetClient(),
		poolManager: poolManager,
		recorder:    mgr.GetEventRecorderFor("kubemacpool"),
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

	c, err := controller.New("maccollision-pod-controller", mgr, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return fmt.Errorf("failed to create MAC collision Pod controller: %w", err)
	}

	err = c.Watch(
		source.Kind(
			mgr.GetCache(),
			&corev1.Pod{},
			&handler.TypedEnqueueRequestForObject[*corev1.Pod]{},
			podCollisionRelevantChanges(),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to watch Pods: %w", err)
	}

	podLog.Info("Successfully registered MAC collision Pod controller")
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
			logger.V(1).Info("Pod not found, assuming deleted, removing from collision tracking")
			r.removePodFromAllCollisions(req.Namespace, req.Name)
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

	if err = r.checkMACCollisions(ctx, pod, logger); err != nil {
		logger.Error(err, "Failed to check MAC collisions")
		return reconcile.Result{}, errors.Wrap(err, "failed to check MAC collisions")
	}

	return reconcile.Result{}, nil
}

func (r *PodReconciler) checkMACCollisions(ctx context.Context, pod *corev1.Pod, logger logr.Logger) error {
	if pod.Status.Phase != corev1.PodRunning {
		logger.V(1).Info("Pod not running, removing from collision tracking", "phase", pod.Status.Phase)
		r.removePodFromAllCollisions(pod.Namespace, pod.Name)
		return nil
	}

	macs := r.extractMACsFromPod(pod)
	if len(macs) == 0 {
		logger.V(1).Info("Pod has no MAC addresses, skipping collision check")
		return nil
	}

	allCollisionsButOwn := make(map[string][]pool_manager.ObjectReference)
	for mac := range macs {
		collidingPods, err := r.findRunningPodsWithMAC(ctx, mac, pod.UID, logger)
		if err != nil {
			return errors.Wrapf(err, "failed to find Pods with MAC %s", mac)
		}

		collidingPods = r.filterPodsForCollision(collidingPods, pod.Namespace, logger)

		if len(collidingPods) > 0 {
			var refs []pool_manager.ObjectReference
			for _, p := range collidingPods {
				refs = append(refs, podObjectRef(p.Namespace, p.Name))
			}

			logger.Info("Found MAC collision", "mac", mac, "collisionCount", len(refs))
			allCollisionsButOwn[mac] = refs
			r.emitCollisionEvents(pod, mac, collidingPods)
		}
	}

	r.poolManager.UpdateCollisionsMap(podObjectRef(pod.Namespace, pod.Name), allCollisionsButOwn)
	return nil
}

func (r *PodReconciler) extractMACsFromPod(pod *corev1.Pod) map[string]struct{} {
	indexMACs := IndexPodByMAC(pod)
	macs := make(map[string]struct{}, len(indexMACs))
	for _, mac := range indexMACs {
		macs[mac] = struct{}{}
	}
	return macs
}

func (r *PodReconciler) findRunningPodsWithMAC(ctx context.Context, normalizedMAC string, excludeUID types.UID, logger logr.Logger) ([]*corev1.Pod, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.MatchingFields{PodMacAddressIndexName: normalizedMAC}); err != nil {
		return nil, errors.Wrap(err, "failed to list Pods by MAC")
	}

	var runningPods []*corev1.Pod
	for i := range podList.Items {
		p := &podList.Items[i]
		if p.UID == excludeUID {
			continue
		}
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		runningPods = append(runningPods, p)
	}

	return runningPods, nil
}

func (r *PodReconciler) filterPodsForCollision(pods []*corev1.Pod, reconciledNamespace string, logger logr.Logger) []*corev1.Pod {
	managedNamespaces := map[string]struct{}{
		reconciledNamespace: {},
	}

	var filtered []*corev1.Pod
	for _, p := range pods {
		if IsKubevirtOwned(p) {
			logger.V(1).Info("Filtering out kubevirt-owned pod", "pod", p.Name, "namespace", p.Namespace)
			continue
		}

		if _, alreadyChecked := managedNamespaces[p.Namespace]; alreadyChecked {
			filtered = append(filtered, p)
			continue
		}

		isManaged, err := r.poolManager.IsPodManaged(p.Namespace)
		if err != nil {
			logger.Error(err, "Failed to check if pod namespace is managed, including in collisions",
				"namespace", p.Namespace, "pod", p.Name)
			filtered = append(filtered, p)
			managedNamespaces[p.Namespace] = struct{}{}
			continue
		}

		if !isManaged {
			logger.V(1).Info("Filtering out pod from unmanaged namespace",
				"namespace", p.Namespace, "pod", p.Name)
			continue
		}

		managedNamespaces[p.Namespace] = struct{}{}
		filtered = append(filtered, p)
	}

	return filtered
}

func (r *PodReconciler) emitCollisionEvents(pod *corev1.Pod, mac string, collidingPods []*corev1.Pod) {
	selfRef := podObjectRef(pod.Namespace, pod.Name)
	all := []string{selfRef.Key()}
	for _, p := range collidingPods {
		all = append(all, podObjectRef(p.Namespace, p.Name).Key())
	}

	sort.Strings(all)

	message := fmt.Sprintf("MAC %s: Collision between %s", mac, strings.Join(all, ", "))

	r.recorder.Event(pod, corev1.EventTypeWarning, "MACCollision", message)

	for _, p := range collidingPods {
		r.recorder.Event(p, corev1.EventTypeWarning, "MACCollision", message)
	}
}

func (r *PodReconciler) removePodFromAllCollisions(namespace, name string) {
	r.poolManager.UpdateCollisionsMap(podObjectRef(namespace, name), nil)
}

func podObjectRef(namespace, name string) pool_manager.ObjectReference {
	return pool_manager.ObjectReference{
		Type:      pool_manager.ObjectTypePod,
		Namespace: namespace,
		Name:      name,
	}
}
