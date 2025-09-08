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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
}

// RangeConfig represents MAC address range configuration parsed from ConfigMap
type RangeConfig struct {
	Start net.HardwareAddr
	End   net.HardwareAddr
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

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

	logger.Info("Processing ConfigMap for MAC range update")

	// Extract and validate new MAC ranges from ConfigMap
	rangeConfig, err := r.extractRangesFromConfigMap(configMap)
	if err != nil {
		logger.Error(err, "Failed to extract ranges from ConfigMap",
			"data", configMap.Data)
		return ctrl.Result{}, fmt.Errorf("invalid ConfigMap data: %v", err)
	}

	logger.V(1).Info("Extracted new MAC ranges from ConfigMap",
		"newRangeStart", rangeConfig.Start.String(),
		"newRangeEnd", rangeConfig.End.String())

	// Check if the new ranges are different from current ranges
	currentStart, currentEnd := r.PoolManager.GetCurrentRanges()
	if rangeConfig.Start.String() == currentStart && rangeConfig.End.String() == currentEnd {
		logger.Info("MAC address ranges unchanged, skipping update",
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
