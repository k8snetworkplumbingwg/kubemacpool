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
	"sigs.k8s.io/controller-runtime/pkg/client"

	pool_manager "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
	kubevirtv1 "kubevirt.io/kubevirt/pkg/api/v1"
)

const timeout = 60 * time.Second
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
			err := testClient.VirtClient.List(context.TODO(), &client.ListOptions{}, vmList)
			Expect(err).ToNot(HaveOccurred())

			for _, vmObject := range vmList.Items {
				err = testClient.VirtClient.Delete(context.TODO(), &vmObject)
				Expect(err).ToNot(HaveOccurred())
			}

			Eventually(func() int {
				vmList := &kubevirtv1.VirtualMachineList{}
				err := testClient.VirtClient.List(context.TODO(), &client.ListOptions{}, vmList)
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

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("When the client tries to assign the same MAC address for two different vm. Within Range and out of range", func() {
			//2166
			Context("When the MAC address is within range", func() {
				It("should reject a vm creation with an already allocated MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})

					Eventually(func() error {
						return testClient.VirtClient.Create(context.TODO(), vm)
					}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
					_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred())

					vmOverlap := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("brOverlap", "")}, []kubevirtv1.Network{newNetwork("brOverlap")})
					// Allocated the same MAC address that was registered to the first vm
					vmOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress

					Eventually(func() error {
						err = testClient.VirtClient.Create(context.TODO(), vmOverlap)
						if err != nil {
							return err
						}
						return nil

					}, timeout, pollingInterval).Should(HaveOccurred())
					_, err = net.ParseMAC(vmOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred())
				})
			})
			//2167
			Context("When the MAC address is out of range", func() {
				It("should reject a vm creation with an already allocated MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "03:ff:ff:ff:ff:ff")},
						[]kubevirtv1.Network{newNetwork("br")})

					Eventually(func() error {
						return testClient.VirtClient.Create(context.TODO(), vm)

					}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
					_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred())

					// Allocated the same mac address that was registered to the first vm
					vmOverlap := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("brOverlap", "03:ff:ff:ff:ff:ff")},
						[]kubevirtv1.Network{newNetwork("brOverlap")})

					Eventually(func() error {
						err = testClient.VirtClient.Create(context.TODO(), vmOverlap)
						if err != nil && strings.Contains(err.Error(), "failed to allocate requested mac address") {
							return err
						}
						return nil
					}, timeout, pollingInterval).Should(HaveOccurred())
				})
			})
		})
		//2199
		Context("when the client tries to assign the same MAC address for two different interfaces in a single VM.", func() {
			Context("When the MAC address is within range", func() {
				It("should reject a VM creation with two interfaces that share the same MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "02:00:00:00:ff:ff"),
						newInterface("br2", "02:00:00:00:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

					Eventually(func() error {
						err = testClient.VirtClient.Create(context.TODO(), vm)
						if err != nil && strings.Contains(err.Error(), "failed to allocate requested mac address") {
							return err
						}
						return nil

					}, timeout, pollingInterval).Should(HaveOccurred())
				})
			})
			//2200
			Context("When the MAC address is out of range", func() {
				It("should reject a VM creation with two interfaces that share the same MAC address", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "03:ff:ff:ff:ff:ff"),
						newInterface("br2", "03:ff:ff:ff:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

					Eventually(func() error {
						err = testClient.VirtClient.Create(context.TODO(), vm)
						if err != nil && strings.Contains(err.Error(), "failed to allocate requested mac address") {
							return err
						}
						return nil

					}, timeout, pollingInterval).Should(HaveOccurred())
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

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm1)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")

				_, err = net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				vm2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br2")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm2)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
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
					return testClient.VirtClient.Create(context.TODO(), newVM1)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				_, err = net.ParseMAC(newVM1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				newVM2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br2", vm2MacAddress)},
					[]kubevirtv1.Network{newNetwork("br2")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), newVM2)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				_, err = net.ParseMAC(newVM2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		//2162
		Context("When trying to create a VM after all MAC addresses in range have been occupied", func() {
			It("should return an error because no MAC address is available", func() {
				err := setRange("02:00:00:00:00:00", "02:00:00:00:00:01")
				Expect(err).ToNot(HaveOccurred())

				vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""),
					newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				starvingVM := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
					[]kubevirtv1.Network{newNetwork("br")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), starvingVM)

				}, timeout, pollingInterval).Should((HaveOccurred()), "failed to apply the new vm object")
			})
		})
		//2165
		Context("when trying to create a VM after a MAC address has just been released duo to a VM deletion", func() {
			It("should re-use the released MAC address for the creation of the new VM and not return an error", func() {
				err := setRange("02:00:00:00:00:00", "02:00:00:00:00:02")
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""),
					newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm1)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				mac1, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				mac2, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				vm2 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br3", "")},
					[]kubevirtv1.Network{newNetwork("br3")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm2)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object error")
				_, err = net.ParseMAC(vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				deleteVMI(vm1)

				newVM1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", ""),
					newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), newVM1)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				newMac1, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
				newMac2, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				Expect(newMac1.String()).To(Equal(mac1.String()))
				Expect(newMac2.String()).To(Equal(mac2.String()))
			})
		})
		//2179
		Context("When restarting kubeMacPool and trying to create a VM with the same manually configured MAC as an older VM", func() {
			It("should return an error because the MAC address is taken by the older VM", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br1", "02:00:ff:ff:ff:ff")}, []kubevirtv1.Network{newNetwork("br1")})

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm1)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")
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

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm1)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object error")

				updateObject.ObjectMeta = *vm1.ObjectMeta.DeepCopy()

				for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
					_, err = net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress)
					Expect(err).ToNot(HaveOccurred())
				}

				Eventually(func() error {

					err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: updateObject.Namespace, Name: updateObject.Name}, updateObject)
					if err != nil {
						return err
					}
					return testClient.VirtClient.Update(context.TODO(), updateObject)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to update client")

				for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
					Expect(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress).To(Equal(updateObject.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress))
				}
			})
		})
		//TODO:  remove the the pending annotation -"P"- from "PContext" when issue #44 is fixed :
		//https://github.com/K8sNetworkPlumbingWG/kubemacpool/issues/44
		PContext("When we re-apply a failed VM yaml", func() {
			It("should allow to assign to the VM the same MAC addresses, with name as requested before and do not return an error", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false,
					[]kubevirtv1.Interface{newInterface("br1", "02:00:ff:ff:ff:ff")},
					[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				baseVM := vm1.DeepCopy()

				Eventually(func() bool {
					err := testClient.VirtClient.Create(context.TODO(), vm1)
					if err != nil && strings.Contains(err.Error(), "every network must be mapped to an interface") {
						return true
					}
					return false

				}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new vm object")

				baseVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(baseVM.Spec.Template.Spec.Domain.Devices.Interfaces, newInterface("br2", ""))

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), baseVM)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object error")
			})
			It("should allow to assign to the VM the same MAC addresses, different name as requested before and do not return an error", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm1 := CreateVmObject(TestNamespace, false,
					[]kubevirtv1.Interface{newInterface("br1", "02:00:ff:ff:ff:ff")},
					[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

				baseVM := vm1.DeepCopy()
				baseVM.Name = "new-vm"

				Eventually(func() bool {
					err := testClient.VirtClient.Create(context.TODO(), vm1)
					if err != nil && strings.Contains(err.Error(), "every network must be mapped to an interface") {
						return true
					}
					return false

				}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new vm object")

				baseVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(baseVM.Spec.Template.Spec.Domain.Devices.Interfaces, newInterface("br2", ""))

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), baseVM)

				}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object error")
			})
		})

		Context("testing finalizers", func() {
			Context("When the VM is not being deleted", func() {
				It("should have a finalizer and deletion timestamp should be zero ", func() {
					err := setRange(rangeStart, rangeEnd)
					Expect(err).ToNot(HaveOccurred())

					vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
						[]kubevirtv1.Network{newNetwork("br")})
					Eventually(func() error {
						return testClient.VirtClient.Create(context.TODO(), vm)

					}, timeout, pollingInterval).Should(Not(HaveOccurred()), "failed to apply the new vm object")

					Eventually(func() bool {
						err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
						if err != nil {
							return false
						}

						if vm.ObjectMeta.DeletionTimestamp.IsZero() {
							if len(vm.ObjectMeta.Finalizers) == 1 {
								if strings.Compare(vm.ObjectMeta.Finalizers[0], pool_manager.RuntimeObjectFinalizerName) == 0 {
									return true
								}
							}
						}

						return false
					}, timeout, pollingInterval).Should(BeTrue())
				})
			})
		})
		Context("When the leader is changed", func() {
			It("should be able to create a new virtual machine", func() {
				err := setRange(rangeStart, rangeEnd)
				Expect(err).ToNot(HaveOccurred())

				vm := CreateVmObject(TestNamespace, false, []kubevirtv1.Interface{newInterface("br", "")},
					[]kubevirtv1.Network{newNetwork("br")})

				anotherVm := vm.DeepCopy()
				anotherVm.Name = "another-vm"

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), vm)

				}, 40*time.Second, 5*time.Second).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())

				By("deleting leader manager")
				DeleteLeaderManager()

				Eventually(func() error {
					return testClient.VirtClient.Create(context.TODO(), anotherVm)

				}, 40*time.Second, 5*time.Second).Should(Not(HaveOccurred()), "failed to apply the new vm object")
				_, err = net.ParseMAC(anotherVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
				Expect(err).ToNot(HaveOccurred())
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
