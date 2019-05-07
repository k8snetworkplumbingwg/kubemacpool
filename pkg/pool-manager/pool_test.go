/*
Copyright 2019 The KubeMacPool Authors.

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

package pool_manager

import (
	"k8s.io/apimachinery/pkg/runtime"
	"net"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	kubevirt "kubevirt.io/kubevirt/pkg/api/v1"
)

var _ = Describe("Pool", func() {
	beforeAllocationAnnotation := map[string]string{networksAnnotation: `[{ "name": "ovs-conf"}]`}
	afterAllocationAnnotation := map[string]string{networksAnnotation: `[{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:00"}]`}
	samplePod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "podpod", Namespace: "default", Annotations: afterAllocationAnnotation}}

	createPoolManager := func(startMacAddr, endMacAddr string, fakeObjectsForClient ...runtime.Object) *PoolManager {
		fakeClient := fake.NewSimpleClientset(fakeObjectsForClient...)
		startPoolRangeEnv, err := net.ParseMAC(startMacAddr)
		Expect(err).ToNot(HaveOccurred())
		endPoolRangeEnv, err := net.ParseMAC(endMacAddr)
		Expect(err).ToNot(HaveOccurred())
		poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
		Expect(err).ToNot(HaveOccurred())

		return poolManager
	}

	Describe("Internal Functions", func() {
		table.DescribeTable("should return the next mac address", func(macAddr, nextMacAddr string) {
			macAddrHW, err := net.ParseMAC(macAddr)
			Expect(err).ToNot(HaveOccurred())
			ExpectedMacAddrHW, err := net.ParseMAC(nextMacAddr)
			Expect(err).ToNot(HaveOccurred())
			nextMacAddrHW := getNextMac(macAddrHW)
			Expect(nextMacAddrHW).To(Equal(ExpectedMacAddrHW))
		},
			table.Entry("02:00:00:00:00:00 -> 02:00:00:00:00:01", "02:00:00:00:00:00", "02:00:00:00:00:01"),
			table.Entry("02:00:00:00:00:FF -> 02:00:00:00:01:00", "02:00:00:00:00:FF", "02:00:00:00:01:00"),
			table.Entry("FF:FF:FF:FF:FF:FF -> 00:00:00:00:00:00", "FF:FF:FF:FF:FF:FF", "00:00:00:00:00:00"),
		)

		table.DescribeTable("should check range", func(startMacAddr, endMacAddr string, needToFail bool) {
			startMacAddrHW, err := net.ParseMAC(startMacAddr)
			Expect(err).ToNot(HaveOccurred())
			endMacAddrHW, err := net.ParseMAC(endMacAddr)
			Expect(err).ToNot(HaveOccurred())
			err = checkRange(startMacAddrHW, endMacAddrHW)
			if needToFail {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
			table.Entry("Start: 02:00:00:00:00:00  End: 02:00:00:00:00:01", "02:00:00:00:00:00", "02:00:00:00:00:01", false),
			table.Entry("Start: 02:00:00:00:00:00  End: 02:10:00:00:00:00", "02:00:00:00:00:00", "02:10:00:00:00:00", false),
			table.Entry("Start: 02:FF:00:00:00:00  End: 02:00:00:00:00:00", "02:FF:00:00:00:00", "00:00:00:00:00:00", true),
		)
	})

	Describe("Pool Manager General Functions ", func() {
		It("should create a pool manager", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("FD:FF:FF:FF:FF:FF")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail to pool manager because of the invalid range", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("03:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Pool Manager Functions For VM", func() {

		bridgeInterface := kubevirt.Interface{
			Name: "pod",
			InterfaceBindingMethod: kubevirt.InterfaceBindingMethod{
				Bridge: &kubevirt.InterfaceBridge{}}}

		masqueradeInterface := kubevirt.Interface{
			Name: "pod",
			InterfaceBindingMethod: kubevirt.InterfaceBindingMethod{
				Masquerade: &kubevirt.InterfaceMasquerade{}}}

		multusBridgeInterface := kubevirt.Interface{
			Name: "multus",
			InterfaceBindingMethod: kubevirt.InterfaceBindingMethod{
				Bridge: &kubevirt.InterfaceBridge{}}}

		anotherMultusBridgeInterface := kubevirt.Interface{
			Name: "another-multus",
			InterfaceBindingMethod: kubevirt.InterfaceBindingMethod{
				Bridge: &kubevirt.InterfaceBridge{}}}

		podNetwork := kubevirt.Network{Name: "pod", NetworkSource: kubevirt.NetworkSource{Pod: &kubevirt.PodNetwork{}}}
		multusNetwork := kubevirt.Network{Name: "multus", NetworkSource: kubevirt.NetworkSource{Multus: &kubevirt.MultusNetwork{NetworkName: "multus"}}}
		anotherMultusNetwork := kubevirt.Network{Name: "another-multus", NetworkSource: kubevirt.NetworkSource{Multus: &kubevirt.MultusNetwork{NetworkName: "another-multus"}}}

		sampleVM := kubevirt.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}, Spec: kubevirt.VirtualMachineSpec{
			Template: &kubevirt.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirt.VirtualMachineInstanceSpec{
					Domain: kubevirt.DomainSpec{
						Devices: kubevirt.Devices{
							Interfaces: []kubevirt.Interface{bridgeInterface}}},
					Networks: []kubevirt.Network{podNetwork}}}}}

		masqueradeVM := kubevirt.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}, Spec: kubevirt.VirtualMachineSpec{
			Template: &kubevirt.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirt.VirtualMachineInstanceSpec{
					Domain: kubevirt.DomainSpec{
						Devices: kubevirt.Devices{
							Interfaces: []kubevirt.Interface{masqueradeInterface}}},
					Networks: []kubevirt.Network{podNetwork}}}}}

		multipleInterfacesVM := kubevirt.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}, Spec: kubevirt.VirtualMachineSpec{
			Template: &kubevirt.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirt.VirtualMachineInstanceSpec{
					Domain: kubevirt.DomainSpec{
						Devices: kubevirt.Devices{
							Interfaces: []kubevirt.Interface{masqueradeInterface, multusBridgeInterface}}},
					Networks: []kubevirt.Network{podNetwork, multusNetwork}}}}}

		It("should allocate a new mac and release it for masquerade", func() {
			fakeClient := fake.NewSimpleClientset(&samplePod)
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
			Expect(err).ToNot(HaveOccurred())
			poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).ToNot(HaveOccurred())
			newVM := masqueradeVM
			newVM.Name = "newVM"

			err = poolManager.AllocateVirtualMachineMac(&newVM)
			Expect(err).ToNot(HaveOccurred())

			Expect(len(poolManager.macPoolMap)).To(Equal(2))
			_, exist := poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeTrue())

			Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:01"))
			macAddress, exist := poolManager.vmToMacPoolMap[vmNamespaced(&newVM)]
			Expect(exist).To(BeTrue())
			Expect(len(macAddress)).To(Equal(1))
			Expect(macAddress["pod"]).To(Equal("02:00:00:00:00:01"))

			err = poolManager.ReleaseVirtualMachineMac(vmNamespaced(&newVM))
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(1))
			_, exist = poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeFalse())
		})
		It("should not allocate a new mac for bridge interface on pod network", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
			Expect(err).ToNot(HaveOccurred())
			poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).ToNot(HaveOccurred())
			newVM := sampleVM
			newVM.Name = "newVM"

			err = poolManager.AllocateVirtualMachineMac(&newVM)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(0))
		})
		It("should allocate a new mac and release it for multiple interfaces", func() {
			fakeClient := fake.NewSimpleClientset(&samplePod)
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
			Expect(err).ToNot(HaveOccurred())
			poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).ToNot(HaveOccurred())
			newVM := multipleInterfacesVM.DeepCopy()
			newVM.Name = "newVM"

			err = poolManager.AllocateVirtualMachineMac(newVM)
			Expect(err).ToNot(HaveOccurred())

			Expect(len(poolManager.macPoolMap)).To(Equal(3))
			_, exist := poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:02"]
			Expect(exist).To(BeTrue())

			Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:01"))
			Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:02"))
			macAddress, exist := poolManager.vmToMacPoolMap[vmNamespaced(newVM)]
			Expect(exist).To(BeTrue())
			Expect(len(macAddress)).To(Equal(2))
			Expect(macAddress["pod"]).To(Equal("02:00:00:00:00:01"))
			Expect(macAddress["multus"]).To(Equal("02:00:00:00:00:02"))

			err = poolManager.ReleaseVirtualMachineMac(vmNamespaced(newVM))
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(1))
			_, exist = poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeFalse())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:02"]
			Expect(exist).To(BeFalse())
		})
		Describe("Update vm object", func() {
			It("should preserve mac addresses on update", func() {
				fakeClient := fake.NewSimpleClientset()
				startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
				Expect(err).ToNot(HaveOccurred())
				poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
				Expect(err).ToNot(HaveOccurred())
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				err = poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				err = poolManager.UpdateMacAddressesForVirtualMachine(updateVm)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
			})
			It("should preserve mac addresses an allocate a requested one on update", func() {
				fakeClient := fake.NewSimpleClientset()
				startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
				Expect(err).ToNot(HaveOccurred())
				poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
				Expect(err).ToNot(HaveOccurred())
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				err = poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress = "01:00:00:00:00:02"
				err = poolManager.UpdateMacAddressesForVirtualMachine(updateVm)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("01:00:00:00:00:02"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:01"]
				Expect(exist).To(BeFalse())
			})
			It("should allow to add a new interface on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02")
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				err := poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:02"]
				Expect(exist).To(BeFalse())

				updatedVM := multipleInterfacesVM.DeepCopy()
				updatedVM.Name = "newVM"
				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				updatedVM.Spec.Template.Spec.Networks = append(updatedVM.Spec.Template.Spec.Networks, anotherMultusNetwork)

				err = poolManager.UpdateMacAddressesForVirtualMachine(updatedVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[2].MacAddress).To(Equal("02:00:00:00:00:02"))

				_, exist = poolManager.macPoolMap["02:00:00:00:00:00"]
				Expect(exist).To(BeTrue())
				_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
				Expect(exist).To(BeTrue())
				_, exist = poolManager.macPoolMap["02:00:00:00:00:02"]
				Expect(exist).To(BeTrue())
			})
			It("should allow to remove an interface on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02")
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"
				newVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(newVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				newVM.Spec.Template.Spec.Networks = append(newVM.Spec.Template.Spec.Networks, anotherMultusNetwork)

				err := poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[2].MacAddress).To(Equal("02:00:00:00:00:02"))

				updatedVM := multipleInterfacesVM.DeepCopy()
				updatedVM.Name = "newVM"

				err = poolManager.UpdateMacAddressesForVirtualMachine(updatedVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:02"]
				Expect(exist).To(BeFalse())
			})
			It("should allow to remove and add an interface on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02")
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				err := poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				updatedVM := sampleVM.DeepCopy()
				updatedVM.Name = "newVM"

				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				updatedVM.Spec.Template.Spec.Networks = append(updatedVM.Spec.Template.Spec.Networks, anotherMultusNetwork)
				err = poolManager.UpdateMacAddressesForVirtualMachine(updatedVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:02"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].Name).To(Equal("another-multus"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:01"]
				Expect(exist).To(BeFalse())
			})
		})
	})

	Describe("Pool Manager Functions For pod", func() {
		It("should allocate a new mac and release it", func() {
			fakeClient := fake.NewSimpleClientset(&samplePod)
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
			Expect(err).ToNot(HaveOccurred())
			poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).ToNot(HaveOccurred())
			newPod := samplePod
			newPod.Name = "newPod"
			newPod.Annotations = beforeAllocationAnnotation

			err = poolManager.AllocatePodMac(&newPod)
			Expect(err).ToNot(HaveOccurred())

			Expect(len(poolManager.macPoolMap)).To(Equal(2))
			_, exist := poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeTrue())

			Expect(newPod.Annotations[networksAnnotation]).To(Equal(`[{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:01"}]`))
			macAddress, exist := poolManager.podToMacPoolMap[podNamespaced(&newPod)]
			Expect(exist).To(BeTrue())
			Expect(len(macAddress)).To(Equal(1))
			Expect(macAddress["ovs-conf"]).To(Equal("02:00:00:00:00:01"))

			err = poolManager.ReleasePodMac(podNamespaced(&newPod))
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(1))
			_, exist = poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeFalse())
		})
		It("should allocate requested mac when empty", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:02")
			Expect(err).ToNot(HaveOccurred())
			poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, false)
			Expect(err).ToNot(HaveOccurred())
			newPod := samplePod
			newPod.Name = "newPod"

			err = poolManager.AllocatePodMac(&newPod)
			Expect(err).ToNot(HaveOccurred())
			Expect(newPod.Annotations[networksAnnotation]).To(Equal(afterAllocationAnnotation[networksAnnotation]))
		})
	})

})
