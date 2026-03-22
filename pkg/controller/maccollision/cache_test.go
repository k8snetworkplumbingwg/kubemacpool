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

package maccollision_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller/maccollision"
)

var _ = Describe("StripVMIForCollisionDetection", func() {
	It("should keep essential metadata fields", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-vmi",
				Namespace:         "test-ns",
				UID:               types.UID("test-uid-123"),
				Labels:            map[string]string{"label": "value"},
				Annotations:       map[string]string{"annotation": "value"},
				DeletionTimestamp: &metav1.Time{},
			},
		}

		result, err := maccollision.StripVMIForCollisionDetection(vmi)
		Expect(err).ToNot(HaveOccurred())

		stripped := result.(*kubevirtv1.VirtualMachineInstance)
		Expect(stripped.Name).To(Equal("test-vmi"))
		Expect(stripped.Namespace).To(Equal("test-ns"))
		Expect(stripped.UID).To(Equal(types.UID("test-uid-123")))
		Expect(stripped.DeletionTimestamp).ToNot(BeNil())
		Expect(stripped.Labels).To(BeEmpty(), "Labels should be stripped")
		Expect(stripped.Annotations).To(BeEmpty(), "Annotations should be stripped")
	})

	It("should keep status interfaces", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
					{Name: "eth0", MAC: "02:00:00:00:00:01"},
					{Name: "eth1", MAC: "02:00:00:00:00:02"},
				},
			},
		}

		result, err := maccollision.StripVMIForCollisionDetection(vmi)
		Expect(err).ToNot(HaveOccurred())

		stripped := result.(*kubevirtv1.VirtualMachineInstance)
		Expect(stripped.Status.Interfaces).To(HaveLen(2))
		Expect(stripped.Status.Interfaces[0].MAC).To(Equal("02:00:00:00:00:01"))
		Expect(stripped.Status.Interfaces[1].MAC).To(Equal("02:00:00:00:00:02"))
	})

	It("should keep phase and migrationState", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Phase: kubevirtv1.Running,
				MigrationState: &kubevirtv1.VirtualMachineInstanceMigrationState{
					SourceState: &kubevirtv1.VirtualMachineInstanceMigrationSourceState{
						VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
							MigrationUID: "source-migration-123",
						},
					},
					TargetState: &kubevirtv1.VirtualMachineInstanceMigrationTargetState{
						VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
							MigrationUID: "target-migration-456",
						},
					},
				},
				Conditions: []kubevirtv1.VirtualMachineInstanceCondition{
					{Type: kubevirtv1.VirtualMachineInstanceReady},
				},
			},
		}

		result, err := maccollision.StripVMIForCollisionDetection(vmi)
		Expect(err).ToNot(HaveOccurred())

		stripped := result.(*kubevirtv1.VirtualMachineInstance)
		Expect(stripped.Status.Phase).To(Equal(kubevirtv1.Running))
		Expect(stripped.Status.MigrationState).ToNot(BeNil())
		Expect(stripped.Status.MigrationState.SourceState).ToNot(BeNil())
		Expect(stripped.Status.MigrationState.SourceState.MigrationUID).To(Equal(types.UID("source-migration-123")))
		Expect(stripped.Status.MigrationState.TargetState).ToNot(BeNil())
		Expect(stripped.Status.MigrationState.TargetState.MigrationUID).To(Equal(types.UID("target-migration-456")))
		Expect(stripped.Status.Conditions).To(BeEmpty(), "Conditions should be stripped")
	})

	It("should handle non-VMI objects gracefully", func() {
		nonVMI := &kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}

		result, err := maccollision.StripVMIForCollisionDetection(nonVMI)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(nonVMI), "Should return object unchanged")
	})
})

var _ = Describe("IndexVMIByMAC", func() {
	It("should return all MAC addresses from VMI status", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
					{Name: "eth0", MAC: "02:00:00:00:00:01"},
					{Name: "eth1", MAC: "02:00:00:00:00:02"},
				},
			},
		}

		macs := maccollision.IndexVMIByMAC(vmi)
		Expect(macs).To(HaveLen(2))
		Expect(macs).To(ContainElement("02:00:00:00:00:01"))
		Expect(macs).To(ContainElement("02:00:00:00:00:02"))
	})

	It("should normalize MAC addresses to lowercase", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
					{Name: "eth0", MAC: "02:00:00:00:00:AA"},
					{Name: "eth1", MAC: "02:00:00:00:00:BB"},
				},
			},
		}

		macs := maccollision.IndexVMIByMAC(vmi)
		Expect(macs).To(HaveLen(2))
		Expect(macs).To(ContainElement("02:00:00:00:00:aa"))
		Expect(macs).To(ContainElement("02:00:00:00:00:bb"))
	})

	It("should skip interfaces without MAC addresses", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
					{Name: "eth0", MAC: "02:00:00:00:00:01"},
					{Name: "eth1", MAC: ""},
				},
			},
		}

		macs := maccollision.IndexVMIByMAC(vmi)
		Expect(macs).To(HaveLen(1))
		Expect(macs).To(ContainElement("02:00:00:00:00:01"))
	})

	It("should return empty slice for VMI with no interfaces", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{},
			},
		}

		macs := maccollision.IndexVMIByMAC(vmi)
		Expect(macs).To(BeEmpty())
	})

	It("should return nil for non-VMI objects", func() {
		nonVMI := &kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}

		macs := maccollision.IndexVMIByMAC(nonVMI)
		Expect(macs).To(BeNil())
	})
})

func podWithProcessedNetworks(name, namespace string, networks []*networkv1.NetworkSelectionElement) *corev1.Pod {
	pod := podWithPendingNetworks(name, namespace, networks)
	pod.Annotations[networkv1.NetworkStatusAnnot] = "[]"
	return pod
}

func podWithPendingNetworks(name, namespace string, networks []*networkv1.NetworkSelectionElement) *corev1.Pod {
	annotations := map[string]string{}
	if networks != nil {
		netJSON, _ := json.Marshal(networks)
		annotations[networkv1.NetworkAttachmentAnnot] = string(netJSON)
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
	}
}

var _ = Describe("StripPodForCollisionDetection", func() {
	It("should keep essential metadata and phase", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-ns",
				UID:       types.UID("pod-uid-123"),
				Labels:    map[string]string{"app": "test"},
				Annotations: map[string]string{
					networkv1.NetworkAttachmentAnnot: `[{"name":"net1","mac":"02:00:00:00:00:01"}]`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "worker-1",
			},
			Status: corev1.PodStatus{
				Phase:  corev1.PodRunning,
				PodIP:  "10.0.0.1",
				HostIP: "192.168.1.1",
			},
		}

		result, err := maccollision.StripPodForCollisionDetection(pod)
		Expect(err).ToNot(HaveOccurred())

		stripped := result.(*corev1.Pod)
		Expect(stripped.Name).To(Equal("test-pod"))
		Expect(stripped.Namespace).To(Equal("test-ns"))
		Expect(stripped.UID).To(Equal(types.UID("pod-uid-123")))
		Expect(stripped.Annotations).To(HaveKey(networkv1.NetworkAttachmentAnnot))
		Expect(stripped.Labels).To(HaveKeyWithValue("app", "test"))
		Expect(stripped.Status.Phase).To(Equal(corev1.PodRunning))
		Expect(stripped.OwnerReferences).To(BeEmpty(), "OwnerReferences should be stripped")
		Expect(stripped.Spec.NodeName).To(BeEmpty(), "Spec should be stripped")
		Expect(stripped.Status.PodIP).To(BeEmpty(), "PodIP should be stripped")
	})

	It("should handle non-Pod objects gracefully", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}

		result, err := maccollision.StripPodForCollisionDetection(vmi)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(vmi), "Should return object unchanged")
	})
})

var _ = Describe("IndexPodByMAC", func() {
	It("should return MAC addresses from network-attachment annotation", func() {
		pod := podWithProcessedNetworks("pod1", "ns1", []*networkv1.NetworkSelectionElement{
			{Name: "net1", MacRequest: "02:00:00:00:00:01"},
			{Name: "net2", MacRequest: "02:00:00:00:00:02"},
		})

		macs := maccollision.IndexPodByMAC(pod)
		Expect(macs).To(HaveLen(2))
		Expect(macs).To(ContainElement("02:00:00:00:00:01"))
		Expect(macs).To(ContainElement("02:00:00:00:00:02"))
	})

	It("should normalize MAC addresses to lowercase", func() {
		pod := podWithProcessedNetworks("pod1", "ns1", []*networkv1.NetworkSelectionElement{
			{Name: "net1", MacRequest: "02:00:00:00:00:AA"},
		})

		macs := maccollision.IndexPodByMAC(pod)
		Expect(macs).To(HaveLen(1))
		Expect(macs).To(ContainElement("02:00:00:00:00:aa"))
	})

	It("should skip networks without MacRequest", func() {
		pod := podWithProcessedNetworks("pod1", "ns1", []*networkv1.NetworkSelectionElement{
			{Name: "net1", MacRequest: "02:00:00:00:00:01"},
			{Name: "net2"},
		})

		macs := maccollision.IndexPodByMAC(pod)
		Expect(macs).To(HaveLen(1))
		Expect(macs).To(ContainElement("02:00:00:00:00:01"))
	})

	It("should deduplicate MACs within the same pod", func() {
		pod := podWithProcessedNetworks("pod1", "ns1", []*networkv1.NetworkSelectionElement{
			{Name: "net1", MacRequest: "02:00:00:00:00:01"},
			{Name: "net2", MacRequest: "02:00:00:00:00:01"},
		})

		macs := maccollision.IndexPodByMAC(pod)
		Expect(macs).To(HaveLen(1))
		Expect(macs).To(ContainElement("02:00:00:00:00:01"))
	})

	It("should return nil when network-status annotation is absent", func() {
		pod := podWithPendingNetworks("pod1", "ns1", []*networkv1.NetworkSelectionElement{
			{Name: "net1", MacRequest: "02:00:00:00:00:01"},
		})

		macs := maccollision.IndexPodByMAC(pod)
		Expect(macs).To(BeNil())
	})

	It("should return nil for non-Pod objects", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}

		macs := maccollision.IndexPodByMAC(vmi)
		Expect(macs).To(BeNil())
	})

	It("should return nil when no network-attachment annotation exists", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod1",
				Namespace: "ns1",
				Annotations: map[string]string{
					networkv1.NetworkStatusAnnot: "[]",
				},
			},
		}

		macs := maccollision.IndexPodByMAC(pod)
		Expect(macs).To(BeNil())
	})
})

var _ = DescribeTable("IsKubevirtOwned",
	func(labels map[string]string, expected bool) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
		}
		Expect(maccollision.IsKubevirtOwned(pod)).To(Equal(expected))
	},
	Entry("virt-launcher pod", map[string]string{kubevirtv1.AppLabel: "virt-launcher"}, true),
	Entry("different kubevirt.io label value", map[string]string{kubevirtv1.AppLabel: "hotplug-disk"}, false),
	Entry("no kubevirt labels", map[string]string{"app": "my-app"}, false),
	Entry("no labels at all", nil, false),
)
