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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	multus "gopkg.in/k8snetworkplumbingwg/multus-cni.v3/pkg/types"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	kubevirt "kubevirt.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const (
	testManagerNamespace    = "kubemacpool-system"
	managedNamespaceName    = "managedNamespaceName"
	notManagedNamespaceName = "notManagedNamespaceName"
)

var _ = Describe("Pool", func() {
	beforeAllocationAnnotation := map[string]string{networkv1.NetworkAttachmentAnnot: `[{ "name": "ovs-conf"}]`}
	afterAllocationAnnotation := func(namespace, macAddress string) map[string]string {
		return map[string]string{networkv1.NetworkAttachmentAnnot: `[{"name":"ovs-conf","namespace":"` + namespace + `","mac":"` + macAddress + `","cni-args":null}]`}
	}
	const (
		managedNamespaceMAC   = "03:00:00:00:00:00"
		unmanagedNamespaceMAC = "03:00:00:00:00:FF"

		minRangeMACPool = "02:00:00:00:00:00"
		maxRangeMACPool = "02:FF:FF:FF:FF:FF"
	)
	managedPodWithMacAllocated := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "podpod",
			Namespace:   managedNamespaceName,
			Annotations: afterAllocationAnnotation(managedNamespaceName, managedNamespaceMAC),
		},
	}

	unmanagedPodWithMacAllocated := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "unmanagedPod",
			Namespace:   notManagedNamespaceName,
			Annotations: afterAllocationAnnotation(notManagedNamespaceName, unmanagedNamespaceMAC),
		},
	}

	vmConfigMap := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testManagerNamespace,
			Name:      names.WAITING_VMS_CONFIGMAP,
		},
	}

	Describe("Internal Functions", func() {
		DescribeTable("should return the next mac address", func(macAddr, nextMacAddr string) {
			macAddrHW, err := net.ParseMAC(macAddr)
			Expect(err).ToNot(HaveOccurred())
			ExpectedMacAddrHW, err := net.ParseMAC(nextMacAddr)
			Expect(err).ToNot(HaveOccurred())
			nextMacAddrHW := getNextMac(macAddrHW)
			Expect(nextMacAddrHW).To(Equal(ExpectedMacAddrHW))
		},
			Entry("02:00:00:00:00:00 -> 02:00:00:00:00:01", "02:00:00:00:00:00", "02:00:00:00:00:01"),
			Entry("02:00:00:00:00:FF -> 02:00:00:00:01:00", "02:00:00:00:00:FF", "02:00:00:00:01:00"),
			Entry("FF:FF:FF:FF:FF:FF -> 00:00:00:00:00:00", "FF:FF:FF:FF:FF:FF", "00:00:00:00:00:00"),
		)

		DescribeTable("should check range", func(startMacAddr, endMacAddr string, needToFail bool) {
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
			Entry("Start: 02:00:00:00:00:00  End: 02:00:00:00:00:01", "02:00:00:00:00:00", "02:00:00:00:00:01", false),
			Entry("Start: 02:00:00:00:00:00  End: 02:10:00:00:00:00", "02:00:00:00:00:00", "02:10:00:00:00:00", false),
			Entry("Start: 02:FF:00:00:00:00  End: 02:00:00:00:00:00", "02:FF:00:00:00:00", "00:00:00:00:00:00", true),
		)

		DescribeTable("should check that the multicast bit is off", func(MacAddr string, shouldFail bool) {
			MacAddrHW, err := net.ParseMAC(MacAddr)
			Expect(err).ToNot(HaveOccurred())
			err = checkCast(MacAddrHW)
			if shouldFail {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
			Entry("Valid address: 02:00:00:00:00:00", "02:00:00:00:00:00", false),
			Entry("Valid address: 06:00:00:00:00:00", "06:00:00:00:00:00", false),
			Entry("Valid address: 0A:00:00:00:00:00", "0A:00:00:00:00:00", false),
			Entry("Valid address: 0E:00:00:00:00:00", "0E:00:00:00:00:00", false),
			Entry("Invalid address: 01:FF:00:00:00:00, the first octet is not 02, 06, 0A or 0E", "01:FF:00:00:00:00", true),
			Entry("Invalid address: FF:FF:00:00:00:00, the first octet is not 02, 06, 0A or 0E", "FF:FF:00:00:00:00", true),
		)

		DescribeTable("should check that a mac pool size is reported correctly", func(startMacAddr, endMacAddr string, expectedSize float64, needToSucceed bool) {
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
			Entry("Start: 40:00:00:00:00:00  End: 50:00:00:00:00:00 should succeed", "40:00:00:00:00:00", "50:00:00:00:00:00", math.Pow(2, 11*4)+1, true),
			Entry("Start: 02:00:00:00:00:00  End: 03:00:00:00:00:00 should succeed", "02:00:00:00:00:00", "03:00:00:00:00:00", math.Pow(2, 10*4)+1, true),
			Entry("Start: 02:00:00:00:00:00  End: 02:01:00:00:00:00 should succeed", "02:00:00:00:00:00", "02:01:00:00:00:00", math.Pow(2, 8*4)+1, true),
			Entry("Start: 02:00:00:00:00:00  End: 02:00:00:10:00:00 should succeed", "02:00:00:00:00:00", "02:00:00:10:00:00", math.Pow(2, 5*4)+1, true),
			Entry("Start: 02:00:00:00:00:10  End: 02:00:00:00:00:00 should succeed", "02:00:00:00:00:00", "02:00:00:00:00:10", math.Pow(2, 1*4)+1, true),
			Entry("Start: 00:00:00:00:00:01  End: 00:00:00:00:00:00 should fail", "00:00:00:00:00:01", "00:00:00:00:00:00", float64(0), false),
			Entry("Start: 80:00:00:00:00:00  End: 00:00:00:00:00:00 should fail", "80:00:00:00:00:00", "00:00:00:00:00:00", float64(0), false),
			Entry("Start: FF:FF:FF:FF:FF:FF  End: FF:FF:FF:FF:FF:FF should fail", "FF:FF:FF:FF:FF:FF", "FF:FF:FF:FF:FF:FF", float64(0), false),
			Entry("Start: 00:00:00:00:00:00  End: 00:00:00:00:00:00 should fail", "00:00:00:00:00:00", "00:00:00:00:00:00", float64(0), false),
		)
	})

	Describe("Pool Manager General Functions ", func() {
		It("should create a pool manager", func() {
			poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
			Expect(poolManager).ToNot(BeNil())
		})
		Context("check NewPoolManager", func() {
			It("should fail to create pool manager when rangeStart is greater than rangeEnd", func() {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
				startPoolRangeEnv, err := net.ParseMAC("0A:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				endPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				_, err = NewPoolManager(fakeClient, fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("Invalid range. rangeStart: 0a:00:00:00:00:00 rangeEnd: 02:00:00:00:00:00"))

			})

			It("should fail to pool manager because of the first octet of RangeStart is not 2, 6, A, E", func() {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
				startPoolRangeEnv, err := net.ParseMAC("03:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				endPoolRangeEnv, err := net.ParseMAC("06:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				_, err = NewPoolManager(fakeClient, fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("RangeStart is invalid: invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is 0X3"))

			})

			It("should fail to create a pool manager object when the first octet of RangeEnd is not 2, 6, A, E", func() {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
				startPoolRangeEnv, err := net.ParseMAC("02:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				endPoolRangeEnv, err := net.ParseMAC("05:00:00:00:00:00")
				Expect(err).ToNot(HaveOccurred())
				_, err = NewPoolManager(fakeClient, fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, 10)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("RangeEnd is invalid: invalid mac address. Multicast addressing is not supported. Unicast addressing must be used. The first octet is 0X5"))
			})

			Context("When poolManager is initialized when there are pods on managed and unmanaged namespaces", func() {
				var poolManager *PoolManager
				BeforeEach(func() {
					poolManager = createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode, &managedPodWithMacAllocated, &unmanagedPodWithMacAllocated)
					Expect(poolManager).ToNot(BeNil())
				})
				It("Should initialize the macPoolmap only with macs on the mananged pods", func() {
					Expect(poolManager.macPoolMap).To(HaveLen(1))
					entry, exist := poolManager.macPoolMap[NewMacKey(managedNamespaceMAC)]
					Expect(exist).To(BeTrue(), "should include the mac allocated by the managed pod")
					expectMacEntry := macEntry{
						instanceName:         fmt.Sprintf("pod/%s/%s", managedPodWithMacAllocated.Namespace, managedPodWithMacAllocated.Name),
						macInstanceKey:       "ovs-conf",
						transactionTimestamp: nil,
					}
					Expect(entry).To(Equal(expectMacEntry))
				})

			})
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

		podNetwork := kubevirt.Network{
			Name: "pod",
			NetworkSource: kubevirt.NetworkSource{
				Pod: &kubevirt.PodNetwork{},
			},
		}

		multusNetwork := kubevirt.Network{
			Name: "multus",
			NetworkSource: kubevirt.NetworkSource{
				Multus: &kubevirt.MultusNetwork{
					NetworkName: "multus",
				},
			},
		}

		anotherMultusNetwork := kubevirt.Network{
			Name: "another-multus",
			NetworkSource: kubevirt.NetworkSource{
				Multus: &kubevirt.MultusNetwork{
					NetworkName: "another-multus",
				},
			},
		}

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

		duplicateInterfacesVM := kubevirt.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}, Spec: kubevirt.VirtualMachineSpec{
			Template: &kubevirt.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirt.VirtualMachineInstanceSpec{
					Domain: kubevirt.DomainSpec{
						Devices: kubevirt.Devices{
							Interfaces: []kubevirt.Interface{masqueradeInterface, multusBridgeInterface, multusBridgeInterface}}},
					Networks: []kubevirt.Network{podNetwork, multusNetwork}}}}}

		updateTransactionTimestamp := func(secondsPassed time.Duration) time.Time {
			return time.Now().Add(secondsPassed * time.Second)
		}
		It("should reject allocation MAC pool is full", func() {
			const (
				minRangeMACSmallPool = "02:00:00:00:00:00"
				maxRangeMACSmallPool = "02:00:00:00:00:01"
				smallMACPoolSize     = 2
			)

			poolManager := createPoolManager(minRangeMACSmallPool, maxRangeMACSmallPool, OptOutMode)
			transactionTimestamp := updateTransactionTimestamp(0)

			for i := 0; i < smallMACPoolSize; i++ {
				vm := masqueradeVM.DeepCopy()
				vm.Name = fmt.Sprintf("newVM%d", i)
				Expect(poolManager.AllocateVirtualMachineMac(vm, &transactionTimestamp, true, logger)).To(Succeed())
			}

			vm := masqueradeVM.DeepCopy()
			vm.Name = fmt.Sprintf("newVM-full")
			Expect(poolManager.AllocateVirtualMachineMac(vm, &transactionTimestamp, true, logger)).To(MatchError(ErrFull))

		})
		It("should reject allocation if there are interfaces with the same name", func() {
			poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
			newVM := duplicateInterfacesVM.DeepCopy()
			newVM.Name = "duplicateInterfacesVM"

			transactionTimestamp := updateTransactionTimestamp(0)
			err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
			Expect(err).To(HaveOccurred(), "Should reject an allocation of a vm with duplicate interface names")
			Expect(poolManager.macPoolMap).To(BeEmpty(), "Should not allocate macs if there are duplicate interfaces")
		})
		It("should not allocate a new mac for bridge interface on pod network", func() {
			poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
			newVM := sampleVM
			newVM.Name = "newVM"

			transactionTimestamp := updateTransactionTimestamp(0)
			err := poolManager.AllocateVirtualMachineMac(&newVM, &transactionTimestamp, true, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(poolManager.macPoolMap).To(BeEmpty(), "Should not allocate mac for unsupported bridge binding")
		})
		Context("and there is a pre-existing pod with mac allocated to it", func() {
			var poolManager *PoolManager
			BeforeEach(func() {
				poolManager = createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode, &managedPodWithMacAllocated)
			})
			It("should allocate a new mac and release it for masquerade", func() {
				newVM := masqueradeVM
				newVM.Name = "newVM"
				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(&newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())

				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{managedNamespaceMAC})).To(Succeed(), "Failed to check macs in macMap")

				err = poolManager.ReleaseAllVirtualMachineMacs(VmNamespaced(&newVM), logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(poolManager.macPoolMap).To(HaveLen(1), "Should keep the pod mac in the macMap")
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, []string{}, []string{managedNamespaceMAC})).To(Succeed(), "Failed to check macs in macMap")
			})
			It("should allocate a new mac and release it for multiple interfaces", func() {
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())

				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{managedNamespaceMAC})).To(Succeed(), "Failed to check macs in macMap")

				err = poolManager.ReleaseAllVirtualMachineMacs(VmNamespaced(newVM), logf.Log.WithName("VirtualMachine Controller"))
				Expect(err).ToNot(HaveOccurred())
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, []string{}, []string{managedNamespaceMAC})).To(Succeed(), "Failed to check macs in macMap")
			})
		})
		Describe("Update vm object", func() {
			It("should preserve disk.io configuration on update", func() {
				addDiskIO := func(vm *kubevirt.VirtualMachine, ioName kubevirt.DriverIO) {
					vm.Spec.Template.Spec.Domain.Devices.Disks = make([]kubevirt.Disk, 1)
					vm.Spec.Template.Spec.Domain.Devices.Disks[0].IO = ioName
				}
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				addDiskIO(newVM, "native-new")
				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Disks[0].IO).To(Equal(kubevirt.DriverIO("native-new")), "disk.io configuration must be preserved after mac allocation")

				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				addDiskIO(updateVm, "native-update")
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(updateVm.Spec.Template.Spec.Domain.Devices.Disks[0].IO).To(Equal(kubevirt.DriverIO("native-update")), "disk.io configuration must be preserved after mac allocation update")
			})
			It("should reject update allocation if there are interfaces with the same name", func() {
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "multipleInterfacesVM"

				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

				updateVm := newVM.DeepCopy()
				newTransactionTimestamp := updateTransactionTimestamp(1)
				updateVm.Spec.Template.Spec.Domain.Devices.Interfaces = append(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces, multusBridgeInterface)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, &newTransactionTimestamp, true, logger)
				Expect(err).To(HaveOccurred(), "Should reject an update with duplicate interface names")
				expectedMACsAfterRejection := expectedUpdatedMACs
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedMACsAfterRejection, []string{})).To(Succeed(), "Failed to check macs in macMap")
			})
			It("should preserve mac addresses on update", func() {
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"
				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

				By("Updating the vm with no mac allocated")
				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				newTransactionTimestamp := updateTransactionTimestamp(1)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, &newTransactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &newTransactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")
			})
			It("should preserve mac addresses and allocate a requested one on update", func() {
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

				By("Updating the vm with no mac allocated")
				updateVm := multipleInterfacesVM.DeepCopy()
				updateVm.Name = "newVM"
				By("changing one of the macs")
				const changedMAC = "01:00:00:00:00:02"
				updateVm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress = changedMAC
				newTransactionTimestamp := updateTransactionTimestamp(1)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updateVm, &newTransactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				oldInterfaceMACs := expectedUpdatedMACs
				Expect(interfaceMACsToList(updateVm.Spec.Template.Spec.Domain.Devices.Interfaces)).To(ContainElement(changedMAC))
				By("asserting that both old and new MACs are set on the pool")
				expectedUpdatedMACs = append(oldInterfaceMACs, changedMAC)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &newTransactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")
			})
			It("should allow to add a new interface on update", func() {
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(newVM.Spec.Template.Spec.Domain.Devices.Interfaces).To(HaveLen(2))
				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

				updatedVM := newVM.DeepCopy()
				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				updatedVM.Spec.Template.Spec.Networks = append(updatedVM.Spec.Template.Spec.Networks, anotherMultusNetwork)
				NewTransactionTimestamp := updateTransactionTimestamp(1)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM, &NewTransactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces).To(HaveLen(3))
				expectedNotUpdatedMacs := expectedUpdatedMACs
				expectedUpdatedMACs = []string{updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[2].MacAddress}
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &NewTransactionTimestamp, expectedUpdatedMACs, expectedNotUpdatedMacs)).To(Succeed(), "Failed to check macs in macMap")
			})
			It("should allow to remove an interface on update", func() {
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"
				newVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(newVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				newVM.Spec.Template.Spec.Networks = append(newVM.Spec.Template.Spec.Networks, anotherMultusNetwork)

				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

				updatedVM := multipleInterfacesVM.DeepCopy()
				updatedVM.Name = "newVM"
				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress = newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress = newVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress
				NewTransactionTimestamp := updateTransactionTimestamp(1)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM, &NewTransactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				expectedUpdatedMACs = []string{newVM.Spec.Template.Spec.Domain.Devices.Interfaces[2].MacAddress}
				expectedNotUpdatedMACs := []string{updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress, updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress}
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &NewTransactionTimestamp, expectedUpdatedMACs, expectedNotUpdatedMACs)).To(Succeed(), "Failed to check macs in macMap")
			})
			It("should allow to remove and add an interface on update", func() {
				poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				newVM := multipleInterfacesVM.DeepCopy()
				newVM.Name = "newVM"

				transactionTimestamp := updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				expectedUpdatedMACs := interfaceMACsToList(newVM.Spec.Template.Spec.Domain.Devices.Interfaces)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &transactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

				By("Updating the vm with no mac allocated")
				updatedVM := sampleVM.DeepCopy()
				updatedVM.Name = "newVM"
				By("adding another multus interface")
				updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces, anotherMultusBridgeInterface)
				updatedVM.Spec.Template.Spec.Networks = append(updatedVM.Spec.Template.Spec.Networks, anotherMultusNetwork)
				NewTransactionTimestamp := updateTransactionTimestamp(1)
				err = poolManager.UpdateMacAddressesForVirtualMachine(newVM, updatedVM, &NewTransactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].Name).To(Equal("another-multus"))
				oldInterfaceMACs := expectedUpdatedMACs
				expectedUpdatedMACs = append(oldInterfaceMACs, updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(checkMacPoolMapEntries(poolManager.macPoolMap, &NewTransactionTimestamp, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")
			})
		})
		Context("creating a vm with mac address", func() {
			var (
				poolManager  *PoolManager
				allocatedMac string
			)
			// in this context we will not use updateTransactionTimestamp() as we want to control the exact time of each timestamp
			// Freeze time
			now := time.Now()
			vmCreationTimestamp := now
			vmFirstUpdateTimestamp := now.Add(time.Duration(1) * time.Second)
			vmSecondUpdateTimestamp := now.Add(time.Duration(2) * time.Second)
			BeforeEach(func() {
				poolManager = createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
			})
			var vm, vmFirstUpdate, vmSecondUpdate *kubevirt.VirtualMachine
			var vmLastPersistedTransactionTimestampAnnotation *time.Time
			BeforeEach(func() {
				By("Creating a vm")
				vm = masqueradeVM.DeepCopy()
				vm.Name = "testVm"

				err := poolManager.AllocateVirtualMachineMac(vm, &vmCreationTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred())
				allocatedMac = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				macEntry, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
				Expect(exist).To(BeTrue(), "mac should be updated in the macPoolMap")
				Expect(macEntry.transactionTimestamp).To(Equal(&vmCreationTimestamp), "mac Entry should update transaction timestamp")

				By("simulating the vm creation as persisted")
				vmLastPersistedTransactionTimestampAnnotation = &vmCreationTimestamp

				By("marking the vm mac as allocated")
				err = poolManager.MarkVMAsReady(vm, vmLastPersistedTransactionTimestampAnnotation, log.WithName("fake-Reconcile"))
				Expect(err).ToNot(HaveOccurred(), "should mark allocated macs as valid")
				macEntry, exist = poolManager.macPoolMap[NewMacKey(allocatedMac)]
				Expect(exist).To(BeTrue(), "mac should be updated in the macPoolMap")
				Expect(macEntry.transactionTimestamp).To(BeNil(), "mac Entry should update transaction timestamp")
			})
			Context("and a first update is set to the vm after the vm creation persisted, removing the mac", func() {
				BeforeEach(func() {
					By("updating the vm, removing the interface")
					vmFirstUpdate = vm.DeepCopy()
					vmFirstUpdate.Spec.Template.Spec.Domain.Devices.Interfaces = vmFirstUpdate.Spec.Template.Spec.Domain.Devices.Interfaces[:0]

					By("updating the vm, removing the interface")
					err := poolManager.UpdateMacAddressesForVirtualMachine(vm, vmFirstUpdate, &vmFirstUpdateTimestamp, true, logger)
					Expect(err).ToNot(HaveOccurred(), "should update vm with no error")
					macEntry, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
					Expect(exist).To(BeTrue(), "mac should be updated in the macPoolMap after first update")
					Expect(macEntry.transactionTimestamp).To(Equal(&vmFirstUpdateTimestamp), "mac Entry should update transaction timestamp")

					By("simulating the vm first update as persisted")
					vmLastPersistedTransactionTimestampAnnotation = &vmFirstUpdateTimestamp

					By("marking the vm mac as allocated by controller reconcile")
					err = poolManager.MarkVMAsReady(vmFirstUpdate, vmLastPersistedTransactionTimestampAnnotation, log.WithName("fake-Reconcile"))
					Expect(err).ToNot(HaveOccurred(), "should not mark vm as ready with no errors")
				})
				It("should set the macs updated as ready", func() {
					_, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
					Expect(exist).To(BeFalse(), "mac should be updated in the macPoolMap after first update persisted")
				})

				Context("and a second update is set before the first change update was persisted", func() {
					BeforeEach(func() {
						By("updating the vm, re-adding the interface")
						vmSecondUpdate = vm.DeepCopy()
						By("updating the vm, removing the interface")
						err := poolManager.UpdateMacAddressesForVirtualMachine(vmFirstUpdate, vmSecondUpdate, &vmSecondUpdateTimestamp, true, logger)
						Expect(err).ToNot(HaveOccurred(), "should update vm with no error")
						macEntry, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
						Expect(exist).To(BeTrue(), "mac should be updated in the macPoolMap after first update")
						Expect(macEntry.transactionTimestamp).To(Equal(&vmSecondUpdateTimestamp), "mac Entry should update transaction timestamp")
					})
					Context("and the first update's controller reconcile is set before second update is persisted", func() {
						BeforeEach(func() {
							By("simulating the vm first update as persisted")
							vmLastPersistedTransactionTimestampAnnotation = &vmFirstUpdateTimestamp

							By("marking the vm mac as allocated by controller reconcile")
							err := poolManager.MarkVMAsReady(vmSecondUpdate, vmLastPersistedTransactionTimestampAnnotation, log.WithName("fake-Reconcile"))
							Expect(err).ToNot(HaveOccurred(), "should not mark vm as ready with no errors")
						})
						It("Should keep the entry since the last persisted timestamp annotation is still prior to the mac's transaction timestamp", func() {
							macEntry, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
							Expect(exist).To(BeTrue(), "mac should be in macMap until last update is persisted to make sure mac is safe from collisions from other updates")
							Expect(macEntry.transactionTimestamp).To(Equal(&vmSecondUpdateTimestamp), "mac Entry should not change until change is persisted")
						})
					})
					Context("and the first update's controller reconcile is set after the second update is persisted", func() {
						BeforeEach(func() {
							By("simulating the vm second update as persisted")
							vmLastPersistedTransactionTimestampAnnotation = &vmSecondUpdateTimestamp

							By("marking the vm mac as allocated by controller reconcile")
							err := poolManager.MarkVMAsReady(vmSecondUpdate, vmLastPersistedTransactionTimestampAnnotation, log.WithName("fake-Reconcile"))
							Expect(err).ToNot(HaveOccurred(), "should not mark vm as ready with no errors")
						})
						It("Should update the entry since the last persisted timestamp annotation is equal or later than the mac's transaction timestamp", func() {
							macEntry, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
							Expect(exist).To(BeTrue(), "mac should be in macMap since the last persisted change includes this mac")
							Expect(macEntry.transactionTimestamp).To(BeNil(), "mac Entry should change to ready after change persisted")
						})
					})
					Context("and the first update's controller reconcile is set and the second update is rejected", func() {
						BeforeEach(func() {
							By("simulating the vm second update as persisted")
							vmLastPersistedTransactionTimestampAnnotation = &vmFirstUpdateTimestamp

							By("marking the vm mac as allocated by controller reconcile")
							err := poolManager.MarkVMAsReady(vmSecondUpdate, vmLastPersistedTransactionTimestampAnnotation, log.WithName("fake-Reconcile"))
							Expect(err).ToNot(HaveOccurred(), "should not mark vm as ready with no errors")
						})
						It("Should keep the entry until a newer change is persisted or until the entry goes stale and removed by handleStaleLegacyConfigMapEntries", func() {
							macEntry, exist := poolManager.macPoolMap[NewMacKey(allocatedMac)]
							Expect(exist).To(BeTrue(), "mac should be in macMap until last update is persisted to make sure mac is safe from collisions from other updates")
							Expect(macEntry.transactionTimestamp).To(Equal(&vmSecondUpdateTimestamp), "mac Entry should not change")
						})
					})
				})
			})
		})
		Context("check create a vm with mac address allocation", func() {
			var (
				newVM                *kubevirt.VirtualMachine
				poolManager          *PoolManager
				allocatedMac         string
				expectedMacEntry     macEntry
				transactionTimestamp time.Time
			)
			BeforeEach(func() {
				poolManager = createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode, &vmConfigMap)
				newVM = masqueradeVM.DeepCopy()
				newVM.Name = "newVM"

				By("Create a VM")
				transactionTimestamp = updateTransactionTimestamp(0)
				err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, true, logger)
				Expect(err).ToNot(HaveOccurred(), "should successfully  allocated macs")

				allocatedMac = newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				expectedMacEntry = macEntry{
					transactionTimestamp: &transactionTimestamp,
					instanceName:         VmNamespaced(newVM),
					macInstanceKey:       newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].Name,
				}
			})
			It("should not set a mac in legacy configmap with new mac", func() {
				By("get configmap")
				configMap := v1.ConfigMap{}
				err := poolManager.kubeClient.Get(context.TODO(), types.NamespacedName{Namespace: poolManager.managerNamespace, Name: names.WAITING_VMS_CONFIGMAP}, &configMap)
				Expect(err).ToNot(HaveOccurred(), "should successfully get configmap")

				By("checking the configmap is not updated with mac allocated")
				Expect(configMap.Data).To(BeEmpty(), "configmap should not hold the mac address waiting for approval")
			})
			It("should set a mac in pool cache with updated transaction timestamp", func() {
				Expect(poolManager.macPoolMap).To(HaveLen(1), "macPoolMap should hold the mac address waiting for approval")
				Expect(poolManager.macPoolMap[NewMacKey(allocatedMac)]).To(Equal(expectedMacEntry), "macPoolMap's mac's entry should be as expected")
			})
			Context("and creating a dry run VM", func() {
				var macPoolMapCopy macMap
				BeforeEach(func() {
					macPoolMapCopy = macMap{}
					for key, value := range poolManager.macPoolMap {
						macPoolMapCopy[key] = value
					}

					newVMDryRun := masqueradeVM.DeepCopy()
					newVMDryRun.Name = "newVMDryRun"

					By("Create a dry run VM")
					transactionTimestamp = updateTransactionTimestamp(0)
					err := poolManager.AllocateVirtualMachineMac(newVM, &transactionTimestamp, false, logger)
					Expect(err).ToNot(HaveOccurred(), "should successfully create dry run VM")
				})
				It("should not storage the dry run mac in the mac pool", func() {
					Expect(poolManager.macPoolMap).To(Equal(macPoolMapCopy), "macPoolMap should left unchanged")
				})
			})
			Context("and VM is marked as ready by controller reconcile", func() {
				var lastPersistedtransactionTimstamp *time.Time
				BeforeEach(func() {
					By("Assuming that the webhook chain was not rejected")
					lastPersistedtransactionTimstamp = &transactionTimestamp

					By("mark the vm as allocated")
					err := poolManager.MarkVMAsReady(newVM, lastPersistedtransactionTimstamp, log.WithName("fake-Reconcile"))
					Expect(err).ToNot(HaveOccurred(), "should mark allocated macs as valid")

					expectedMacEntry = macEntry{
						transactionTimestamp: nil,
						instanceName:         VmNamespaced(newVM),
						macInstanceKey:       newVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].Name,
					}
				})
				It("should make sure legacy configmap is empty after vm creation", func() {
					By("check configmap is empty")
					configMap := v1.ConfigMap{}
					err := poolManager.kubeClient.Get(context.TODO(), types.NamespacedName{Namespace: poolManager.managerNamespace, Name: names.WAITING_VMS_CONFIGMAP}, &configMap)
					Expect(err).ToNot(HaveOccurred(), "should successfully get configmap")
					Expect(configMap.Data).To(BeEmpty(), "configmap should hold no more mac addresses for approval")
				})
				It("should properly update the pool cache after vm creation", func() {
					By("check allocated pool is populated and set to AllocationStatusAllocated status")
					Expect(poolManager.macPoolMap[NewMacKey(allocatedMac)]).To(Equal(expectedMacEntry), "updated macPoolMap's mac's entry should remove transaction timestamp")
				})
				It("should check no mac is inserted if the pool does not contain the mac address", func() {
					By("deleting the mac from the pool")
					delete(poolManager.macPoolMap, NewMacKey(allocatedMac))

					By("re-marking the vm as ready")
					err := poolManager.MarkVMAsReady(newVM, lastPersistedtransactionTimstamp, log.WithName("fake-Reconcile"))
					Expect(err).ToNot(HaveOccurred(), "should not return err if there are no macs to mark as ready")

					By("checking the pool cache is not updated")
					Expect(poolManager.macPoolMap).To(BeEmpty(), "macPoolMap should be empty")
				})
			})
		})
	})

	Describe("Pool Manager Functions For pod", func() {
		It("should allocate a new mac and release it", func() {
			poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode, &managedPodWithMacAllocated)
			newPod := managedPodWithMacAllocated
			newPod.Name = "newPod"
			newPod.Annotations = beforeAllocationAnnotation

			err := poolManager.AllocatePodMac(&newPod, true)
			Expect(err).ToNot(HaveOccurred())
			preAllocatedPodMAC := managedNamespaceMAC
			allocatedMAC, err := getAllocatedMacByInterface(newPod.Annotations[networkv1.NetworkAttachmentAnnot], "ovs-conf")
			Expect(err).ToNot(HaveOccurred())

			expectedUpdatedMACs := []string{preAllocatedPodMAC, allocatedMAC}
			Expect(checkMacPoolMapEntries(poolManager.macPoolMap, nil, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

			Expect(newPod.Annotations[networkv1.NetworkAttachmentAnnot]).To(Equal(afterAllocationAnnotation(managedNamespaceName, allocatedMAC)[networkv1.NetworkAttachmentAnnot]))
			expectedMacEntry := macEntry{
				transactionTimestamp: nil,
				instanceName:         podNamespaced(&newPod),
				macInstanceKey:       "ovs-conf",
			}
			Expect(poolManager.macPoolMap[NewMacKey(allocatedMAC)]).To(Equal(expectedMacEntry))

			err = poolManager.ReleaseAllPodMacs(podNamespaced(&newPod))
			Expect(err).ToNot(HaveOccurred())

			expectedUpdatedMACs = []string{preAllocatedPodMAC}
			Expect(checkMacPoolMapEntries(poolManager.macPoolMap, nil, expectedUpdatedMACs, []string{})).To(Succeed(), "Failed to check macs in macMap")

			_, exist := poolManager.macPoolMap[NewMacKey(allocatedMAC)]
			Expect(exist).To(BeFalse())
		})
		It("should allocate requested mac when empty", func() {
			poolManager := createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
			newPod := managedPodWithMacAllocated
			newPod.Name = "newPod"

			err := poolManager.AllocatePodMac(&newPod, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(newPod.Annotations[networkv1.NetworkAttachmentAnnot]).To(Equal(afterAllocationAnnotation(managedNamespaceName, managedNamespaceMAC)[networkv1.NetworkAttachmentAnnot]))
		})
	})

	Describe("Multus Network Annotations API Tests", func() {
		Context("when pool-manager is configured with available addresses", func() {
			poolManager := &PoolManager{}

			BeforeEach(func() {
				poolManager = createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode)
				Expect(poolManager).ToNot(Equal(nil), "should create pool-manager")
			})

			DescribeTable("should allocate mac-address correspond to the one specified in the networks annotation",
				func(networkRequestAnnotation string) {
					pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "testPod", Namespace: "default"}}
					pod.Annotations = map[string]string{networkv1.NetworkAttachmentAnnot: fmt.Sprintf("%s", networkRequestAnnotation)}

					By("Request specific mac-address by adding the address to the networks pod annotation")
					err := poolManager.AllocatePodMac(&pod, true)
					Expect(err).ToNot(HaveOccurred(), "should allocate mac address and ip address correspond to networks annotation")

					By("Convert obtained networks annotation JSON to multus.NetworkSelectionElement array")
					obtainedNetworksAnnotationJson := pod.Annotations[networkv1.NetworkAttachmentAnnot]
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
				Entry("with single ip-address request as string array",
					`[{"name":"ovs-conf","namespace":"default","ips":["10.10.0.1"],"mac":"02:00:00:00:00:00"}]`),
				Entry("with multiple ip-address request as string array",
					`[{"name":"ovs-conf","namespace":"default","ips":["10.10.0.1","10.10.0.2","10.0.0.3"],"mac":"02:00:00:00:00:00"}]`),
				Entry("with multiple networks requsets", `[
						{"name":"ovs-conf","namespace":"default","ips":["10.10.0.1","10.10.0.2","10.0.0.3"],"mac":"02:00:00:00:00:00"},
						{"name":"cnv-bridge","namespace":"openshift-cnv","ips":["192.168.66.100","192.168.66.101"],"mac":"02:F0:F0:F0:F0:F0"}
				]`),
			)
			It("should fail to allocate requested mac-address, with ip-address request as string instead of string array", func() {
				pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "testPod", Namespace: "default"}}
				pod.Annotations = map[string]string{
					networkv1.NetworkAttachmentAnnot: `[{"name":"ovs-conf","namespace":"default","ips":"10.10.0.1","mac":"02:00:00:00:00:00"}]`}

				By("Request specific mac-address by adding the address to the networks pod annotation")
				err := poolManager.AllocatePodMac(&pod, true)
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
		noneOnDryRun := admissionregistrationv1.SideEffectClassNoneOnDryRun

		optOutMutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: optOutMutatingWebhookConfigurationName,
			},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				admissionregistrationv1.MutatingWebhook{
					Name:                    webhookName,
					SideEffects:             &noneOnDryRun,
					AdmissionReviewVersions: []string{"v1", "v1beta1"},
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
		optInMutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: optInMutatingWebhookConfigurationName,
			},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				admissionregistrationv1.MutatingWebhook{
					Name:                    webhookName,
					SideEffects:             &noneOnDryRun,
					AdmissionReviewVersions: []string{"v1", "v1beta1"},
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
			poolManager = createPoolManager(minRangeMACPool, maxRangeMACPool, OptOutMode, optOutMutatingWebhookConfiguration, optInMutatingWebhookConfiguration, namespaceWithIncludingLabel, namespaceWithExcludingLabel, namespaceWithNoLabels, namespaceWithIrrelevantLabels)
		})
		DescribeTable("Should return the expected namespace acceptance outcome according to the opt-mode or return an error",
			func(n *isNamespaceSelectorCompatibleWithOptModeLabelParams) {

				isNamespaceManaged, err := poolManager.isNamespaceSelectorCompatibleWithOptModeLabel(n.namespaceName, n.mutatingWebhookConfigurationName, webhookName, n.optMode)
				if n.ErrorTextExpected != "" {
					Expect(err).Should(MatchError(n.ErrorTextExpected), "isNamespaceSelectorCompatibleWithOptModeLabel should match expected error message")
				} else {
					Expect(err).ToNot(HaveOccurred(), "isNamespaceSelectorCompatibleWithOptModeLabel should not return an error")
				}

				Expect(isNamespaceManaged).To(Equal(n.expectedResult), n.failureDescription)
			},
			Entry("when opt-mode is opt-in and using a namespace with including label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIncludingLabelName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace when in opt-in and including label is set in the namespace",
				}),
			Entry("when opt-mode is opt-in and using a namespace with irrelevant label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIrrelevantLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should not include namespace by default unless including label is set in the namespace",
				}),
			Entry("when opt-mode is opt-in and using a namespace with no labels",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithNoLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should not include namespace by default unless including label is set in the namespace",
				}),
			Entry("when opt-mode is opt-in and using a namespace with excluding label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithExcludingLabelName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should not include namespace by default unless including label is set in the namespace",
				}),
			Entry("when opt-mode is opt-out and using a namespace with excluding label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithExcludingLabelName,
					ErrorTextExpected:                "",
					expectedResult:                   false,
					failureDescription:               "Should exclude namespace when in opt-out and excluding label is set in the namespace",
				}),
			Entry("when opt-mode is opt-out and using a namespace with irrelevant label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIrrelevantLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace by default unless excluding label is set in the namespace",
				}),
			Entry("when opt-mode is opt-out and using a namespace with no labels",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithNoLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace by default unless excluding label is set in the namespace",
				}),
			Entry("when opt-mode is opt-out and using a namespace with including label",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptOutMode,
					mutatingWebhookConfigurationName: optOutMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithNoLabelsName,
					ErrorTextExpected:                "",
					expectedResult:                   true,
					failureDescription:               "Should include namespace by default unless excluding label is set in the namespace",
				}),
			Entry("when opt-mode parameter is not valid",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptMode("not-valid"),
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    namespaceWithIncludingLabelName,
					ErrorTextExpected:                "Failed to check if namespaces are managed by default by opt-mode: opt-mode is not defined: not-valid",
					expectedResult:                   false,
					failureDescription:               "Should reject namespace if an error has occurred during the function operation",
				}),
			Entry("when namespace is not found",
				&isNamespaceSelectorCompatibleWithOptModeLabelParams{
					optMode:                          OptInMode,
					mutatingWebhookConfigurationName: optInMutatingWebhookConfigurationName,
					namespaceName:                    "non-existing-namespace-name",
					ErrorTextExpected:                "Failed to get Namespace: namespaces \"non-existing-namespace-name\" not found",
					expectedResult:                   false,
					failureDescription:               "Should reject namespace if an error has occurred during the function operation",
				}),
			Entry("when mutatingWebhookConfiguration is not found",
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

func appendOptOutModes(fakeObjectsForClient []runtime.Object) []runtime.Object {
	noneOnDryRun := admissionregistrationv1.SideEffectClassNoneOnDryRun
	mutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: mutatingWebhookConfigName,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			admissionregistrationv1.MutatingWebhook{
				Name:                    virtualMachnesWebhookName,
				SideEffects:             &noneOnDryRun,
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
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
							Key:      virtualMachnesWebhookName,
							Operator: "NotIn",
							Values:   []string{"ignore"},
						},
					},
				},
			},
			admissionregistrationv1.MutatingWebhook{
				Name:                    podsWebhookName,
				SideEffects:             &noneOnDryRun,
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
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
							Key:      podsWebhookName,
							Operator: "NotIn",
							Values:   []string{"ignore"},
						},
					},
				},
			},
		},
	}
	managedNamespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managedNamespaceName}}
	notManagedNamespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: notManagedNamespaceName, Labels: map[string]string{podsWebhookName: "ignore", virtualMachnesWebhookName: "ignore"}}}
	By("Setting kubemacpool MutatingWebhookConfigurations to opt-out mode on vms and pods")
	fakeObjectsForClient = append(fakeObjectsForClient, mutatingWebhookConfiguration)
	By("Setting managed and non-managed namespaces")
	fakeObjectsForClient = append(fakeObjectsForClient, managedNamespace, notManagedNamespace)
	return fakeObjectsForClient
}

func appendOptInModes(fakeObjectsForClient []runtime.Object) []runtime.Object {
	noneOnDryRun := admissionregistrationv1.SideEffectClassNoneOnDryRun
	mutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: mutatingWebhookConfigName,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			admissionregistrationv1.MutatingWebhook{
				Name:                    virtualMachnesWebhookName,
				SideEffects:             &noneOnDryRun,
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
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
							Key:      virtualMachnesWebhookName,
							Operator: "In",
							Values:   []string{"allocate"},
						},
					},
				},
			},
			admissionregistrationv1.MutatingWebhook{
				Name:                    podsWebhookName,
				SideEffects:             &noneOnDryRun,
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
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
							Key:      podsWebhookName,
							Operator: "In",
							Values:   []string{"allocate"},
						},
					},
				},
			},
		},
	}
	managedNamespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managedNamespaceName, Labels: map[string]string{podsWebhookName: "allocate", virtualMachnesWebhookName: "allocate"}}}
	notManagedNamespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: notManagedNamespaceName}}
	By("Setting kubemacpool MutatingWebhookConfigurations to opt-in mode on vms and pods")
	fakeObjectsForClient = append(fakeObjectsForClient, mutatingWebhookConfiguration)
	By("Setting managed and non-managed namespaces")
	fakeObjectsForClient = append(fakeObjectsForClient, managedNamespace, notManagedNamespace)
	return fakeObjectsForClient
}

func createPoolManager(startMacAddr, endMacAddr string, optMode OptMode, fakeObjectsForClient ...runtime.Object) *PoolManager {
	waitTimeSeconds := 10
	if optMode == OptOutMode {
		fakeObjectsForClient = appendOptOutModes(fakeObjectsForClient)
	} else {
		fakeObjectsForClient = appendOptInModes(fakeObjectsForClient)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(fakeObjectsForClient...).Build()
	startPoolRangeEnv, err := net.ParseMAC(startMacAddr)
	Expect(err).ToNot(HaveOccurred(), "should successfully parse starting mac address range")
	endPoolRangeEnv, err := net.ParseMAC(endMacAddr)
	Expect(err).ToNot(HaveOccurred(), "should successfully parse ending mac address range")
	poolManager, err := NewPoolManager(fakeClient, fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, waitTimeSeconds)
	Expect(err).ToNot(HaveOccurred(), "should successfully initialize poolManager")
	err = poolManager.Start()
	Expect(err).ToNot(HaveOccurred(), "should successfully start poolManager routines")
	return poolManager
}

func checkMacPoolMapEntries(macPoolMap map[macKey]macEntry, updatedTransactionTimestamp *time.Time, updatedMacs, notUpdatedMacs []string) error {
	if len(macPoolMap) != len(updatedMacs)+len(notUpdatedMacs) {
		return fmt.Errorf("mac pool size %d is not as expected %d, should only contain MACs %v, macPoolMap %+v", len(macPoolMap), len(updatedMacs)+len(notUpdatedMacs), append(updatedMacs, notUpdatedMacs...), macPoolMap)
	}
	for _, macAddress := range updatedMacs {
		macEntry, exist := macPoolMap[NewMacKey(macAddress)]
		if !exist {
			return fmt.Errorf("mac %s should exist in macPoolMap %v", macAddress, macPoolMap)
		}
		if macEntry.transactionTimestamp != updatedTransactionTimestamp {
			return fmt.Errorf("mac %s has transactionTimestamp %s, should have an updated transactionTimestamp %s", macAddress, macEntry.transactionTimestamp, updatedTransactionTimestamp)
		}
	}
	for _, macAddress := range notUpdatedMacs {
		macEntry, exist := macPoolMap[NewMacKey(macAddress)]
		if !exist {
			return fmt.Errorf("mac %s should exist in macPoolMap %v", macAddress, macPoolMap)
		}
		if macEntry.transactionTimestamp == updatedTransactionTimestamp {
			return fmt.Errorf("mac %s has transactionTimestamp %s, should not have an updated transactionTimestamp %s", macAddress, macEntry.transactionTimestamp, updatedTransactionTimestamp)
		}
	}
	return nil
}

func interfaceMACsToList(interfaces []kubevirt.Interface) []string {
	var macList []string
	for _, iface := range interfaces {
		if len(iface.MacAddress) != 0 {
			macList = append(macList, iface.MacAddress)
		}
	}
	return macList
}

func getAllocatedMacByInterface(networksAnnot string, iface string) (string, error) {
	if networksAnnot == "" {
		return "", fmt.Errorf("network annotation empty")
	}

	type Network struct {
		Name string `json:"name"`
		MAC  string `json:"mac"`
	}

	var networks []Network
	if err := json.Unmarshal([]byte(networksAnnot), &networks); err != nil {
		return "", fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if len(networks) == 0 {
		return "", fmt.Errorf("networks array is empty")
	}

	for _, network := range networks {
		if network.Name == iface {
			return network.MAC, nil
		}
	}
	return "", fmt.Errorf("interface %s not found", iface)
}
