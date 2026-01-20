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

package configmap

import (
	"context"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/go-logr/logr"

	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

// PoolManager defines the interface for MAC pool management
type PoolManager interface {
	GetCurrentRanges() (start, end string)
	UpdateRanges(start, end net.HardwareAddr) error
}

// ConfigMapReconciler reconciles a ConfigMap object for MAC range updates
type ConfigMapReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	PoolManager        PoolManager
	ConfigMapNamespace string
	EventRecorder      record.EventRecorder
}

// RangeConfig represents MAC address range configuration parsed from ConfigMap
type RangeConfig struct {
	Start net.HardwareAddr
	End   net.HardwareAddr
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Add creates a new ConfigMap Controller and adds it to the Manager with the given poolManager.
func Add(mgr manager.Manager, poolManager *poolmanager.PoolManager) (bool, error) {
	// Get the namespace from the pool manager
	podNamespace := poolManager.ManagerNamespace()

	reconciler := &ConfigMapReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		PoolManager:        poolManager,
		ConfigMapNamespace: podNamespace,
		EventRecorder:      mgr.GetEventRecorderFor("configmap-controller"),
	}

	err := ctrl.NewControllerManagedBy(mgr).
		Named("mac-range-configmap").
		For(&corev1.ConfigMap{}).
		Complete(reconciler)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ConfigMap (predicate already filtered to our specific ConfigMap)
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, req.NamespacedName, configMap)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ConfigMap not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	}

	logger.V(2).Info("Processing ConfigMap for MAC range update")

	// Extract and validate new MAC ranges from ConfigMap
	rangeConfig, err := r.extractRangesFromConfigMap(configMap)
	if err != nil {
		logger.Error(err, "Failed to extract ranges from ConfigMap",
			"data", configMap.Data)

		r.recordEvent(ctx, logger, corev1.EventTypeWarning, "InvalidRange",
			"Invalid MAC range in ConfigMap, ignoring new range values: %v", err)

		return ctrl.Result{}, fmt.Errorf("invalid ConfigMap data: %v", err)
	}

	logger.V(1).Info("Extracted new MAC ranges from ConfigMap",
		"newRangeStart", rangeConfig.Start.String(),
		"newRangeEnd", rangeConfig.End.String())

	// Check if the new ranges are different from current ranges
	currentStart, currentEnd := r.PoolManager.GetCurrentRanges()
	if rangeConfig.Start.String() == currentStart && rangeConfig.End.String() == currentEnd {
		logger.V(2).Info("MAC address ranges unchanged, skipping update",
			"currentRangeStart", currentStart,
			"currentRangeEnd", currentEnd)
		return ctrl.Result{}, nil
	}

	logger.Info("MAC address ranges changed, applying update",
		"oldRangeStart", currentStart,
		"oldRangeEnd", currentEnd,
		"newRangeStart", rangeConfig.Start.String(),
		"newRangeEnd", rangeConfig.End.String())

	// Apply the new ranges to PoolManager
	if err := r.PoolManager.UpdateRanges(rangeConfig.Start, rangeConfig.End); err != nil {
		logger.Error(err, "Failed to update MAC address ranges in PoolManager",
			"rangeStart", rangeConfig.Start.String(),
			"rangeEnd", rangeConfig.End.String())

		r.recordEvent(ctx, logger, corev1.EventTypeWarning, "UpdateFailed",
			"Failed to apply new MAC ranges %s - %s: %v",
			rangeConfig.Start.String(), rangeConfig.End.String(), err)

		return ctrl.Result{}, fmt.Errorf("failed to apply new ranges: %v", err)
	}

	logger.V(1).Info("Successfully updated MAC address ranges",
		"rangeStart", rangeConfig.Start.String(),
		"rangeEnd", rangeConfig.End.String())

	return ctrl.Result{}, nil
}

// extractRangesFromConfigMap parses MAC ranges from ConfigMap data
func (r *ConfigMapReconciler) extractRangesFromConfigMap(cm *corev1.ConfigMap) (*RangeConfig, error) {
	// Validate ConfigMap has the expected keys
	startStr, ok := cm.Data["RANGE_START"]
	if !ok {
		return nil, fmt.Errorf("RANGE_START not found in ConfigMap")
	}

	endStr, ok := cm.Data["RANGE_END"]
	if !ok {
		return nil, fmt.Errorf("RANGE_END not found in ConfigMap")
	}

	// Parse and validate MAC addresses
	start, err := net.ParseMAC(startStr)
	if err != nil {
		return nil, fmt.Errorf("invalid RANGE_START %q: %w", startStr, err)
	}

	end, err := net.ParseMAC(endStr)
	if err != nil {
		return nil, fmt.Errorf("invalid RANGE_END %q: %w", endStr, err)
	}

	return newRangeConfig(start, end), nil
}

// newRangeConfig creates a new RangeConfig with the provided MAC addresses
func newRangeConfig(start, end net.HardwareAddr) *RangeConfig {
	return &RangeConfig{Start: start, End: end}
}

// getManagerPod finds the kubemacpool manager pod for event recording
func (r *ConfigMapReconciler) getManagerPod(ctx context.Context) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	listOptions := []client.ListOption{
		client.InNamespace(r.ConfigMapNamespace),
		client.MatchingLabels{
			"app":           "kubemacpool",
			"control-plane": "mac-controller-manager",
		},
	}

	if err := r.List(ctx, podList, listOptions...); err != nil {
		return nil, fmt.Errorf("failed to list kubemacpool pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no kubemacpool manager pods found")
	}

	// Return the first running pod
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return &pod, nil
		}
	}

	// If no running pod found, return the first pod
	return &podList.Items[0], nil
}

// recordEvent is a helper to record Kubernetes events on the manager pod
func (r *ConfigMapReconciler) recordEvent(ctx context.Context, logger logr.Logger, eventType, reason, messageFmt string, args ...interface{}) {
	if r.EventRecorder != nil {
		if managerPod, err := r.getManagerPod(ctx); err != nil {
			logger.V(1).Info("Could not find manager pod for event recording", "error", err)
		} else {
			r.EventRecorder.Eventf(managerPod, eventType, reason, messageFmt, args...)
		}
	}
}
