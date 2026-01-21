package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const (
	vmiCleanupTimeout         = 2 * time.Minute
	vmiCleanupPollingInterval = 5 * time.Second
)

type VMIOption func(*kubevirtv1.VirtualMachineInstance)

func WithInterface(iface kubevirtv1.Interface) VMIOption {
	return func(vmi *kubevirtv1.VirtualMachineInstance) {
		vmi.Spec.Domain.Devices.Interfaces = append(vmi.Spec.Domain.Devices.Interfaces, iface)
	}
}

func WithNetwork(network kubevirtv1.Network) VMIOption {
	return func(vmi *kubevirtv1.VirtualMachineInstance) {
		vmi.Spec.Networks = append(vmi.Spec.Networks, network)
	}
}

func NewVMI(namespace, name string, opts ...VMIOption) *kubevirtv1.VirtualMachineInstance {
	vmi := &kubevirtv1.VirtualMachineInstance{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VirtualMachineInstance",
			APIVersion: kubevirtv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      randName(name),
			Namespace: namespace,
		},
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Domain: kubevirtv1.DomainSpec{
				Memory: &kubevirtv1.Memory{
					Guest: func() *resource.Quantity { q := resource.MustParse("64Mi"); return &q }(),
				},
				Resources: kubevirtv1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
				Devices: kubevirtv1.Devices{
					Disks: []kubevirtv1.Disk{
						{
							Name: "containerdisk",
							DiskDevice: kubevirtv1.DiskDevice{
								Disk: &kubevirtv1.DiskTarget{
									Bus: kubevirtv1.VirtIO,
								},
							},
						},
						{
							Name: "cloudinitdisk",
							DiskDevice: kubevirtv1.DiskDevice{
								Disk: &kubevirtv1.DiskTarget{
									Bus: kubevirtv1.VirtIO,
								},
							},
						},
					},
				},
			},
			TerminationGracePeriodSeconds: func() *int64 { i := int64(0); return &i }(),
			Volumes: []kubevirtv1.Volume{
				{
					Name: "containerdisk",
					VolumeSource: kubevirtv1.VolumeSource{
						ContainerDisk: &kubevirtv1.ContainerDiskSource{
							Image: "quay.io/kubevirt/cirros-container-disk-demo:latest",
						},
					},
				},
				{
					Name: "cloudinitdisk",
					VolumeSource: kubevirtv1.VolumeSource{
						CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
							UserData: "#!/bin/sh\necho 'printed from cloud-init userdata'\n",
						},
					},
				},
			},
		},
	}
	for _, opt := range opts {
		opt(vmi)
	}
	return vmi
}

func getVMIEvents(namespace, vmiName string) (*v1.EventList, error) {
	return testClient.K8sClient.CoreV1().Events(namespace).List(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=VirtualMachineInstance", vmiName),
	})
}

func getVMIPhase(namespace, name string) kubevirtv1.VirtualMachineInstancePhase {
	vmi := &kubevirtv1.VirtualMachineInstance{}
	err := testClient.CRClient.Get(context.TODO(),
		client.ObjectKey{Namespace: namespace, Name: name},
		vmi)
	if err != nil {
		return ""
	}
	return vmi.Status.Phase
}

// cleanupVMIsInNamespaces deletes all VMIs in the given namespaces
func cleanupVMIsInNamespaces(namespaces []string) error {
	for _, namespace := range namespaces {
		if err := testClient.CRClient.DeleteAllOf(context.TODO(), &kubevirtv1.VirtualMachineInstance{},
			client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("failed to delete VMIs in namespace %s: %w", namespace, err)
		}
	}
	return nil
}

// removeFinalizersFromVMI patches a VMI to remove all finalizers
func removeFinalizersFromVMI(namespace, name string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		vmi := &kubevirtv1.VirtualMachineInstance{}
		err := testClient.CRClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: name}, vmi)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if len(vmi.Finalizers) == 0 {
			return nil
		}

		vmi.Finalizers = []string{}
		return testClient.CRClient.Update(context.TODO(), vmi)
	})
}

// waitForVMIsDeleted waits for all VMIs in the given namespaces to be deleted
func waitForVMIsDeleted(namespaces []string) {
	Eventually(func(g Gomega) int {
		totalVMIs := 0
		for _, namespace := range namespaces {
			vmiList := &kubevirtv1.VirtualMachineInstanceList{}
			g.Expect(testClient.CRClient.List(context.TODO(), vmiList, client.InNamespace(namespace))).To(Succeed())
			totalVMIs += len(vmiList.Items)
		}
		return totalVMIs
	}).WithTimeout(vmiCleanupTimeout).WithPolling(vmiCleanupPollingInterval).Should(Equal(0), "All VMIs should be deleted")
}

// cleanupMigrationsInNamespaces deletes all VirtualMachineInstanceMigrations in the given namespaces
func cleanupMigrationsInNamespaces(namespaces []string) error {
	for _, namespace := range namespaces {
		if err := testClient.CRClient.DeleteAllOf(context.TODO(), &kubevirtv1.VirtualMachineInstanceMigration{},
			client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("failed to delete migrations in namespace %s: %w", namespace, err)
		}
	}
	return nil
}

// forceCleanupStuckVMIs removes finalizers from any remaining VMIs that are stuck in terminating
func forceCleanupStuckVMIs(namespaces []string) error {
	for _, namespace := range namespaces {
		vmiList := &kubevirtv1.VirtualMachineInstanceList{}
		if err := testClient.CRClient.List(context.TODO(), vmiList, client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("failed to list VMIs in namespace %s: %w", namespace, err)
		}

		for i := range vmiList.Items {
			vmi := &vmiList.Items[i]
			if vmi.DeletionTimestamp != nil && len(vmi.Finalizers) > 0 {
				if err := removeFinalizersFromVMI(vmi.Namespace, vmi.Name); err != nil {
					return fmt.Errorf("failed to remove finalizers from stuck VMI %s/%s: %w", vmi.Namespace, vmi.Name, err)
				}
			}
		}
	}
	return nil
}
