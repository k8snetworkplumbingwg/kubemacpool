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

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func listRunningPodsWithMAC(ctx context.Context, k8sClient client.Client, normalizedMAC string) ([]*corev1.Pod, error) {
	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.MatchingFields{PodMacAddressIndexName: normalizedMAC}); err != nil {
		return nil, errors.Wrap(err, "failed to list Pods by MAC")
	}

	var runningPods []*corev1.Pod
	for i := range podList.Items {
		p := &podList.Items[i]
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		runningPods = append(runningPods, p)
	}

	return runningPods, nil
}

func excludePodUID(pods []*corev1.Pod, excludeUID types.UID) []*corev1.Pod {
	var filtered []*corev1.Pod
	for _, p := range pods {
		if p.UID == excludeUID {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}

func filterCollisionCandidatePods(pods []*corev1.Pod, reconciledNamespace string, poolManager PoolManagerInterface, logger logr.Logger) []*corev1.Pod {
	managedNamespaces := sets.New[string](reconciledNamespace)

	var filtered []*corev1.Pod
	for _, p := range pods {
		if IsKubevirtOwned(p) {
			logger.V(1).Info("Filtering out kubevirt-owned pod", "pod", p.Name, "namespace", p.Namespace)
			continue
		}

		if managedNamespaces.Has(p.Namespace) {
			filtered = append(filtered, p)
			continue
		}

		managed, err := poolManager.IsPodManaged(p.Namespace)
		if err != nil {
			logger.Error(err, "Failed to check if pod namespace is managed, including in collisions",
				"namespace", p.Namespace, "pod", p.Name)
			filtered = append(filtered, p)
			managedNamespaces.Insert(p.Namespace)
			continue
		}

		if !managed {
			logger.V(1).Info("Filtering out pod from unmanaged namespace",
				"namespace", p.Namespace, "pod", p.Name)
			continue
		}

		managedNamespaces.Insert(p.Namespace)
		filtered = append(filtered, p)
	}

	return filtered
}
