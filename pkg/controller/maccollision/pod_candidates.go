package maccollision

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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

		managed, err := poolManager.IsPodManaged(p.Namespace)
		if err != nil {
			logger.Error(err, "Failed to check if pod namespace is managed, including in collisions",
				"namespace", p.Namespace, "pod", p.Name)
			filtered = append(filtered, p)
			managedNamespaces[p.Namespace] = struct{}{}
			continue
		}

		if !managed {
			logger.V(1).Info("Filtering out pod from unmanaged namespace",
				"namespace", p.Namespace, "pod", p.Name)
			continue
		}

		managedNamespaces[p.Namespace] = struct{}{}
		filtered = append(filtered, p)
	}

	return filtered
}
