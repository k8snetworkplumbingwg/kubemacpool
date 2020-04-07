package tests

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
)

const timeout = 2 * time.Minute
const pollingInterval = 5 * time.Second

var _ = Describe("Virtual Machines", func() {
	BeforeAll(func() {
		result := testClient.KubeClient.ExtensionsV1beta1().RESTClient().
			Post().
			RequestURI(fmt.Sprintf(nadPostUrl, TestNamespace, "linux-bridge")).
			Body([]byte(fmt.Sprintf(linuxBridgeConfCRD, "linux-bridge", TestNamespace))).
			Do()
		Expect(result.Error()).NotTo(HaveOccurred())
	})

	Context("Check the client", func() {
		AfterEach(func() {
			vmList := &kubevirtv1.VirtualMachineList{}
			err := testClient.VirtClient.List(context.TODO(), vmList, &client.ListOptions{})
			Expect(err).ToNot(HaveOccurred())

			for _, vmObject := range vmList.Items {
				err = testClient.VirtClient.Delete(context.TODO(), &vmObject)
				Expect(err).ToNot(HaveOccurred())
			}

			Eventually(func() int {
				vmList := &kubevirtv1.VirtualMachineList{}
				err := testClient.VirtClient.List(context.TODO(), vmList, &client.ListOptions{})
				Expect(err).ToNot(HaveOccurred())
				return len(vmList.Items)

			}, timeout, pollingInterval).Should(Equal(0), "failed to remove all vm objects")
		})

		Context("When the client wants to create a vm", func() {
			It("should create a vm object and automatically assign a static MAC address", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
					[]kubevirtv1.Network{newNetwork("br")})

				err = testClient.VirtClient.Create(context.TODO(), vm)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("When the client tries to assign the same MAC address for two different vm. Within Range and out of range", func() {
			//2166 TODO until we split the range to 2 separate non overlapping - then this test is expected to fail.
			Context("When the MAC address is within range", func() {
				It("should reject a vm creation with an already allocated MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})
					err = testClient.VirtClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred())
					_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred())

					vmOverlap := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("brOverlap", "")}, []kubevirtv1.Network{newNetwork("brOverlap")})
					// Allocated the same MAC address that was registered to the first vm
					vmOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
					err = testClient.VirtClient.Create(context.TODO(), vmOverlap)
					Expect(err).To(HaveOccurred())
					_, err = net.ParseMAC(vmOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred())
				})
			})
			//2167 TODO until we split the range to 2 separate non overlapping - then this test is expected to fail.
			Context("When the MAC address is out of range", func() {
				It("should reject a vm creation with an already allocated MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "03:ff:ff:ff:ff:ff")},
						[]kubevirtv1.Network{newNetwork("br")})
					err = testClient.VirtClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred())
					_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred())

					// Allocated the same mac address that was registered to the first vm
					vmOverlap := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("brOverlap", "03:ff:ff:ff:ff:ff")},
						[]kubevirtv1.Network{newNetwork("brOverlap")})
					err = testClient.VirtClient.Create(context.TODO(), vmOverlap)
					Expect(err).To(HaveOccurred())
					Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))

				})
			})
		})
		//2199 TODO until we split the range to 2 separate non overlapping - then this test is expected to fail.
		Context("when the client tries to assign the same MAC address for two different interfaces in a single VM.", func() {
			Context("When the MAC address is within range", func() {
				It("should reject a VM creation with two interfaces that share the same MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "02:00:00:00:ff:ff"),
						newInterface("br2", "02:00:00:00:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
					err = testClient.VirtClient.Create(context.TODO(), vm)
					Expect(err).To(HaveOccurred())
					Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
				})
			})
			//2200 TODO until we split the range to 2 separate non overlapping - then this test is expected to fail.
			Context("When the MAC address is out of range", func() {
				It("should reject a VM creation with two interfaces that share the same MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "03:ff:ff:ff:ff:ff"),
						newInterface("br2", "03:ff:ff:ff:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
					err = testClient.VirtClient.Create(context.TODO(), vm)
					Expect(err).To(HaveOccurred())
					Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
				})
			})
		})
		//2164
		Context("When two VM are deleted and we try to assign their MAC addresses for two newly created VM", func() {
			It("should not return an error because the MAC addresses of the old VMs should have been released", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				//creating two VMs
				vm1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "")}, []kubevirtv1.Network{newNetwork("br1")})
				err = testClient.VirtClient.Create(context.TODO(), vm1)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				vm2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br2")})
				err = testClient.VirtClient.Create(context.TODO(), vm2)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				vm1MacAddress := vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				vm2MacAddress := vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress

				//deleting both and try to assign their MAC address to the new VM
				deleteVMI(vm1)
				deleteVMI(vm2)

				newVM1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", vm1MacAddress)},
					[]kubevirtv1.Network{newNetwork("br1")})

				Eventually(func() error {
					err = testClient.VirtClient.Create(context.TODO(), newVM1)
					if err != nil {
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					}
					return err

				}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
				_, err = net.ParseMAC(newVM1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				newVM2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br2", vm2MacAddress)},
					[]kubevirtv1.Network{newNetwork("br2")})

				Eventually(func() error {
					err = testClient.VirtClient.Create(context.TODO(), newVM2)
					if err != nil {
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					}
					return err

				}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
				_, err = net.ParseMAC(newVM2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		//2162 test postponed due to issue: https://github.com/k8snetworkplumbingwg/kubemacpool/issues/104
		PContext("When trying to create a VM after all MAC addresses in range have been occupied", func() {
			It("should return an error because no MAC address is available", func() {
				err := setRange("02:00:00:00:00:00", "02:00:00:00:00:01")
				Expect(err).ToNot(HaveOccurred())

				vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""),
					newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				err = testClient.VirtClient.Create(context.TODO(), vm)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				starvingVM := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
					[]kubevirtv1.Network{newNetwork("br")})

				err = testClient.VirtClient.Create(context.TODO(), starvingVM)
				Expect(err).To(HaveOccurred())
			})
		})
		//2165 test postponed due to issue: https://github.com/k8snetworkplumbingwg/kubemacpool/issues/105
		PContext("when trying to create a VM after a MAC address has just been released duo to a VM deletion", func() {
			It("should re-use the released MAC address for the creation of the new VM and not return an error", func() {
				err := setRange("02:00:00:00:00:00", "02:00:00:00:00:02")
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""),
					newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				err = testClient.VirtClient.Create(context.TODO(), vm1)
				Expect(err).ToNot(HaveOccurred())
				mac1, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
				mac2, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				vm2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br3", "")},
					[]kubevirtv1.Network{newNetwork("br3")})

				err = testClient.VirtClient.Create(context.TODO(), vm2)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				deleteVMI(vm1)

				newVM1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""),
					newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), newVM1)

				}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
				newMac1, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
				newMac2, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				Expect(newMac1.String()).To(Equal(mac1.String()))
				Expect(newMac2.String()).To(Equal(mac2.String()))
			})
		})
		//2179 TODO until we split the range to 2 separate non overlapping - then this test is expected to fail.
		Context("When restarting kubeMacPool and trying to create a VM with the same manually configured MAC as an older VM", func() {
			It("should return an error because the MAC address is taken by the older VM", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "02:00:ff:ff:ff:ff")}, []kubevirtv1.Network{newNetwork("br1")})

				err = testClient.VirtClient.Create(context.TODO(), vm1)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				//restart kubeMacPool
				err = setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br2", "02:00:ff:ff:ff:ff")},
					[]kubevirtv1.Network{newNetwork("br2")})

				Eventually(func() error {
					err = testClient.VirtClient.Create(context.TODO(), vm2)
					if err != nil && strings.Contains(err.Error(), "failed to allocate requested mac address") {
						return err
					}
					return nil

				}, timeout, pollingInterval).Should(HaveOccurred())
			})
		})
		//2243
		Context("When we re-apply a VM yaml", func() {
			It("should assign to the VM the same MAC addresses as before the re-apply, and not return an error", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				intrefaces := make([]kubevirtv1.Interface, 5)
				networks := make([]kubevirtv1.Network, 5)
				for i := 0; i < 5; i++ {
					intrefaces[i] = newInterface("br"+strconv.Itoa(i), "")
					networks[i] = newNetwork("br" + strconv.Itoa(i))
				}

				vm1 := CreateVmObject(TestNamespace, false, intrefaces, networks)
				updateObject := vm1.DeepCopy()

				err = testClient.VirtClient.Create(context.TODO(), vm1)
				Expect(err).ToNot(HaveOccurred())

				updateObject.ObjectMeta = *vm1.ObjectMeta.DeepCopy()

				for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
					_, err = net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress)
					Expect(err).ToNot(HaveOccurred())
				}

				err = retry.RetryOnConflict(retry.DefaultRetry, func() error {

					err = testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: updateObject.Namespace, Name: updateObject.Name}, updateObject)
					Expect(err).ToNot(HaveOccurred())

					err = testClient.VirtClient.Update(context.TODO(), updateObject)
					return err
				})
				Expect(err).ToNot(HaveOccurred())

				for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
					Expect(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress).To(Equal(updateObject.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress))
				}
			})
		})
		//2633 test postponed due to issue: https://github.com/k8snetworkplumbingwg/kubemacpool/issues/101
		PContext("When we re-apply a failed VM yaml", func() {
			It("should allow to assign to the VM the same MAC addresses, with name as requested before and do not return an error", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false,
					[]kubevirtv1.Interface{newInterface("br1", "02:00:ff:ff:ff:ff")},
					[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				baseVM := vm1.DeepCopy()

				err = testClient.VirtClient.Create(context.TODO(), vm1)
				Expect(err).To(HaveOccurred())
				Expect(strings.Contains(err.Error(), "every network must be mapped to an interface")).To(Equal(true))

				baseVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(baseVM.Spec.Template.Spec.Domain.Devices.Interfaces, newInterface("br2", ""))

				Eventually(func() error {
					err = testClient.VirtClient.Create(context.TODO(), baseVM)
					if err != nil {
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					}
					return err

				}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
			})
			It("should allow to assign to the VM the same MAC addresses, different name as requested before and do not return an error", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false,
					[]kubevirtv1.Interface{newInterface("br1", "02:00:ff:ff:ff:ff")},
					[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				baseVM := vm1.DeepCopy()
				baseVM.Name = "new-vm"

				err = testClient.VirtClient.Create(context.TODO(), vm1)
				Expect(err).To(HaveOccurred())
				Expect(strings.Contains(err.Error(), "every network must be mapped to an interface")).To(Equal(true))

				baseVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(baseVM.Spec.Template.Spec.Domain.Devices.Interfaces, newInterface("br2", ""))

				Eventually(func() error {
					err = testClient.VirtClient.Create(context.TODO(), baseVM)
					if err != nil {
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					}
					return err

				}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
			})
		})

		// TODO until we split the range to 2 separate non overlapping - then this test is expected to fail.
		Context("testing finalizers", func() {
			Context("When the VM is not being deleted", func() {
				It("should have a finalizer and deletion timestamp should be zero", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
						[]kubevirtv1.Network{newNetwork("br")})
					err = testClient.VirtClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred())

					Eventually(func() bool {
						err = testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
						Expect(err).ToNot(HaveOccurred())
						if vm.ObjectMeta.DeletionTimestamp.IsZero() {
							if len(vm.ObjectMeta.Finalizers) == 1 {
								return strings.Contains(vm.ObjectMeta.Finalizers[0], pool_manager.RuntimeObjectFinalizerName+"kubemacpool-mac-controller-manager-")
							}
						}
						return false
					}, timeout, pollingInterval).Should(BeTrue())
				})
			})
		})

		Context("When 1 pod mgr is down", func() {
			It("should be able to create a new virtual machine", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
					[]kubevirtv1.Network{newNetwork("br")})

				anotherVm := vm.DeepCopy()
				anotherVm.Name = "another-vm"

				err = testClient.VirtClient.Create(context.TODO(), vm)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				By("deleting 1 kubemacpool manager pod")
				DeleteOneManager()

				err = testClient.VirtClient.Create(context.TODO(), anotherVm)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(anotherVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		//2995
		Context("When a VM's NIC is removed and a new VM is created with the same MAC", func() {
			It("should successfully release the MAC and the new VM should be created with no errors", func() {
				err := setRange("02:00:00:00:00:00", "02:00:00:00:00:01")
				Expect(err).ToNot(HaveOccurred())

				vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""), newInterface("br2", "")},
					[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
				err = testClient.VirtClient.Create(context.TODO(), vm)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				By("checking that a new VM cannot be created when the range is full")
				newVM := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "")},
					[]kubevirtv1.Network{newNetwork("br1")})
				err = testClient.VirtClient.Create(context.TODO(), newVM)
				Expect(err).To(HaveOccurred())
				Expect(strings.Contains(err.Error(), "Failed to create virtual machine allocation error: the range is full")).To(Equal(true))

				By("checking that the VM's NIC can be removed")

				err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
					err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
					Expect(err).ToNot(HaveOccurred())

					vm.Spec.Template.Spec.Domain.Devices.Interfaces = []kubevirtv1.Interface{newInterface("br2", "")}
					vm.Spec.Template.Spec.Networks = []kubevirtv1.Network{newNetwork("br2")}

					err = testClient.VirtClient.Update(context.TODO(), vm)
					return err
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 1).To(Equal(true))

				By("checking that a new VM can be created after the VM's NIC had been removed ")

				Eventually(func() error {
					err = testClient.VirtClient.Create(context.TODO(), newVM)
					if err != nil {
						Expect(strings.Contains(err.Error(), "Failed to create virtual machine allocation error: the range is full")).To(Equal(true))
					}
					return err

				}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")

				Expect(len(newVM.Spec.Template.Spec.Domain.Devices.Interfaces) == 1).To(Equal(true))
			})
		})
	})
})

func newInterface(name, macAddress string) kubevirtv1.Interface {
	return kubevirtv1.Interface{
		Name: name,
		InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
			Bridge: &kubevirtv1.InterfaceBridge{},
		},
		MacAddress: macAddress,
	}
}

func newNetwork(name string) kubevirtv1.Network {
	return kubevirtv1.Network{
		Name: name,
		NetworkSource: kubevirtv1.NetworkSource{
			Multus: &kubevirtv1.MultusNetwork{
				NetworkName: name,
			},
		},
	}
}

func deleteVMI(vm *kubevirtv1.VirtualMachine) {
	err := testClient.VirtClient.Delete(context.TODO(), vm)
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() bool {
		err = testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
		if err != nil && errors.IsNotFound(err) {
			return true
		}
		return false
	}, timeout, pollingInterval).Should(BeTrue(), "failed to delete VM")
}
