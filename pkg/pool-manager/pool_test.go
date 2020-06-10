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
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"net"

	multus "github.com/intel/multus-cni/types"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	kubevirt "kubevirt.io/client-go/api/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("Pool", func() {
	beforeAllocationAnnotation := map[string]string{networksAnnotation: `[{ "name": "ovs-conf"}]`}
	afterAllocationAnnotation := map[string]string{networksAnnotation: `[{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:00"}]`}
	samplePod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "podpod", Namespace: "default", Annotations: afterAllocationAnnotation}}
	vmConfigMap := v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: names.MANAGER_NAMESPACE, Name: names.WAITING_VMS_CONFIGMAP}}

	createPoolManager := func(startMacAddr, endMacAddr string, fakeObjectsForClient ...runtime.Object) *PoolManager {
		fakeClient := fake.NewSimpleClientset(fakeObjectsForClient...)
		startPoolRangeEnv, err := net.ParseMAC(startMacAddr)
		Expect(err).ToNot(HaveOccurred(), "should successfully parse starting mac address range")
		endPoolRangeEnv, err := net.ParseMAC(endMacAddr)
		Expect(err).ToNot(HaveOccurred(), "should successfully parse ending mac address range")
		poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, names.MANAGER_NAMESPACE, false, 10)
		Expect(err).ToNot(HaveOccurred(), "should successfully initialize poolManager")
		err = poolManager.Start()
		Expect(err).ToNot(HaveOccurred(), "should successfully start poolManager routines")

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

		table.DescribeTable("should check that the multicast bit is off", func(MacAddr string, shouldFail bool) {
			MacAddrHW, err := net.ParseMAC(MacAddr)
			Expect(err).ToNot(HaveOccurred())
			err = checkCast(MacAddrHW)
			if shouldFail {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
			table.Entry("Valid address: 02:00:00:00:00:00", "02:00:00:00:00:00", false),
			table.Entry("Valid address: 06:00:00:00:00:00", "06:00:00:00:00:00", false),
			table.Entry("Valid address: 0A:00:00:00:00:00", "0A:00:00:00:00:00", false),
			table.Entry("Valid address: 0E:00:00:00:00:00", "0E:00:00:00:00:00", false),
			table.Entry("Invalid address: 01:FF:00:00:00:00, the first octet is not 02, 06, 0A or 0E", "01:FF:00:00:00:00", true),
			table.Entry("Invalid address: FF:FF:00:00:00:00, the first octet is not 02, 06, 0A or 0E", "FF:FF:00:00:00:00", true),
		)
	})

	Describe("Pool Manager General Functions ", func() {
		It("should create a pool manager", func() {
			createPoolManager("02:00:00:00:00:00", "02:FF:FF:FF:FF:FF")
		})

		It("should fail to create pool manager when rangeStart is greater than rangeEnd", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("0A:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, names.MANAGER_NAMESPACE, false, 10)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("Invalid range. rangeStart: 0a:00:00:00:00:00 rangeEnd: 02:00:00:00:00:00"))

		})

		It("should fail to pool manager because of the first octet of RangeStart is not 2, 6, A, E", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("03:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("06:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, names.MANAGER_NAMESPACE, false, 10)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("RangeStart is invalid: invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is 0X3"))

		})

		It("should fail to create a pool manager object when the first octet of RangeEnd is not 2, 6, A, E", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("05:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, names.MANAGER_NAMESPACE, false, 10)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("RangeEnd is invalid: invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is 0X5"))
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
			poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &samplePod, &vmConfigMap)
			newVM := masqueradeVM
			newVM.Name = "newVM"

			err := poolManager.AllocateVirtualMachineMac(&newVM)
			Expect(err).ToNot(HaveOccurred())

			Expect(len(poolManager.macPoolMap)).To(Equal(2))
			_, exist := poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeTrue())

			Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:01"))

			err = poolManager.ReleaseVirtualMachineMac(&newVM, logf.Log.WithName("pool Test"))
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(1))
			_, exist = poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeFalse())
		})
		It("should not allocate a new mac for bridge interface on pod network", func() {
			poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
			newVM := sampleVM
			newVM.Name = "newVM"

			err := poolManager.AllocateVirtualMachineMac(&newVM)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(0))
		})
		It("should allocate a new mac and release it for multiple interfaces", func() {
			poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &samplePod, &vmConfigMap)
			newVM := multipleInterfacesVM.DeepCopy()
			newVM.Name = "newVM"

			err := poolManager.AllocateVirtualMachineMac(newVM)
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

			err = poolManager.ReleaseVirtualMachineMac(newVM, logf.Log.WithName("VirtualMachine Controller"))
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
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"
				err := poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
			})
			It("should preserve mac addresses and allocate a requested one on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				err := poolManager.AllocateVirtualMachineMac(newVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress = "01:00:00:00:00:02"
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("01:00:00:00:00:02"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:01"]
				Expect(exist).To(BeFalse())
			})
			It("should allow to add a new interface on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
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

				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM)
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
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
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

				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:02"]
				Expect(exist).To(BeFalse())
			})
			It("should allow to remove and add an interface on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
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
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM)
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
			poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &samplePod, &vmConfigMap)
			newPod := samplePod
			newPod.Name = "newPod"
			newPod.Annotations = beforeAllocationAnnotation

			err := poolManager.AllocatePodMac(&newPod)
			Expect(err).ToNot(HaveOccurred())

			Expect(len(poolManager.macPoolMap)).To(Equal(2))
			_, exist := poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeTrue())

			Expect(newPod.Annotations[networksAnnotation]).To(Equal(`[{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:01","cni-args":null}]`))
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
			poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
			newPod := samplePod
			newPod.Name = "newPod"

			err := poolManager.AllocatePodMac(&newPod)
			Expect(err).ToNot(HaveOccurred())
			Expect(newPod.Annotations[networksAnnotation]).To(Equal(afterAllocationAnnotation[networksAnnotation]))
		})
	})

	Describe("Multus Network Annotations API Tests", func() {
		Context("when pool-manager is configured with available addresses", func() {
			poolManager := &PoolManager{}

			BeforeEach(func() {
				poolManager = createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
				Expect(poolManager).ToNot(Equal(nil), "should create pool-manager")
			})

			table.DescribeTable("should allocate mac-address correspond to the one specified in the networks annotation",
				func(networkRequestAnnotation string) {
					pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "testPod", Namespace: "default"}}
					pod.Annotations = map[string]string{networksAnnotation: fmt.Sprintf("%s", networkRequestAnnotation)}

					By("Request specific mac-address by adding the address to the networks pod annotation")
					err := poolManager.AllocatePodMac(&pod)
					Expect(err).ToNot(HaveOccurred(), "should allocate mac address and ip address correspond to networks annotation")

					By("Convert obtained networks annotation JSON to multus.NetworkSelectionElement array")
					obtainedNetworksAnnotationJson := pod.Annotations[networksAnnotation]
					obtainedNetworksAnnotation := []multus.NetworkSelectionElement{}
					err = json.Unmarshal([]byte(obtainedNetworksAnnotationJson), &obtainedNetworksAnnotation)
					Expect(err).ToNot(HaveOccurred(), "should convert obtained annotation as json to multus.NetworkSelectionElement")

					By("Convert expected networks annotation JSON to multus.NetworkSelectionElement array")
					expectedNetworksAnnotation := []multus.NetworkSelectionElement{}
					err = json.Unmarshal([]byte(networkRequestAnnotation), &expectedNetworksAnnotation)
					Expect(err).ToNot(HaveOccurred(), "should convert expected annotation as json to multus.NetworkSelectionElement")

					By("Compare between each obtained and expected network request")
					for _, expectedNetwork := range expectedNetworksAnnotation {
						Expect(obtainedNetworksAnnotation).To(ContainElement(expectedNetwork))
					}
				},
				table.Entry("with single ip-address request as string array",
					`[{"name":"ovs-conf","namespace":"default","ips":["10.10.0.1"],"mac":"02:00:00:00:00:00"}]`),
				table.Entry("with multiple ip-address request as string array",
					`[{"name":"ovs-conf","namespace":"default","ips":["10.10.0.1","10.10.0.2","10.0.0.3"],"mac":"02:00:00:00:00:00"}]`),
				table.Entry("with multiple networks requsets", `[
						{"name":"ovs-conf","namespace":"default","ips":["10.10.0.1","10.10.0.2","10.0.0.3"],"mac":"02:00:00:00:00:00"},
						{"name":"cnv-bridge","namespace":"openshift-cnv","ips":["192.168.66.100","192.168.66.101"],"mac":"02:F0:F0:F0:F0:F0"}
				]`),
			)
			It("should fail to allocate requested mac-address, with ip-address request as string instead of string array", func() {
				pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "testPod", Namespace: "default"}}
				pod.Annotations = map[string]string{
					networksAnnotation: `[{"name":"ovs-conf","namespace":"default","ips":"10.10.0.1","mac":"02:00:00:00:00:00"}]`}

				By("Request specific mac-address by adding the address to the networks pod annotation")
				err := poolManager.AllocatePodMac(&pod)
				Expect(err).To(HaveOccurred(), "should fail to allocate mac address due to bad annotation format")
			})
		})
	})
})
