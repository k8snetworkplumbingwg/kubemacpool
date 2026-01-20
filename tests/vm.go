package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const (
	vmCleanupTimeout         = 2 * time.Minute
	vmCleanupPollingInterval = 5 * time.Second
)

// removeFinalizersFromVM patches a VM to remove all finalizers
func removeFinalizersFromVM(namespace, name string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		vm := &kubevirtv1.VirtualMachine{}
		err := testClient.CRClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: name}, vm)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if len(vm.Finalizers) == 0 {
			return nil
		}

		vm.Finalizers = []string{}
		return testClient.CRClient.Update(context.TODO(), vm)
	})
}

// cleanupVMsInNamespaces deletes all VMs in the given namespaces
func cleanupVMsInNamespaces(namespaces []string) error {
	for _, namespace := range namespaces {
		if err := testClient.CRClient.DeleteAllOf(context.TODO(), &kubevirtv1.VirtualMachine{},
			client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("failed to delete VMs in namespace %s: %w", namespace, err)
		}
	}
	return nil
}

// waitForVMsDeleted waits for all VMs in the given namespaces to be deleted
func waitForVMsDeleted(namespaces []string) {
	Eventually(func(g Gomega) int {
		totalVMs := 0
		for _, namespace := range namespaces {
			vmList := &kubevirtv1.VirtualMachineList{}
			g.Expect(testClient.CRClient.List(context.TODO(), vmList, client.InNamespace(namespace))).To(Succeed())
			totalVMs += len(vmList.Items)
		}
		return totalVMs
	}).WithTimeout(vmCleanupTimeout).WithPolling(vmCleanupPollingInterval).Should(Equal(0), "All VMs should be deleted")
}

// forceCleanupStuckVMs removes finalizers from any remaining VMs that are stuck in terminating
func forceCleanupStuckVMs(namespaces []string) error {
	for _, namespace := range namespaces {
		vmList := &kubevirtv1.VirtualMachineList{}
		if err := testClient.CRClient.List(context.TODO(), vmList, client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("failed to list VMs in namespace %s: %w", namespace, err)
		}

		for i := range vmList.Items {
			vm := &vmList.Items[i]
			if vm.DeletionTimestamp != nil && len(vm.Finalizers) > 0 {
				if err := removeFinalizersFromVM(vm.Namespace, vm.Name); err != nil {
					return fmt.Errorf("failed to remove finalizers from stuck VM %s/%s: %w", vm.Namespace, vm.Name, err)
				}
			}
		}
	}
	return nil
}
