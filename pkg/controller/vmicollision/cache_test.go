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

package vmicollision_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller/vmicollision"
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

		result, err := vmicollision.StripVMIForCollisionDetection(vmi)
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

		result, err := vmicollision.StripVMIForCollisionDetection(vmi)
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

		result, err := vmicollision.StripVMIForCollisionDetection(vmi)
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

		result, err := vmicollision.StripVMIForCollisionDetection(nonVMI)
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

		macs := vmicollision.IndexVMIByMAC(vmi)
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

		macs := vmicollision.IndexVMIByMAC(vmi)
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

		macs := vmicollision.IndexVMIByMAC(vmi)
		Expect(macs).To(HaveLen(1))
		Expect(macs).To(ContainElement("02:00:00:00:00:01"))
	})

	It("should return empty slice for VMI with no interfaces", func() {
		vmi := &kubevirtv1.VirtualMachineInstance{
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{},
			},
		}

		macs := vmicollision.IndexVMIByMAC(vmi)
		Expect(macs).To(BeEmpty())
	})

	It("should return nil for non-VMI objects", func() {
		nonVMI := &kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}

		macs := vmicollision.IndexVMIByMAC(nonVMI)
		Expect(macs).To(BeNil())
	})
})
