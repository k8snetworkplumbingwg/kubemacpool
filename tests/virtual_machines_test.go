package tests

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	"github.com/k8snetworkplumbingwg/kubemacpool/tests/kubectl"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const timeout = 2 * time.Minute
const pollingInterval = 5 * time.Second
const testMacAddress = "02:00:ff:ff:ff:ff"

// TODO: the rfe_id was taken from kubernetes-nmstate we have to discover the rigth parameters here
var _ = Describe("[rfe_id:3503][crit:medium][vendor:cnv-qe@redhat.com][level:component]Virtual Machines", Ordered, func() {
	restoreFailedWebhookChangesTimeout := time.Duration(0)
	BeforeAll(func() {
		result := testClient.VirtClient.ExtensionsV1beta1().RESTClient().
			Post().
			RequestURI(fmt.Sprintf(nadPostUrl, TestNamespace, "linux-bridge")).
			Body([]byte(fmt.Sprintf(linuxBridgeConfCRD, "linux-bridge", TestNamespace))).
			Do(context.TODO())
		Expect(result.Error()).NotTo(HaveOccurred(), "KubeCient should successfully respond to post request")

		vmFailCleanupWaitTime := getVmFailCleanupWaitTime()
		// since this test checks vmWaitingCleanupLook routine, we need to adjust the total timeout with the wait-time argument.
		// we also add some extra timeout apart form wait-time to be sure that we catch the vm mac release.
		restoreFailedWebhookChangesTimeout = vmFailCleanupWaitTime + time.Minute
	})

	BeforeEach(func() {
		By("Verify that there are no VMs left from previous tests")
		currentVMList, err := testClient.VirtClient.VirtualMachine(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred(), "Should successfully list add VMs")
		Expect(len(currentVMList.Items)).To(BeZero(), "There should be no VM's in the cluster before a test")

		// remove all the labels from the test namespaces
		for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
			err = cleanNamespaceLabels(namespace)
			Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")
		}
	})

	Context("Check the client", func() {
		BeforeEach(func() {
			err := initKubemacpoolParams()
			Expect(err).ToNot(HaveOccurred())

		})
		AfterEach(func() {
			vmList, err := testClient.VirtClient.VirtualMachine(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())

			for _, vmObject := range vmList.Items {
				err := testClient.VirtClient.VirtualMachine(vmObject.Namespace).Delete(context.TODO(), vmObject.Name, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			}

			Eventually(func() int {
				vmList, listErr := testClient.VirtClient.VirtualMachine(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
				Expect(listErr).ToNot(HaveOccurred())
				return len(vmList.Items)

			}, timeout, pollingInterval).Should(Equal(0), "failed to remove all vm objects")
		})
		AfterEach(func() {
			Expect(checkKubemacpoolCrash()).To(Succeed(), "Kubemacpool should not crash during test")
		})
		Context("When Running with default opt-mode configuration", func() {
			BeforeEach(func() {
				By("Getting the current VM Opt-mode")
				vmWebhookOptMode, err := getOptMode(vmNamespaceOptInLabel)
				Expect(err).ToNot(HaveOccurred(), "Should succeed getting the mutating webhook opt-mode")

				if vmWebhookOptMode == optInMode {
					By("opting in the namespace")
					err := addLabelsToNamespace(TestNamespace, map[string]string{vmNamespaceOptInLabel: "allocate"})
					Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")
				}
			})

			Context("and applying a dryRun vm create request", func() {
				var macAddress string
				BeforeEach(func() {
					By("Creating a vm with dry run mode")
					var err error
					vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})
					createOptions := &client.CreateOptions{}
					client.DryRunAll.ApplyToCreate(createOptions)

					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")
					macAddress = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				})

				//TODO Add dry-run functionality in kubevirt/client-go's VirtualMachine().Create(context.TODO(), )
				PIt("should not allocate the Mac assigned in the dryRun request into the pool", func() {
					By("creating a vm with the same mac to make sure the dryRun assigned mac is not occupied on the macpool")
					var err error
					vmDryRunOverlap := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("brDryRunOverlap", macAddress)}, []kubevirtv1.Network{newNetwork("brDryRunOverlap")})
					vmDryRunOverlap, err = testClient.VirtClient.VirtualMachine(vmDryRunOverlap.Namespace).Create(context.TODO(), vmDryRunOverlap, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					actualMac, err := net.ParseMAC(vmDryRunOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing vmDryRunOverlap mac")

					Expect(actualMac.String()).To(Equal(macAddress), "Should successfully parse mac address")
				})
			})

			Context("and a simple vm is applied", func() {
				It("should not remove the transactions timestamp annotation", func() {
					vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})
					vm, err := testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					Consistently(func() (string, error) {
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
						if err != nil {
							return "", err
						}
						return vm.Annotations[pool_manager.TransactionTimestampAnnotation], nil
					}).WithPolling(time.Second).WithTimeout(5*time.Second).ShouldNot(BeEmpty(), "vm %s should have a transaction timestamp annotation", vm.Name)
				})
			})

			Context("and the client tries to assign the same MAC address for two different vm. Within Range and out of range", func() {
				Context("When the MAC address is within range", func() {
					DescribeTable("[test_id:2166]should reject a vm creation with an already allocated MAC address", func(separator string) {
						var err error
						vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred())
						Expect(net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

						vmOverlap := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("brOverlap", "")}, []kubevirtv1.Network{newNetwork("brOverlap")})
						// Allocated the same MAC address that was registered to the first vm
						vmOverlapMacAddress := strings.Replace(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress, ":", separator, 5)
						vmOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress = vmOverlapMacAddress
						vmOverlap, err = testClient.VirtClient.VirtualMachine(vmOverlap.Namespace).Create(context.TODO(), vmOverlap, metav1.CreateOptions{})
						Expect(err).To(HaveOccurred())
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					},
						Entry("with the same mac format", ":"),
						Entry("with different mac format", "-"),
					)
				})
				Context("and the MAC address is out of range", func() {
					It("[test_id:2167]should reject a vm creation with an already allocated MAC address", func() {
						var err error
						vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "03:ff:ff:ff:ff:ff")},
							[]kubevirtv1.Network{newNetwork("br")})
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred())
						Expect(net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

						// Allocated the same mac address that was registered to the first vm
						vmOverlap := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("brOverlap", "03:ff:ff:ff:ff:ff")},
							[]kubevirtv1.Network{newNetwork("brOverlap")})
						vmOverlap, err = testClient.VirtClient.VirtualMachine(vmOverlap.Namespace).Create(context.TODO(), vmOverlap, metav1.CreateOptions{})
						Expect(err).To(HaveOccurred())
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))

					})
				})
			})
			Context("and the client tries to assign the same MAC address for two different interfaces in a single VM.", func() {
				Context("and when the MAC address is within range", func() {
					It("[test_id:2199]should reject a VM creation with two interfaces that share the same MAC address", func() {
						var err error
						vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", "02:00:00:00:ff:ff"),
							newInterface("br2", "02:00:00:00:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
						Expect(err).To(HaveOccurred())
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					})
				})
				Context("and when the MAC address is out of range", func() {
					It("[test_id:2200]should reject a VM creation with two interfaces that share the same MAC address", func() {
						var err error
						vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", "03:ff:ff:ff:ff:ff"),
							newInterface("br2", "03:ff:ff:ff:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
						Expect(err).To(HaveOccurred())
						Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
					})
				})
			})
			Context("and two VM are deleted and we try to assign their MAC addresses for two newly created VM", func() {
				It("[test_id:2164]should not return an error because the MAC addresses of the old VMs should have been released", func() {
					var err error
					//creating two VMs
					vm1 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", "")}, []kubevirtv1.Network{newNetwork("br1")})
					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

					vm2 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br2")})
					vm2, err = testClient.VirtClient.VirtualMachine(vm2.Namespace).Create(context.TODO(), vm2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

					vm1MacAddress := vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
					vm2MacAddress := vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress

					//deleting both and try to assign their MAC address to the new VM
					deleteVMI(vm1)
					deleteVMI(vm2)

					newVM1 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", vm1MacAddress)},
						[]kubevirtv1.Network{newNetwork("br1")})

					Eventually(func() error {
						newVM1, err = testClient.VirtClient.VirtualMachine(newVM1.Namespace).Create(context.TODO(), newVM1, metav1.CreateOptions{})
						if err != nil {
							Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
						}
						return err

					}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
					Expect(net.ParseMAC(newVM1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

					newVM2 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br2", vm2MacAddress)},
						[]kubevirtv1.Network{newNetwork("br2")})

					Eventually(func() error {
						newVM2, err = testClient.VirtClient.VirtualMachine(newVM2.Namespace).Create(context.TODO(), newVM2, metav1.CreateOptions{})
						if err != nil {
							Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
						}
						return err

					}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
					Expect(net.ParseMAC(newVM2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")
				})
			})
			Context("and trying to create a VM after a MAC address has just been released due to a VM deletion", func() {
				It("[test_id:2165]should re-use the released MAC address for the creation of the new VM and not return an error", func() {
					var err error
					vm1 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", ""),
						newInterface("br2", "")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating vm1")
					mac1, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing vm1's first mac")
					mac2, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing vm1's second mac")

					vm2 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br3", "")},
						[]kubevirtv1.Network{newNetwork("br3")})

					vm2, err = testClient.VirtClient.VirtualMachine(vm2.Namespace).Create(context.TODO(), vm2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating vm2")
					Expect(net.ParseMAC(vm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse vm2's mac address")

					newVM1 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress),
						newInterface("br2", vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

					deleteVMI(vm1)

					Eventually(func() error {
						_, err = testClient.VirtClient.VirtualMachine(newVM1.Namespace).Create(context.TODO(), newVM1, metav1.CreateOptions{})
						return err
					}, timeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
					newMac1, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing newVM1's first mac")
					newMac2, err := net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing newVM1's second mac")

					Expect(newMac1.String()).To(Equal(mac1.String()), "Should be equal to the first mac that was released by vm1")
					Expect(newMac2.String()).To(Equal(mac2.String()), "Should be equal to the second mac that was released by vm1")
				})
			})

			Context("and when restarting kubeMacPool and trying to create a VM with the same manually configured MAC as an older VM", func() {
				It("[test_id:2179]should return an error because the MAC address is taken by the older VM", func() {
					var err error
					vm1 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", testMacAddress)}, []kubevirtv1.Network{newNetwork("br1")})

					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

					//restart kubeMacPool
					err = initKubemacpoolParams()
					Expect(err).ToNot(HaveOccurred())

					vm2 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br2", testMacAddress)},
						[]kubevirtv1.Network{newNetwork("br2")})

					Eventually(func() error {
						_, err = testClient.VirtClient.VirtualMachine(vm2.Namespace).Create(context.TODO(), vm2, metav1.CreateOptions{})
						if err != nil && strings.Contains(err.Error(), "failed to allocate requested mac address") {
							return err
						}
						return nil

					}, timeout, pollingInterval).Should(HaveOccurred())
				})
			})
			Context("and we re-apply a VM yaml", func() {
				It("[test_id:2243]should assign to the VM the same MAC addresses as before the re-apply, and not return an error", func() {
					intrefaces := make([]kubevirtv1.Interface, 5)
					networks := make([]kubevirtv1.Network, 5)
					for i := 0; i < 5; i++ {
						intrefaces[i] = newInterface("br"+strconv.Itoa(i), "")
						networks[i] = newNetwork("br" + strconv.Itoa(i))
					}

					vm1 := CreateVmObject(TestNamespace, intrefaces, networks)
					updateObject := vm1.DeepCopy()

					var err error
					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					updateObject.ObjectMeta = *vm1.ObjectMeta.DeepCopy()

					for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
						Expect(net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")
					}

					err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
						updateObject, err = testClient.VirtClient.VirtualMachine(updateObject.Namespace).Get(context.TODO(), updateObject.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())

						_, err = testClient.VirtClient.VirtualMachine(updateObject.Namespace).Update(context.TODO(), updateObject, metav1.UpdateOptions{})
						return err
					})
					Expect(err).ToNot(HaveOccurred())

					for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
						Expect(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress).To(Equal(updateObject.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress))
					}
				})
			})
			Context("and we re-apply a failed VM yaml", func() {
				It("[test_id:2633]should allow to assign to the VM the same MAC addresses, with name as requested before and do not return an error", func() {
					var err error
					vm1 := CreateVmObject(TestNamespace,
						[]kubevirtv1.Interface{newInterface("br1", testMacAddress)},
						[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

					baseVM := vm1.DeepCopy()

					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).To(HaveOccurred(), "should fail to create VM due to missing interface assignment to a network")

					baseVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(baseVM.Spec.Template.Spec.Domain.Devices.Interfaces, newInterface("br2", ""))

					Eventually(func() error {
						_, err = testClient.VirtClient.VirtualMachine(baseVM.Namespace).Create(context.TODO(), baseVM, metav1.CreateOptions{})
						if err != nil {
							Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
						}
						return err

					}, restoreFailedWebhookChangesTimeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
				})
				It("should allow to assign to the VM the same MAC addresses, different name as requested before and do not return an error", func() {
					var err error
					vm1 := CreateVmObject(TestNamespace,
						[]kubevirtv1.Interface{newInterface("br1", testMacAddress)},
						[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})

					baseVM := vm1.DeepCopy()
					baseVM.Name = "new-vm"

					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).To(HaveOccurred(), "should fail to create VM due to missing interface assignment to a network")

					baseVM.Spec.Template.Spec.Domain.Devices.Interfaces = append(baseVM.Spec.Template.Spec.Domain.Devices.Interfaces, newInterface("br2", ""))

					Eventually(func() error {
						_, err = testClient.VirtClient.VirtualMachine(baseVM.Namespace).Create(context.TODO(), baseVM, metav1.CreateOptions{})
						if err != nil {
							Expect(strings.Contains(err.Error(), "failed to allocate requested mac address")).To(Equal(true))
						}
						return err

					}, restoreFailedWebhookChangesTimeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
				})
			})
			Context("and a vm is created with no interfaces", func() {
				var err error
				maxNumOfIfaces := 16
				var vm *kubevirtv1.VirtualMachine
				interfaces := []kubevirtv1.Interface{}
				networks := []kubevirtv1.Network{}
				BeforeEach(func() {
					for numOfIfaces := 0; numOfIfaces < maxNumOfIfaces; numOfIfaces++ {
						interfaces = append(interfaces, newInterface("br"+strconv.Itoa(numOfIfaces), fmt.Sprintf("02:00:00:00:00:%02X", numOfIfaces)))
						networks = append(networks, newNetwork("br"+strconv.Itoa(numOfIfaces)))
					}
					vm = CreateVmObject(TestNamespace, []kubevirtv1.Interface{}, []kubevirtv1.Network{})
					By("Creating the vm with 0 interfaces")
					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")
				})
				Context(fmt.Sprintf("and then sequentially updated from %d interfaces back to 0 with minimal time between requests", maxNumOfIfaces), func() {
					BeforeEach(func() {
						for numOfIfaces := maxNumOfIfaces; numOfIfaces >= 0; numOfIfaces-- {
							By(fmt.Sprintf("updating the number of interfaces to %d", numOfIfaces))

							err = retry.RetryOnConflict(retry.DefaultRetry, func() error {

								vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
								Expect(err).ToNot(HaveOccurred())

								vm.Spec.Template.Spec.Domain.Devices.Interfaces = interfaces[0:numOfIfaces]
								vm.Spec.Template.Spec.Networks = networks[0:numOfIfaces]

								_, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Update(context.TODO(), vm, metav1.UpdateOptions{})
								return err
							})

							Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Should succeed updating the vm to %d interfaces", numOfIfaces))
						}

						By("Checking that the vm has 0 interface after the changes are made")
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred(), "Should succeed getting vm")
						Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces).To(BeEmpty(), "Should Have no interfaces")
					})
					It("should be able to allocate the freed macs to new vms", func() {
						By(fmt.Sprintf("Creating %d vms, each with a MAC interface used in the base vm", maxNumOfIfaces))
						for ifaceIdx := 0; ifaceIdx < maxNumOfIfaces; ifaceIdx++ {
							Eventually(func() error {
								newVm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{interfaces[ifaceIdx]}, []kubevirtv1.Network{networks[ifaceIdx]})
								newVm, createErr := testClient.VirtClient.VirtualMachine(newVm.Namespace).Create(context.TODO(), newVm, metav1.CreateOptions{})

								if createErr != nil {
									Expect(createErr).Should(MatchError("admission webhook \"mutatevirtualmachines.kubemacpool.io\" denied the request: Failed to create virtual machine allocation error: Failed to allocate mac to the vm object: failed to allocate requested mac address"), "Should only fail to allocate vm because the mac is already used")
								}
								return createErr

							}, restoreFailedWebhookChangesTimeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")
						}
					})
				})
				Context(fmt.Sprintf("and then sequentially updated from 0 to %d interfaces with minimal time between requests", maxNumOfIfaces), func() {
					BeforeEach(func() {
						for numOfIfaces := 1; numOfIfaces <= maxNumOfIfaces; numOfIfaces++ {
							By(fmt.Sprintf("updating the number of interfaces to %d", numOfIfaces))
							err = retry.RetryOnConflict(retry.DefaultRetry, func() error {

								vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
								Expect(err).ToNot(HaveOccurred())

								vm.Spec.Template.Spec.Domain.Devices.Interfaces = interfaces[0:numOfIfaces]
								vm.Spec.Template.Spec.Networks = networks[0:numOfIfaces]

								_, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Update(context.TODO(), vm, metav1.UpdateOptions{})
								return err
							})

							Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Should succeed updating the vm to %d interfaces", numOfIfaces))
						}

						By(fmt.Sprintf("Checking that the vm has %d interface after the changes are made", maxNumOfIfaces))
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred(), "Should succeed getting vm")
						Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces).To(HaveLen(maxNumOfIfaces), fmt.Sprintf("Should have %d interfaces", maxNumOfIfaces))
					})
					It("should not be able to allocate the occupied macs to new vms", func() {
						By(fmt.Sprintf("Creating %d vms, each with a MAC interface used in the base vm", maxNumOfIfaces))
						for ifaceIdx := 0; ifaceIdx < maxNumOfIfaces; ifaceIdx++ {
							newVm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{interfaces[ifaceIdx]}, []kubevirtv1.Network{networks[ifaceIdx]})
							_, err = testClient.VirtClient.VirtualMachine(newVm.Namespace).Create(context.TODO(), newVm, metav1.CreateOptions{})
							Expect(err).To(HaveOccurred(), "Should fail creating the vm")
						}
					})
				})
			})
			Context("and a VM's NIC is added just before an old update request with no NICs is made", func() {
				var vm *kubevirtv1.VirtualMachine
				var reusedMacAddress string
				BeforeEach(func() {
					var err error
					By("Creating a vm with no NICs")
					vm = CreateVmObject(TestNamespace, []kubevirtv1.Interface{}, []kubevirtv1.Network{})
					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")

					By("Saving the vm instance to reuse it after resourceVersion has changed to cause a conflict error")
					copyVm := vm.DeepCopy()

					By("Adding a new NIC to the vm")
					err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())

						vm.Spec.Template.Spec.Domain.Devices.Interfaces = []kubevirtv1.Interface{newInterface("br1", "")}
						vm.Spec.Template.Spec.Networks = []kubevirtv1.Network{newNetwork("br1")}
						_, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Update(context.TODO(), vm, metav1.UpdateOptions{})
						return err
					})
					Expect(err).ToNot(HaveOccurred(), "Should succeed updating the vm")

					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					reusedMacAddress = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
					_, err = net.ParseMAC(reusedMacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing the vm mac")

					By("Issuing the outdated vm request")
					_, err = testClient.VirtClient.VirtualMachine(copyVm.Namespace).Update(context.TODO(), copyVm, metav1.UpdateOptions{})
					Expect(apierrors.IsConflict(err)).Should(Equal(true), "Should fail update on conflict")
				})
				It("should successfully reject the old request on conflict and should reject a new vm trying to allocate using this mac", func() {
					By(fmt.Sprintf("waiting for cache to be restored after %v", restoreFailedWebhookChangesTimeout))
					time.Sleep(restoreFailedWebhookChangesTimeout)

					By("trying to reuse the mac in a new vm")
					var err error
					newVM := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", reusedMacAddress)},
						[]kubevirtv1.Network{newNetwork("br1")})
					newVM, err = testClient.VirtClient.VirtualMachine(newVM.Namespace).Create(context.TODO(), newVM, metav1.CreateOptions{})
					Expect(err).Should(MatchError("admission webhook \"mutatevirtualmachines.kubemacpool.io\" denied the request: Failed to create virtual machine allocation error: Failed to allocate mac to the vm object: failed to allocate requested mac address"), "Should fail to allocate vm because the mac is already used")
				})
			})
			Context("and a VM's NIC is removed and a new VM is created with the same MAC", func() {
				It("[test_id:2995]should successfully release the MAC and the new VM should be created with no errors", func() {
					var err error
					vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", ""), newInterface("br2", "")},
						[]kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")

					Expect(net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse vm's first mac address")
					Expect(net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[1].MacAddress)).ToNot(BeEmpty(), "Should successfully parse vm's second mac address")

					reusedMacAddress := vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
					By("checking that a new VM cannot be created when the mac is already occupied")
					newVM := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", reusedMacAddress)},
						[]kubevirtv1.Network{newNetwork("br1")})
					_, err = testClient.VirtClient.VirtualMachine(newVM.Namespace).Create(context.TODO(), newVM, metav1.CreateOptions{})
					Expect(err).Should(MatchError("admission webhook \"mutatevirtualmachines.kubemacpool.io\" denied the request: Failed to create virtual machine allocation error: Failed to allocate mac to the vm object: failed to allocate requested mac address"), "Should fail to allocate vm because the mac is already used")

					By("checking that the VM's NIC can be removed")
					err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred(), "Should succeed getting vm")

						vm.Spec.Template.Spec.Domain.Devices.Interfaces = []kubevirtv1.Interface{newInterface("br2", "")}
						vm.Spec.Template.Spec.Networks = []kubevirtv1.Network{newNetwork("br2")}

						_, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Update(context.TODO(), vm, metav1.UpdateOptions{})
						return err
					})
					Expect(err).ToNot(HaveOccurred(), "should succeed to remove NIC from vm")

					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed getting vm")
					Expect(len(vm.Spec.Template.Spec.Domain.Devices.Interfaces) == 1).To(Equal(true), "vm's total NICs should be 1")

					By("checking that a new VM can be created after the VM's NIC had been removed ")
					Eventually(func() error {
						_, err = testClient.VirtClient.VirtualMachine(newVM.Namespace).Create(context.TODO(), newVM, metav1.CreateOptions{})
						if err != nil {
							Expect(err).Should(MatchError("admission webhook \"mutatevirtualmachines.kubemacpool.io\" denied the request: Failed to create virtual machine allocation error: Failed to allocate mac to the vm object: failed to allocate requested mac address"), "Should only get a mac-allocation denial error until cache get updated")
						}
						return err

					}, restoreFailedWebhookChangesTimeout, pollingInterval).ShouldNot(HaveOccurred(), "failed to apply the new vm object")

					newVM, err = testClient.VirtClient.VirtualMachine(newVM.Namespace).Get(context.TODO(), newVM.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed getting newVM")
					Expect(len(newVM.Spec.Template.Spec.Domain.Devices.Interfaces) == 1).To(Equal(true), "new vm should be allocated with the now released mac address")
				})
			})
			Context("And checking kubemacpool upgrade", func() {
				allocatedMacAddress := testMacAddress
				BeforeEach(func() {
					Expect(createVmWaitConfigMap()).To(Succeed(), "should succeed creating configMap")
				})
				AfterEach(func() {
					Expect(deleteVmWaitConfigMap()).To(Succeed(), "should succeed removing configMap if present")
				})
				Context("When a vm was not successfully created in a version that uses the legacy configMap", func() {
					BeforeEach(func() {
						By("simulating a leftover configMap entry from a failed vm, entry soon to become stale")
						Expect(simulateSoonToBeStaleEntryInConfigMap(allocatedMacAddress)).To(Succeed())
					})
					Context("and then KMP is upgraded", func() {
						BeforeEach(func() {
							By("simulating kubemacpool upgrade with restart")
							err := initKubemacpoolParams()
							Expect(err).ToNot(HaveOccurred(), "should succeed restarting KMP")
						})
						It("Should make sure configMap is removed", func() {
							_, err := getVmWaitConfigMap()
							Expect(apierrors.IsNotFound(err)).To(BeTrue(), "legacy configMap should be removed after Kubemacpool restart")
						})
						It("Should remove the vm from cache", func() {
							var err error
							vm := CreateVmObject(TestNamespace,
								[]kubevirtv1.Interface{newInterface("br1", allocatedMacAddress)},
								[]kubevirtv1.Network{newNetwork("br1")})
							By("checking that the mac is occupied by trying to allocate it in a new vm")

							vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
							Expect(err).To(HaveOccurred(), "should fail creating a vm with the now occupied mac address")

							By("checking that the mac is released by trying to allocate it in a new vm")
							vm1 := CreateVmObject(TestNamespace,
								[]kubevirtv1.Interface{newInterface("br1", allocatedMacAddress)},
								[]kubevirtv1.Network{newNetwork("br1")})
							Eventually(func() error {
								_, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
								return err
							}, restoreFailedWebhookChangesTimeout, pollingInterval).ShouldNot(HaveOccurred(), "should succeed creating a vm with the now released mac address after stale mac restored")
						})
					})
				})
				Context("When a vm was successfully created in a version that uses the legacy configMap", func() {
					BeforeEach(func() {
						var err error
						vm := CreateVmObject(TestNamespace,
							[]kubevirtv1.Interface{newInterface("br1", allocatedMacAddress)},
							[]kubevirtv1.Network{newNetwork("br1")})
						By("simulating a vm created on version using legacy configMap")
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "should succeed creating a vm with the now released mac address")
						Expect(simulateSoonToBeStaleEntryInConfigMap(allocatedMacAddress)).To(Succeed())
					})
					Context("and then KMP is upgraded", func() {
						BeforeEach(func() {
							By("simulating kubemacpool upgrade with restart")
							err := initKubemacpoolParams()
							Expect(err).ToNot(HaveOccurred(), "should succeed restarting KMP")
						})
						It("Should make sure configMap is removed", func() {
							_, err := getVmWaitConfigMap()
							Expect(apierrors.IsNotFound(err)).To(BeTrue(), "legacy configMap should be removed after Kubemacpool restart")
						})
						It("Should remove the vm from cache", func() {
							By("checking that the mac is occupied and not removed as stale by trying to allocate it in a new vm")
							vm1 := CreateVmObject(TestNamespace,
								[]kubevirtv1.Interface{newInterface("br1", allocatedMacAddress)},
								[]kubevirtv1.Network{newNetwork("br1")})
							Consistently(func() error {
								var err error
								_, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
								return err
							}, timeout, pollingInterval).ShouldNot(Succeed())
						})
					})
				})
			})
		})

		Context("When all the VMs are not serviced by default (opt-in mode)", func() {
			BeforeEach(func() {
				By("setting vm webhook to not accept all namespaces unless they include opt-in label")
				err := setWebhookOptMode(vmNamespaceOptInLabel, optInMode)
				Expect(err).ToNot(HaveOccurred(), "should set opt-mode to mutatingwebhookconfiguration")
			})

			Context("and kubemacpool is not opted-in on a namespace", func() {
				var allocatedMac, preSetMac string
				notManagedNamespace := TestNamespace
				managedNamespace := OtherTestNamespace
				BeforeEach(func() {
					By("Removing any namespace labels to make sure that kubemacpool is not opted in on the namespace")
					err := cleanNamespaceLabels(notManagedNamespace)
					Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")
				})
				Context("and 2 vms is created in the non opted-in namespace", func() {
					var notManagedVm1, notManagedVm2 *kubevirtv1.VirtualMachine
					BeforeEach(func() {
						By("Adding a vm with no preset mac")
						var err error
						notManagedVm1 = CreateVmObject(notManagedNamespace, []kubevirtv1.Interface{newInterface("br", "")},
							[]kubevirtv1.Network{newNetwork("br")})
						notManagedVm1, err = testClient.VirtClient.VirtualMachine(notManagedVm1.Namespace).Create(context.TODO(), notManagedVm1, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")

						By("Adding a vm with a preset mac")
						preSetMac = testMacAddress
						notManagedVm2 = CreateVmObject(notManagedNamespace, []kubevirtv1.Interface{newInterface("br", preSetMac)},
							[]kubevirtv1.Network{newNetwork("br")})
						notManagedVm2, err = testClient.VirtClient.VirtualMachine(notManagedVm2.Namespace).Create(context.TODO(), notManagedVm2, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")
					})
					It("should create the VM objects without a MAC allocated by Kubemacpool", func() {
						Expect(notManagedVm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).Should(Equal(""), "should not allocate a mac to the opted-out vm")
						Expect(notManagedVm2.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).Should(Equal(preSetMac), "should not allocate a mac to the opted-out vm")
					})
					Context("and another vm is created in an opted-in namespace", func() {
						BeforeEach(func() {
							By(fmt.Sprintf("opting in the %s namespace", managedNamespace))
							err := addLabelsToNamespace(managedNamespace, map[string]string{vmNamespaceOptInLabel: "allocate"})
							Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")

							By("creating a vm in the opted-in namespace")
							managedVm := CreateVmObject(managedNamespace, []kubevirtv1.Interface{newInterface("br", "")},
								[]kubevirtv1.Network{newNetwork("br")})
							managedVm, err = testClient.VirtClient.VirtualMachine(managedVm.Namespace).Create(context.TODO(), managedVm, metav1.CreateOptions{})
							Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")
							allocatedMac = managedVm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
						})
						It("should create the opted-in VM object with a MAC assigned", func() {
							Expect(net.ParseMAC(allocatedMac)).ToNot(BeEmpty(), "Should successfully parse mac address")
						})
						It("should reject a new opted-in VM object with the occupied MAC assigned", func() {
							anotherManagedVm := CreateVmObject(managedNamespace, []kubevirtv1.Interface{newInterface("br", allocatedMac)},
								[]kubevirtv1.Network{newNetwork("br")})
							_, err := testClient.VirtClient.VirtualMachine(anotherManagedVm.Namespace).Create(context.TODO(), anotherManagedVm, metav1.CreateOptions{})
							Expect(err).To(HaveOccurred(), "Should reject a new vm with occupied mac address")
						})
						It("should not reject a new opted-in VM object with unmanaged preset MAC", func() {
							anotherManagedVm := CreateVmObject(managedNamespace, []kubevirtv1.Interface{newInterface("br", preSetMac)},
								[]kubevirtv1.Network{newNetwork("br")})
							_, err := testClient.VirtClient.VirtualMachine(anotherManagedVm.Namespace).Create(context.TODO(), anotherManagedVm, metav1.CreateOptions{})
							Expect(err).ToNot(HaveOccurred(), "Should not reject a new vm if the mac is occupied on a non-opted-in namespace")
						})

						Context("and kubemacpool restarts", func() {
							BeforeEach(func() {
								//restart kubeMacPool
								err := initKubemacpoolParams()
								Expect(err).ToNot(HaveOccurred())
							})

							It("should still reject a new opted-in VM object with the occupied MAC assigned", func() {
								anotherManagedVm := CreateVmObject(managedNamespace, []kubevirtv1.Interface{newInterface("br", allocatedMac)},
									[]kubevirtv1.Network{newNetwork("br")})
								_, err := testClient.VirtClient.VirtualMachine(anotherManagedVm.Namespace).Create(context.TODO(), anotherManagedVm, metav1.CreateOptions{})
								Expect(err).To(HaveOccurred(), "Should reject a new vm with occupied mac address")
							})
							It("should still not reject a new opted-in VM object with unmanaged preset MAC", func() {
								anotherManagedVm := CreateVmObject(managedNamespace, []kubevirtv1.Interface{newInterface("br", preSetMac)},
									[]kubevirtv1.Network{newNetwork("br")})
								_, err := testClient.VirtClient.VirtualMachine(anotherManagedVm.Namespace).Create(context.TODO(), anotherManagedVm, metav1.CreateOptions{})
								Expect(err).ToNot(HaveOccurred(), "Should not reject a new vm if the mac is occupied on a non-opted-in namespce")
							})
						})
					})
				})
			})
		})

		Context("When all the VMs are serviced by default (opt-out mode)", func() {
			BeforeEach(func() {
				By("setting vm webhook to accept all namespaces that don't include opt-out label")
				err := setWebhookOptMode(vmNamespaceOptInLabel, optOutMode)
				Expect(err).ToNot(HaveOccurred(), "should set opt-mode to mutatingwebhookconfiguration")
			})

			Context("and kubemacpool is opted-out on a namespace", func() {
				BeforeEach(func() {
					By("opting out the namespace")
					err := addLabelsToNamespace(TestNamespace, map[string]string{vmNamespaceOptInLabel: "ignore"})
					Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")
				})
				It("should create a VM object without a MAC assigned", func() {
					var err error
					vm := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")},
						[]kubevirtv1.Network{newNetwork("br")})

					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")
					Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).Should(Equal(""), "should not allocated a mac to the opted-out vm")
				})
			})

			Context("and the client creates a vm on a non-opted-out namespace", func() {
				var vm *kubevirtv1.VirtualMachine
				var allocatedMac string
				BeforeEach(func() {
					vm = CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")},
						[]kubevirtv1.Network{newNetwork("br")})

					By("Create VM")
					var err error
					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred(), "should success creating the vm")
					allocatedMac = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				})
				It("should automatically assign the vm with static MAC address within range", func() {
					By("Retrieve VM")
					var err error
					vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred(), "should success getting the VM after creating it")

					Expect(net.ParseMAC(allocatedMac)).ToNot(BeEmpty(), "Should successfully parse mac address")
				})
				It("should reject a new VM object with the occupied MAC assigned", func() {
					vm = CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", allocatedMac)},
						[]kubevirtv1.Network{newNetwork("br")})
					_, err := testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
					Expect(err).To(HaveOccurred(), "Should reject a new vm with occupied mac address")
				})
				Context("and then when we opt-out the namespace", func() {
					BeforeEach(func() {
						By("Adding namespace opt-out label to mark it as opted-out")
						err := addLabelsToNamespace(TestNamespace, map[string]string{vmNamespaceOptInLabel: "ignore"})
						Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")

						By("Wait 5 seconds for namespace label clean-up to be propagated at server")
						time.Sleep(5 * time.Second)
					})
					AfterEach(func() {
						By("Removing namespace opt-out label to get kmp service again")
						err := cleanNamespaceLabels(TestNamespace)
						Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")

						By("Wait 5 seconds for opt-in label at namespace to be propagated at server")
						time.Sleep(5 * time.Second)
					})
					It("should able to be deleted", func() {
						By("Delete the VM after opt-out the namespace")
						deleteVMI(vm)
					})
				})
			})
			Context("and testing finalizers removal", func() {
				Context("When VM with legacy finalizer is being deleted", func() {
					var vm *kubevirtv1.VirtualMachine
					BeforeEach(func() {
						var err error
						vm = CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")},
							[]kubevirtv1.Network{newNetwork("br")})
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred())
						By("checking the original created vm does not have a finalizer")
						Expect(vm.GetFinalizers()).ToNot(ContainElements(pool_manager.RuntimeObjectFinalizerName), "vm should have a finalizer")
						By("adding a finalizer to the vm")
						Expect(addFinalizer(vm, pool_manager.RuntimeObjectFinalizerName)).To(Succeed(), "Should succeed adding the legacy finalizer to the vm")
						vm, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						Expect(vm.GetFinalizers()).To(ContainElements(pool_manager.RuntimeObjectFinalizerName), "vm should have a finalizer")
					})
					It("should allow vm removal", func() {
						By("deleting the vm")
						err := testClient.VirtClient.VirtualMachine(vm.Namespace).Delete(context.TODO(), vm.Name, metav1.DeleteOptions{})
						Expect(err).ToNot(HaveOccurred())

						Eventually(func() error {
							_, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
							return err
						}, timeout, pollingInterval).Should(SatisfyAll(HaveOccurred(), WithTransform(apierrors.IsNotFound, BeTrue())), "should remove the finalizer and delete the vm")
					})
				})
			})

			Context("and creating two VMs with same mac, one on unmanaged (opted out) namespace", func() {
				var conflictingVM *kubevirtv1.VirtualMachine
				BeforeEach(func() {
					var err error
					macAddress := "02:00:ff:ff:ff:ff"

					By(fmt.Sprintf("Adding a vm with a Mac Address %s in the managed namespace", macAddress))
					vm1 := CreateVmObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", macAddress)}, []kubevirtv1.Network{newNetwork("br1")})
					vm1, err = testClient.VirtClient.VirtualMachine(vm1.Namespace).Create(context.TODO(), vm1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")

					err = addLabelsToNamespace(OtherTestNamespace, map[string]string{vmNamespaceOptInLabel: "ignore"})
					Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")

					By(fmt.Sprintf("Adding a vm with the same Mac Address %s on the unmanaged namespace", macAddress))
					conflictingVM = CreateVmObject(OtherTestNamespace, []kubevirtv1.Interface{newInterface("br1", macAddress)}, []kubevirtv1.Network{newNetwork("br1")})
					conflictingVM, err = testClient.VirtClient.VirtualMachine(conflictingVM.Namespace).Create(context.TODO(), conflictingVM, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(conflictingVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(), "Should successfully parse mac address")
				})

				It("should detect duplicate macs gauge after restarting kubemacpool, and then mitigating that", func() {
					By("moving the unmanaged namespace to be managed")
					err := cleanNamespaceLabels(OtherTestNamespace)
					Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")

					By("restarting Kubemacpool to check if there are duplicats macs in the managed namespaces")
					err = initKubemacpoolParams()
					Expect(err).ToNot(HaveOccurred())

					token, stderr, err := getPrometheusToken()
					Expect(err).ToNot(HaveOccurred(), stderr)

					metrics, _, err := getMetrics(token)
					Expect(err).ToNot(HaveOccurred())

					expectedMetric := "kubevirt_kmp_duplicate_macs"
					expectedValue := "1"
					metric := findMetric(metrics, expectedMetric)
					Expect(metric).ToNot(BeEmpty(), fmt.Sprintf("metric %s does not appear in endpoint scrape", expectedMetric))
					Expect(metric).To(Equal(fmt.Sprintf("%s %s", expectedMetric, expectedValue)),
						fmt.Sprintf("metric %s does not have the expected value %s", expectedMetric, expectedValue))

					By("mitigating the conflict by deleting the vm with the conflicting Mac Address")
					err = testClient.VirtClient.VirtualMachine(conflictingVM.Namespace).Delete(context.TODO(), conflictingVM.Name, metav1.DeleteOptions{})
					Expect(err).ToNot(HaveOccurred())

					By("restarting Kubemacpool")
					err = initKubemacpoolParams()
					Expect(err).ToNot(HaveOccurred())

					By("verifying the conflict has been resolved")
					metrics, _, err = getMetrics(token)
					Expect(err).ToNot(HaveOccurred())

					expectedValue = "0"
					metric = findMetric(metrics, expectedMetric)
					Expect(metric).ToNot(BeEmpty(), fmt.Sprintf("metric %s does not appear in endpoint scrape", expectedMetric))
					Expect(metric).To(Equal(fmt.Sprintf("%s %s", expectedMetric, expectedValue)),
						fmt.Sprintf("metric %s does not have the expected value %s", expectedMetric, expectedValue))
				})
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
	By(fmt.Sprintf("Delete vm %s/%s", vm.Namespace, vm.Name))
	err := testClient.VirtClient.VirtualMachine(vm.Namespace).Delete(context.TODO(), vm.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).ToNot(HaveOccurred(), "should success deleting VM")

	By(fmt.Sprintf("Wait for vm %s/%s to be deleted", vm.Namespace, vm.Name))
	Eventually(func() bool {
		_, err = testClient.VirtClient.VirtualMachine(vm.Namespace).Get(context.TODO(), vm.Name, metav1.GetOptions{})
		if err != nil && apierrors.IsNotFound(err) {
			return true
		}
		Expect(err).ToNot(HaveOccurred(), "should success getting vm if is still there")
		return false
	}, timeout, pollingInterval).Should(BeTrue(), "should eventually fail getting vm with IsNotFound after vm deletion")
}

func getMetrics(token string) (string, string, error) {
	podList, err := getManagerPods()
	if err != nil {
		return "", "", err
	}

	bearer := "Authorization: Bearer " + token
	stdout, stderr, err := kubectl.Kubectl("exec", "-n", managerNamespace, podList.Items[0].Name, "--",
		"curl", "-s", "-k", "--header", bearer, "https://127.0.0.1:8443/metrics")

	return stdout, stderr, err
}

func getManagerPods() (*v1.PodList, error) {
	deployment, err := testClient.VirtClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(), names.MANAGER_DEPLOYMENT, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelSelector := labels.Set(deployment.Spec.Selector.MatchLabels).String()
	podList, err := testClient.VirtClient.CoreV1().Pods(managerNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}

	return podList, err
}

func findMetric(metrics string, expectedMetric string) string {
	for _, line := range strings.Split(metrics, "\n") {
		if strings.HasPrefix(line, expectedMetric+" ") {
			return line
		}
	}

	return ""
}

func getPrometheusToken() (string, string, error) {
	const (
		monitoringNamespace = "monitoring"
		prometheusPod       = "prometheus-k8s-0"
		container           = "prometheus"
		tokenPath           = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	)

	return kubectl.Kubectl("exec", "-n", monitoringNamespace, prometheusPod, "-c", container, "--", "cat", tokenPath)
}
