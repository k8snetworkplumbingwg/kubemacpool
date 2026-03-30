package maccollision

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func listVMIsByMAC(ctx context.Context, k8sClient client.Client, normalizedMAC string) (*kubevirtv1.VirtualMachineInstanceList, error) {
	vmiList := &kubevirtv1.VirtualMachineInstanceList{}
	if err := k8sClient.List(ctx, vmiList, client.MatchingFields{MacAddressIndexName: normalizedMAC}); err != nil {
		return nil, errors.Wrap(err, "failed to list VMIs by MAC")
	}
	return vmiList, nil
}

func listRunningVMIsByMAC(ctx context.Context, k8sClient client.Client, normalizedMAC string) ([]*kubevirtv1.VirtualMachineInstance, error) {
	vmiList, err := listVMIsByMAC(ctx, k8sClient, normalizedMAC)
	if err != nil {
		return nil, err
	}

	var runningVMIs []*kubevirtv1.VirtualMachineInstance
	for i := range vmiList.Items {
		vmi := &vmiList.Items[i]
		if vmi.Status.Phase != kubevirtv1.Running {
			continue
		}
		runningVMIs = append(runningVMIs, vmi)
	}

	return runningVMIs, nil
}

func listRunningVMIsByMACWithExcludeUID(ctx context.Context, k8sClient client.Client, normalizedMAC string, excludeUID types.UID) ([]*kubevirtv1.VirtualMachineInstance, error) {
	vmiList, err := listVMIsByMAC(ctx, k8sClient, normalizedMAC)
	if err != nil {
		return nil, err
	}

	var filtered []*kubevirtv1.VirtualMachineInstance
	for i := range vmiList.Items {
		vmi := &vmiList.Items[i]
		if vmi.Status.Phase != kubevirtv1.Running {
			continue
		}
		if vmi.UID == excludeUID {
			continue
		}
		filtered = append(filtered, vmi)
	}

	return filtered, nil
}

func filterManagedVMIs(candidates []*kubevirtv1.VirtualMachineInstance, knownManagedNamespaces map[string]struct{}, poolManager PoolManagerInterface, logger logr.Logger) []*kubevirtv1.VirtualMachineInstance {
	if knownManagedNamespaces == nil {
		knownManagedNamespaces = map[string]struct{}{}
	}

	var filtered []*kubevirtv1.VirtualMachineInstance
	for _, candidate := range candidates {
		if _, ok := knownManagedNamespaces[candidate.Namespace]; ok {
			filtered = append(filtered, candidate)
			continue
		}

		isManaged, err := poolManager.IsVirtualMachineManaged(candidate.Namespace)
		if err != nil {
			logger.Error(err, "Failed to check if VMI namespace is managed, including in collisions",
				"namespace", candidate.Namespace, "vmi", candidate.Name)
			filtered = append(filtered, candidate)
			knownManagedNamespaces[candidate.Namespace] = struct{}{}
			continue
		}

		if !isManaged {
			logger.V(1).Info("Filtering out VMI from unmanaged namespace",
				"namespace", candidate.Namespace, "vmi", candidate.Name)
			continue
		}

		knownManagedNamespaces[candidate.Namespace] = struct{}{}
		filtered = append(filtered, candidate)
	}

	return filtered
}
