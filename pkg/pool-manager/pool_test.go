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
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	multus "github.com/intel/multus-cni/types"
	"github.com/onsi/ginkgo/extensions/table"
	"k8s.io/api/admissionregistration/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	kubevirt "kubevirt.io/client-go/api/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const testManagerNamespace = "kubemacpool-system"

var _ = Describe("Pool", func() {
	beforeAllocationAnnotation := map[string]string{networksAnnotation: `[{ "name": "ovs-conf"}]`}
	afterAllocationAnnotation := map[string]string{networksAnnotation: `[{"name":"ovs-conf","namespace":"default","mac":"02:00:00:00:00:00"}]`}
	samplePod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "podpod", Namespace: "default", Annotations: afterAllocationAnnotation}}
	vmConfigMap := v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: testManagerNamespace, Name: names.WAITING_VMS_CONFIGMAP}}

	createPoolManager := func(startMacAddr, endMacAddr string, fakeObjectsForClient ...runtime.Object) *PoolManager {
		fakeClient := fake.NewSimpleClientset(fakeObjectsForClient...)
		startPoolRangeEnv, err := net.ParseMAC(startMacAddr)
		Expect(err).ToNot(HaveOccurred(), "should successfully parse starting mac address range")
		endPoolRangeEnv, err := net.ParseMAC(endMacAddr)
		Expect(err).ToNot(HaveOccurred(), "should successfully parse ending mac address range")
		poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
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

		table.DescribeTable("should check that a mac pool size is reported correctly", func(startMacAddr, endMacAddr string, expectedSize float64, needToSucceed bool) {
			startMacAddrHW, err := net.ParseMAC(startMacAddr)
			Expect(err).ToNot(HaveOccurred(), "Should succeed parsing startMacAddr")
			endMacAddrHW, err := net.ParseMAC(endMacAddr)
			Expect(err).ToNot(HaveOccurred(), "Should succeed parsing endMacAddr")
			poolSize, err := GetMacPoolSize(startMacAddrHW, endMacAddrHW)
			if needToSucceed {
				Expect(err).ToNot(HaveOccurred(), "Should succeed getting Mac Pool size")
				Expect(float64(poolSize)).To(Equal(expectedSize), "Should get the expected pool size value")
			} else {
				Expect(err).To(HaveOccurred(), "Should fail getting Mac Pool size duu to invalid params")
			}
		},
			table.Entry("Start: 40:00:00:00:00:00  End: 50:00:00:00:00:00 should succeed", "40:00:00:00:00:00", "50:00:00:00:00:00", math.Pow(2, 11*4)+1, true),
			table.Entry("Start: 02:00:00:00:00:00  End: 03:00:00:00:00:00 should succeed", "02:00:00:00:00:00", "03:00:00:00:00:00", math.Pow(2, 10*4)+1, true),
			table.Entry("Start: 02:00:00:00:00:00  End: 02:01:00:00:00:00 should succeed", "02:00:00:00:00:00", "02:01:00:00:00:00", math.Pow(2, 8*4)+1, true),
			table.Entry("Start: 02:00:00:00:00:00  End: 02:00:00:10:00:00 should succeed", "02:00:00:00:00:00", "02:00:00:10:00:00", math.Pow(2, 5*4)+1, true),
			table.Entry("Start: 02:00:00:00:00:10  End: 02:00:00:00:00:00 should succeed", "02:00:00:00:00:00", "02:00:00:00:00:10", math.Pow(2, 1*4)+1, true),
			table.Entry("Start: 00:00:00:00:00:01  End: 00:00:00:00:00:00 should fail", "00:00:00:00:00:01", "00:00:00:00:00:00", float64(0), false),
			table.Entry("Start: 80:00:00:00:00:00  End: 00:00:00:00:00:00 should fail", "80:00:00:00:00:00", "00:00:00:00:00:00", float64(0), false),
			table.Entry("Start: FF:FF:FF:FF:FF:FF  End: FF:FF:FF:FF:FF:FF should fail", "FF:FF:FF:FF:FF:FF", "FF:FF:FF:FF:FF:FF", float64(0), false),
			table.Entry("Start: 00:00:00:00:00:00  End: 00:00:00:00:00:00 should fail", "00:00:00:00:00:00", "00:00:00:00:00:00", float64(0), false),
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
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("Invalid range. rangeStart: 0a:00:00:00:00:00 rangeEnd: 02:00:00:00:00:00"))

		})

		It("should fail to pool manager because of the first octet of RangeStart is not 2, 6, A, E", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("03:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("06:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("RangeStart is invalid: invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is 0X3"))

		})

		It("should fail to create a pool manager object when the first octet of RangeEnd is not 2, 6, A, E", func() {
			fakeClient := fake.NewSimpleClientset()
			startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			endPoolRangeEnv, err := net.ParseMAC("05:00:00:00:00:00")
			Expect(err).ToNot(HaveOccurred())
			_, err = NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("RangeEnd is invalid: invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is 0X5"))
		})
	})

	Describe("Pool Manager Functions For VM", func() {

		logger := logf.Log.WithName("pool Test")
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

			err := poolManager.AllocateVirtualMachineMac(&newVM, logger)
			Expect(err).ToNot(HaveOccurred())

			Expect(len(poolManager.macPoolMap)).To(Equal(2))
			_, exist := poolManager.macPoolMap["02:00:00:00:00:00"]
			Expect(exist).To(BeTrue())
			_, exist = poolManager.macPoolMap["02:00:00:00:00:01"]
			Expect(exist).To(BeTrue())

			Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:01"))

			err = poolManager.ReleaseVirtualMachineMac(&newVM, logger)
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

			err := poolManager.AllocateVirtualMachineMac(&newVM, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(poolManager.macPoolMap)).To(Equal(0))
		})
		It("should allocate a new mac and release it for multiple interfaces", func() {
			poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &samplePod, &vmConfigMap)
			newVM := multipleInterfacesVM.DeepCopy()
			newVM.Name = "newVM"

			err := poolManager.AllocateVirtualMachineMac(newVM, logger)
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
			It("should preserve disk.io configuration on update", func() {
				addDiskIO := func(vm *kubevirt.VirtualMachine, ioName kubevirt.DriverIO) {
					vm.Spec.Template.Spec.Domain.Devices.Disks = make([]kubevirt.Disk, 1)
					vm.Spec.Template.Spec.Domain.Devices.Disks[0].IO = ioName
				}
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				addDiskIO(newVM, "native-new")
				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Disks[0].IO).To(Equal(kubevirt.DriverIO("native-new")), "disk.io configuration must be preserved after mac allocation")

				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				addDiskIO(updateVm, "native-update")
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Disks[0].IO).To(Equal(kubevirt.DriverIO("native-update")), "disk.io configuration must be preserved after mac allocation update")
			})
			It("should preserve mac addresses on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"
				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
			})
			It("should preserve mac addresses and allocate a requested one on update", func() {
				poolManager := createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:02", &vmConfigMap)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress = "01:00:00:00:00:02"
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, logger)
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

				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:02"]
				Expect(exist).To(BeFalse())

				updatedVM := multipleInterfacesVM.DeepCopy()
				updatedVM.Name = "newVM"
				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				updatedVM.Spec.Template.Spec.Networks = append(updatedVM.Spec.Template.Spec.Networks, anotherMultusNetwork)

				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM, logger)
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

				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[2].MacAddress).To(Equal("02:00:00:00:00:02"))

				updatedVM := multipleInterfacesVM.DeepCopy()
				updatedVM.Name = "newVM"

				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM, logger)
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

				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:01"))

				updatedVM := sampleVM.DeepCopy()
				updatedVM.Name = "newVM"

				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				updatedVM.Spec.Template.Spec.Networks = append(updatedVM.Spec.Template.Spec.Networks, anotherMultusNetwork)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:00"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress).To(Equal("02:00:00:00:00:02"))
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].Name).To(Equal("another-multus"))

				_, exist := poolManager.macPoolMap["02:00:00:00:00:01"]
				Expect(exist).To(BeFalse())
			})
		})
		Context("check create a vm with mac address allocation", func() {
			var (
				newVM        *kubevirt.VirtualMachine
				poolManager  *PoolManager
				allocatedMac string
			)
			BeforeEach(func() {
				poolManager = createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:01", &vmConfigMap)
				newVM = masqueradeVM.DeepCopy()
				newVM.Name = "newVM"

				By("Create a VM")
				err := poolManager.AllocateVirtualMachineMac(newVM, logger)
				Expect(err).ToNot(HaveOccurred(), "should successfully  allocated macs")

				allocatedMac = newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
			})
			It("should set a mac in configmap with new mac", func() {
				By("get configmap")
				configMap, err := poolManager.kubeClient.CoreV1().ConfigMaps(poolManager.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred(), "should successfully get configmap")

				By("checking the configmap is updated with mac allocated")
				macAddressInConfigMapFormat := strings.Replace(allocatedMac, ":", "-", 5)
				Expect(configMap.Data).To(HaveLen(1), "configmap should hold the mac address waiting for approval")
				_, exist := configMap.Data[macAddressInConfigMapFormat]
				Expect(exist).To(Equal(true), "should have an entry of the mac in the configmap")
			})
			It("should set a mac in pool cache in AllocationStatusWaitingForPod status", func() {
				Expect(poolManager.macPoolMap).To(HaveLen(1), "macPoolMap should hold the mac address waiting for approval")
				Expect(poolManager.macPoolMap[allocatedMac]).To(Equal(AllocationStatusWaitingForPod), "macPoolMap's mac's status should be set to AllocationStatusWaitingForPod status")
			})
			Context("and VM is marked as ready", func() {
				BeforeEach(func() {
					By("mark the vm as allocated")
					err := poolManager.MarkVMAsReady(newVM, log.WithName("fake-Reconcile"))
					Expect(err).ToNot(HaveOccurred(), "should mark allocated macs as valid")
				})
				It("should successfully allocate the first mac in the range", func() {
					By("check mac allocated as expected")
					Expect(allocatedMac).To(Equal("02:00:00:00:00:01"), "should successfully allocate the first mac in the range")
				})
				It("should properly update the configmap after vm creation", func() {
					By("check configmap is empty")
					configMap, err := poolManager.kubeClient.CoreV1().ConfigMaps(poolManager.managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred(), "should successfully get configmap")
					Expect(configMap.Data).To(BeEmpty(), "configmap should hold no more mac addresses for approval")
				})
				It("should properly update the pool cache after vm creation", func() {
					By("check allocated pool is populated and set to AllocationStatusAllocated status")
					Expect(poolManager.macPoolMap[allocatedMac]).To(Equal(AllocationStatusAllocated), "macPoolMap's mac's status should be set to AllocationStatusAllocated status")
				})
				It("should check no mac is inserted if the pool does not contain the mac address", func() {
					By("deleting the mac from the pool")
					delete(poolManager.macPoolMap, allocatedMac)

					By("re-marking the vm as ready")
					err := poolManager.MarkVMAsReady(newVM, log.WithName("fake-Reconcile"))
					Expect(err).ToNot(HaveOccurred(), "should not return err if there are no macs to mark as ready")

					By("checking the pool cache is not updated")
					Expect(poolManager.macPoolMap).To(BeEmpty(), "macPoolMap should be empty")
				})
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

	type isNamespaceSelectorCompatibleWithOptModeLabelParams struct {
		mutatingWebhookConfigurationName string
		namespaceName                    string
		optMode                          OptMode
		ErrorTextExpected                string
		expectedResult                   bool
		failureDescription               string
	}

	Describe("isNamespaceSelectorCompatibleWithOptModeLabel API Tests", func() {
		webhookName := "webhook"
		LabelKey := "targetLabel.kubemacpool.io"
		includingValue := "allocate"
		excludingValue := "ignore"
		optOutMutatingWebhookConfigurationName := "optOutMutatingWebhookConfiguration"
		optInMutatingWebhookConfigurationName := "optInMutatingWebhookConfiguration"
		namespaceWithIncludingLabelName := "withIncludingLabel"
		namespaceWithExcludingLabelName := "withExcludingLabel"
		namespaceWithNoLabelsName := "withNoLabels"
		namespaceWithIrrelevantLabelsName := "withIrrelevantLabels"

		optOutMutatingWebhookConfiguration := &v1beta1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: optOutMutatingWebhookConfigurationName,
			},
			Webhooks: []v1beta1.MutatingWebhook{
				v1beta1.MutatingWebhook{
					Name: webhookName,
					NamespaceSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							metav1.LabelSelectorRequirement{
								Key:      "runlevel",
								Operator: "NotIn",
								Values:   []string{"0", "1"},
							},
							metav1.LabelSelectorRequirement{
								Key:      "openshift.io/run-level",
								Operator: "NotIn",
								Values:   []string{"0", "1"},
							},
							metav1.LabelSelectorRequirement{
								Key:      LabelKey,
								Operator: "NotIn",
								Values:   []string{excludingValue},
							},
						},
					},
				},
			},
		}
		optInMutatingWebhookConfiguration := &v1beta1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: optInMutatingWebhookConfigurationName,
			},
			Webhooks: []v1beta1.MutatingWebhook{
				v1beta1.MutatingWebhook{
					Name: webhookName,
					NamespaceSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							metav1.LabelSelectorRequirement{
								Key:      "runlevel",
								Operator: "NotIn",
								Values:   []string{"0", "1"},
							},
							metav1.LabelSelectorRequirement{
								Key:      "openshift.io/run-level",
								Operator: "NotIn",
								Values:   []string{"0", "1"},
							},
							metav1.LabelSelectorRequirement{
								Key:      LabelKey,
								Operator: "In",
								Values:   []string{includingValue},
							},
						},
					},
				},
			},
		}
		namespaceWithIncludingLabel := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespaceWithIncludingLabelName,
				Labels: map[string]string{LabelKey: includingValue},
			},
		}
		namespaceWithExcludingLabel := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespaceWithExcludingLabelName,
				Labels: map[string]string{LabelKey: excludingValue},
			},
		}
		namespaceWithNoLabels := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespaceWithNoLabelsName,
			},
		}
		namespaceWithIrrelevantLabels := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespaceWithIrrelevantLabelsName,
				Labels: map[string]string{"other": "label"},
			},
		}
		var poolManager *PoolManager
		BeforeEach(func() {
			poolManager = createPoolManager("02:00:00:00:00:00", "02:00:00:00:00:01", optOutMutatingWebhookConfiguration, optInMutatingWebhookConfiguration, namespaceWithIncludingLabel, namespaceWithExcludingLabel, namespaceWithNoLabels, namespaceWithIrrelevantLabels)
		})
		table.DescribeTable("Should return the expected namespace acceptance outcome according to the opt-mode or return an error",
			func(n *isNamespaceSelectorCompatibleWithOptModeLabelParams) {
				isNamespaceManaged, err := poolManager.isNamespaceSelectorCompatibleWithOptModeLabel(n.namespaceName, n.mutatingWebhookConfigurationName, webhookName, n.optMode)
				if n.ErrorTextExpected != "" {
					Expect(err).Should(MatchError(n.ErrorTextExpected), "isNamespaceSelectorCompatibleWithOptModeLabel should match expected error message")
				} else {
					Expect(err).ToNot(HaveOccurred(), "isNamespaceSelectorCompatibleWithOptModeLabel should not return an error")
				}

				Expect(isNamespaceManaged).To(Equal(n.expectedResult), n.failureDescription)
			},
			table.Entry("when opt-mode is opt-in and using a namespace with including label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIncludingLabelName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace when in opt-in and including label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-in and using a namespace with irrelevant label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIrrelevantLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should not include namespace by default unless including label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-in and using a namespace with no labels",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithNoLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should not include namespace by default unless including label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-in and using a namespace with excluding label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithExcludingLabelName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should not include namespace by default unless including label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-out and using a namespace with excluding label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithExcludingLabelName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should exclude namespace when in opt-out and excluding label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-out and using a namespace with irrelevant label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIrrelevantLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace by default unless excluding label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-out and using a namespace with no labels",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithNoLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace by default unless excluding label is set in the namespace",
				}),
			table.Entry("when opt-mode is opt-out and using a namespace with including label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithNoLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace by default unless excluding label is set in the namespace",
				}),
			table.Entry("when opt-mode parameter is not valid",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptMode("not-valid"),
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIncludingLabelName,
					ErrorTextExpected:                "Failed to check if namespaces are managed by default by opt-mode: opt-mode is not defined: not-valid",
					expectedResult:                   false,
					failureDescription:               "Should reject namespace if an error has occurred during the function operation",
				}),
			table.Entry("when namespace is not found",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    "non-existing-namespace-name",
					ErrorTextExpected:                "Failed to get Namespace: namespaces \"non-existing-namespace-name\" not found",
					expectedResult:                   false,
					failureDescription:               "Should reject namespace if an error has occurred during the function operation",
				}),
			table.Entry("when mutatingWebhookConfiguration is not found",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: "non-existing-mutatingWebhookConfiguration-name",
					namespaceName:                    namespaceWithIncludingLabelName,
					ErrorTextExpected:                "Failed lookup webhook in MutatingWebhookConfig: Failed to get mutatingWebhookConfig: mutatingwebhookconfigurations.admissionregistration.k8s.io \"non-existing-mutatingWebhookConfiguration-name\" not found",
					expectedResult:                   false,
					failureDescription:               "Should reject namespace if an error has occurred during the function operation",
				}),
		)
	})
})
