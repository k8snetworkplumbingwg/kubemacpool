package tests

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubevirtv1 "kubevirt.io/api/core/v1"
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
