package tests

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const MACAllocationLabel = "mac-allocation"

const timeout = 2 * time.Minute
const pollingInterval = 5 * time.Second
const testMacAddress = "02:00:ff:ff:ff:ff"

// TODO: the rfe_id was taken from kubernetes-nmstate we have to discover the rigth parameters here
var _ = Describe("[rfe_id:3503][crit:medium][vendor:cnv-qe@redhat.com][level:component]Virtual Machines", Ordered, func() {
	BeforeAll(func() {
		By("Creating network attachment definitions")
		Expect(createNetworkAttachmentDefinition(TestNamespace, "linux-bridge")).To(Succeed())
	})

	BeforeEach(func() {
		By("Verify that there are no VMs left from previous tests")
		currentVMList := &kubevirtv1.VirtualMachineList{}
		err := testClient.CRClient.List(context.TODO(), currentVMList)
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
			vmList := &kubevirtv1.VirtualMachineList{}
			err := testClient.CRClient.List(context.TODO(), vmList)
			Expect(err).ToNot(HaveOccurred())

			for i := range vmList.Items {
				vmObject := &vmList.Items[i]
				err := testClient.CRClient.Delete(context.TODO(), vmObject)
				Expect(err).ToNot(HaveOccurred())
			}

			Eventually(func() int {
				vmList := &kubevirtv1.VirtualMachineList{}
				listErr := testClient.CRClient.List(context.TODO(), vmList)
				Expect(listErr).ToNot(HaveOccurred())
				return len(vmList.Items)

			}, timeout, pollingInterval).Should(Equal(0), "failed to remove all vm objects")
		})
		Context("When Running with default opt-mode configuration", func() {
			BeforeEach(func() {
				By("Getting the current VM Opt-mode")
				vmWebhookOptMode, err := getOptMode(vmNamespaceOptInLabel)
				Expect(err).ToNot(HaveOccurred(), "Should succeed getting the mutating webhook opt-mode")

				if vmWebhookOptMode == optInMode {
					optInNamespaceForVMs(TestNamespace)
				}
			})

			Context("and applying a dryRun vm create request", func() {
				var macAddress string
				BeforeEach(func() {
					By("Creating a vm with dry run mode")
					var err error
					vm := CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})
					createOptions := &client.CreateOptions{}
					client.DryRunAll.ApplyToCreate(createOptions)

					err = testClient.CRClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(),
						"Should successfully parse mac address")
					macAddress = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress
				})

				// TODO Add dry-run functionality in kubevirt/client-go's VirtualMachine().Create(context.TODO(), )
				PIt("should not allocate the Mac assigned in the dryRun request into the pool", func() {
					By("creating a vm with the same mac to make sure the dryRun assigned mac is not occupied on the macpool")
					var err error
					vmDryRunOverlap := CreateVMObject(TestNamespace,
						[]kubevirtv1.Interface{newInterface("brDryRunOverlap", macAddress)},
						[]kubevirtv1.Network{newNetwork("brDryRunOverlap")})
					err = testClient.CRClient.Create(context.TODO(), vmDryRunOverlap)
					Expect(err).ToNot(HaveOccurred())

					actualMac, err := net.ParseMAC(vmDryRunOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should succeed parsing vmDryRunOverlap mac")

					Expect(actualMac.String()).To(Equal(macAddress), "Should successfully parse mac address")
				})
			})

			Context("and a simple vm is applied", func() {
				It("should not remove the transactions timestamp annotation", Label(MACAllocationLabel), func() {
					vm := CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")}, []kubevirtv1.Network{newNetwork("br")})
					err := testClient.CRClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred())

					Consistently(func() (string, error) {
						err = testClient.CRClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
						if err != nil {
							return "", err
						}
						return vm.Annotations[pool_manager.TransactionTimestampAnnotation], nil
					}).WithPolling(time.Second).WithTimeout(5*time.Second).ShouldNot(BeEmpty(),
						"vm %s should have a transaction timestamp annotation", vm.Name)
				})
			})

			Context("and the client tries to assign the same MAC address for two different interfaces in the same VM.", func() {
				Context("and when the MAC address is within range", func() {
					It("[test_id:2199]should reject a VM creation with intra-VM duplicate MAC addresses", Label(MACAllocationLabel), func() {
						var err error
						vm := CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", "02:00:00:00:ff:ff"),
							newInterface("br2", "02:00:00:00:ff:ff")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
						err = testClient.CRClient.Create(context.TODO(), vm)
						Expect(err).To(HaveOccurred())
						Expect(err).To(MatchError(ContainSubstring("duplicate MAC address")))
					})
				})
				Context("and when the MAC address is out of range", func() {
					It("[test_id:2200]should reject a VM creation with intra-VM duplicate MAC addresses", Label(MACAllocationLabel), func() {
						var err error
						vm := CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", "06:00:00:00:00:00"),
							newInterface("br2", "06:00:00:00:00:00")}, []kubevirtv1.Network{newNetwork("br1"), newNetwork("br2")})
						err = testClient.CRClient.Create(context.TODO(), vm)
						Expect(err).To(HaveOccurred())
						Expect(err).To(MatchError(ContainSubstring("duplicate MAC address")))
					})
				})
			})

			Context("and we re-apply a VM yaml", func() {
				It("[test_id:2243]should preserve MAC addresses when re-applying VM", Label(MACAllocationLabel), func() {
					intrefaces := make([]kubevirtv1.Interface, 5)
					networks := make([]kubevirtv1.Network, 5)
					for i := 0; i < 5; i++ {
						intrefaces[i] = newInterface("br"+strconv.Itoa(i), "")
						networks[i] = newNetwork("br" + strconv.Itoa(i))
					}

					vm1 := CreateVMObject(TestNamespace, intrefaces, networks)
					updateObject := vm1.DeepCopy()

					var err error
					err = testClient.CRClient.Create(context.TODO(), vm1)
					Expect(err).ToNot(HaveOccurred())

					updateObject.ObjectMeta = *vm1.ObjectMeta.DeepCopy()

					for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
						Expect(net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress)).ToNot(BeEmpty(),
							"Should successfully parse mac address")
					}

					err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
						err = testClient.CRClient.Get(context.TODO(),
							client.ObjectKey{Namespace: updateObject.Namespace, Name: updateObject.Name}, updateObject)
						Expect(err).ToNot(HaveOccurred())

						err = testClient.CRClient.Update(context.TODO(), updateObject)
						return err
					})
					Expect(err).ToNot(HaveOccurred())

					for index := range vm1.Spec.Template.Spec.Domain.Devices.Interfaces {
						Expect(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress).To(
							Equal(updateObject.Spec.Template.Spec.Domain.Devices.Interfaces[index].MacAddress))
					}
				})
			})

			Context("and testing finalizers removal", func() {
				Context("When VM with legacy finalizer is being deleted", func() {
					var vm *kubevirtv1.VirtualMachine
					BeforeEach(func() {
						var err error
						vm = CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("br", "")},
							[]kubevirtv1.Network{newNetwork("br")})
						err = testClient.CRClient.Create(context.TODO(), vm)
						Expect(err).ToNot(HaveOccurred())
						By("checking the original created vm does not have a finalizer")
						Expect(vm.GetFinalizers()).ToNot(ContainElements(pool_manager.RuntimeObjectFinalizerName), "vm should have a finalizer")
						By("adding a finalizer to the vm")
						Expect(addFinalizer(vm, pool_manager.RuntimeObjectFinalizerName)).To(Succeed(),
							"Should succeed adding the legacy finalizer to the vm")
						err = testClient.CRClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
						Expect(err).ToNot(HaveOccurred())
						Expect(vm.GetFinalizers()).To(ContainElements(pool_manager.RuntimeObjectFinalizerName), "vm should have a finalizer")
					})
					It("should allow vm removal", Label(MACAllocationLabel), func() {
						By("deleting the vm")
						err := testClient.CRClient.Delete(context.TODO(), vm)
						Expect(err).ToNot(HaveOccurred())

						Eventually(func() error {
							err = testClient.CRClient.Get(context.TODO(), client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, vm)
							return err
						}, timeout, pollingInterval).Should(SatisfyAll(HaveOccurred(), WithTransform(apierrors.IsNotFound, BeTrue())),
							"should remove the finalizer and delete the vm")
					})
				})
			})

			Context("and a VM is rebooted", Label(MACCollisionDetectionLabel), func() {
				var nadName string

				BeforeEach(func() {
					nadName = randName("br-reboot")
					By(fmt.Sprintf("Creating network attachment definition: %s", nadName))
					Expect(createNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
				})

				AfterEach(func() {
					By("Deleting network attachment definition")
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
				})

				It("should not report collision when VM with set MAC is rebooted", func() {
					const vmMAC = "02:00:00:00:00:99"

					By("Creating VM with RunStrategy Always and set MAC")
					vm := CreateVMObject(TestNamespace,
						[]kubevirtv1.Interface{newInterface(nadName, vmMAC)},
						[]kubevirtv1.Network{newNetwork(nadName)})
					runStrategyAlways := kubevirtv1.RunStrategyAlways
					vm.Spec.RunStrategy = &runStrategyAlways
					Expect(testClient.CRClient.Create(context.TODO(), vm)).To(Succeed())

					By("Waiting for initial VMI to reach Running phase")
					vmNamespace := vm.Namespace
					vmName := vm.Name
					var initialVMIUID string
					Eventually(func() kubevirtv1.VirtualMachineInstancePhase {
						phase := getVMIPhase(vmNamespace, vmName)
						if phase == kubevirtv1.Running {
							// Capture the UID of the initial VMI
							vmi := &kubevirtv1.VirtualMachineInstance{}
							err := testClient.CRClient.Get(context.TODO(),
								client.ObjectKey{Namespace: vmNamespace, Name: vmName}, vmi)
							if err == nil {
								initialVMIUID = string(vmi.UID)
							}
						}
						return phase
					}).WithTimeout(timeout).WithPolling(pollingInterval).Should(Equal(kubevirtv1.Running))

					By(fmt.Sprintf("Initial VMI is running with UID: %s", initialVMIUID))

					By("Restarting the VM")
					Expect(restartVirtualMachine(vmNamespace, vmName)).To(Succeed())

					By("Waiting for VMI to be restarted (UID should change)")
					var newVMIUID string
					Eventually(func(g Gomega) {
						vmi := &kubevirtv1.VirtualMachineInstance{}
						err := testClient.CRClient.Get(context.TODO(),
							client.ObjectKey{Namespace: vmNamespace, Name: vmName}, vmi)
						g.Expect(err).ToNot(HaveOccurred())

						// Only consider it restarted if it's Running AND has a different UID
						g.Expect(vmi.Status.Phase).To(Equal(kubevirtv1.Running))
						newVMIUID = string(vmi.UID)
						g.Expect(newVMIUID).ToNot(Equal(initialVMIUID))
					}).WithTimeout(timeout).WithPolling(pollingInterval).Should(Succeed(), "VMI should be restarted with new UID and reach Running phase")

					By(fmt.Sprintf("New VMI is running with UID: %s (old UID was: %s)", newVMIUID, initialVMIUID))

					By("Verifying that the new VMI does not have collision events")
					vmiRef := []vmiReference{{vmNamespace, vmName}}
					expectNoMACCollisionEvents(vmiRef, "after VM reboot")
					expectNoMACCollisionGaugeConsistently(vmMAC)
				})
			})

		})

		Context("When all the VMs are not serviced by default (opt-in mode)", Label("vm-opt-in"), func() {
			BeforeEach(func() {
				By("setting vm webhook to not accept all namespaces unless they include opt-in label")
				err := setVMWebhookOptMode(optInMode)
				Expect(err).ToNot(HaveOccurred(), "should set opt-mode to mutatingwebhookconfiguration")
			})

			Context("and kubemacpool is not opted-in on a namespace", func() {
				unmanagedNamespace := TestNamespace
				It("should create a VM object without a MAC assigned", func() {
					vm := CreateVMObject(unmanagedNamespace, []kubevirtv1.Interface{newInterface("br", "")},
						[]kubevirtv1.Network{newNetwork("br")})
					err := testClient.CRClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")
					Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).Should(Equal(""),
						"should not allocate a mac to the non-opted-in vm")
				})
			})
		})

		Context("When all the VMs are serviced by default (opt-out mode)", Label("vm-opt-out"), func() {
			BeforeEach(func() {
				By("setting vm webhook to accept all namespaces that don't include opt-out label")
				err := setVMWebhookOptMode(optOutMode)
				Expect(err).ToNot(HaveOccurred(), "should set opt-mode to mutatingwebhookconfiguration")
			})

			Context("and kubemacpool is opted-out on a namespace", func() {
				unmanagedNamespace := TestNamespace
				BeforeEach(func() {
					optOutNamespaceForVMs(unmanagedNamespace)
				})
				It("should create a VM object without a MAC assigned", Label(MACAllocationLabel), func() {
					vm := CreateVMObject(unmanagedNamespace, []kubevirtv1.Interface{newInterface("br", "")},
						[]kubevirtv1.Network{newNetwork("br")})
					err := testClient.CRClient.Create(context.TODO(), vm)
					Expect(err).ToNot(HaveOccurred(), "Should succeed creating the vm")
					Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).Should(Equal(""),
						"should not allocate a mac to the opted-out vm")
				})
			})

			Context("and creating two VMs with same mac, one on unmanaged (opted out) namespace", func() {
				var conflictingVM *kubevirtv1.VirtualMachine
				BeforeEach(func() {
					var err error
					macAddress := "02:00:ff:ff:ff:ff"

					By(fmt.Sprintf("Adding a vm with a Mac Address %s in the managed namespace", macAddress))
					vm1 := CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("br1", macAddress)}, []kubevirtv1.Network{newNetwork("br1")})
					err = testClient.CRClient.Create(context.TODO(), vm1)
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(vm1.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(),
						"Should successfully parse mac address")

					optOutNamespaceForVMs(OtherTestNamespace)

					By(fmt.Sprintf("Adding a vm with the same Mac Address %s on the unmanaged namespace", macAddress))
					conflictingVM = CreateVMObject(OtherTestNamespace,
						[]kubevirtv1.Interface{newInterface("br1", macAddress)},
						[]kubevirtv1.Network{newNetwork("br1")})
					err = testClient.CRClient.Create(context.TODO(), conflictingVM)
					Expect(err).ToNot(HaveOccurred())
					Expect(net.ParseMAC(conflictingVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)).ToNot(BeEmpty(),
						"Should successfully parse mac address")
				})

				It("should detect duplicate macs gauge after restarting kubemacpool, and then mitigating that", Label(MACAllocationLabel), func() {
					By("moving the unmanaged namespace to be managed")
					err := cleanNamespaceLabels(OtherTestNamespace)
					Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")

					By("restarting Kubemacpool to check if there are duplicates macs in the managed namespaces")
					err = initKubemacpoolParams()
					Expect(err).ToNot(HaveOccurred())

					token, stderr, err := getPrometheusToken()
					Expect(err).ToNot(HaveOccurred(), stderr)

					metrics, err := getMetrics(token)
					Expect(err).ToNot(HaveOccurred())

					expectedMetric := "kubevirt_kmp_duplicate_macs"
					expectedValue := "1"
					metric := findMetric(metrics, expectedMetric)
					Expect(metric).ToNot(BeEmpty(), fmt.Sprintf("metric %s does not appear in endpoint scrape", expectedMetric))
					Expect(metric).To(Equal(fmt.Sprintf("%s %s", expectedMetric, expectedValue)),
						fmt.Sprintf("metric %s does not have the expected value %s", expectedMetric, expectedValue))

					By("mitigating the conflict by deleting the vm with the conflicting Mac Address")
					err = testClient.CRClient.Delete(context.TODO(), conflictingVM)
					Expect(err).ToNot(HaveOccurred())

					By("waiting for VM to be fully deleted")
					Eventually(func() bool {
						err = testClient.CRClient.Get(context.TODO(),
							client.ObjectKey{Namespace: conflictingVM.Namespace, Name: conflictingVM.Name}, conflictingVM)
						return apierrors.IsNotFound(err)
					}, timeout, pollingInterval).Should(BeTrue(), "conflicting VM should be deleted")

					By("restarting Kubemacpool")
					err = initKubemacpoolParams()
					Expect(err).ToNot(HaveOccurred())

					By("verifying the conflict has been resolved")
					metrics, err = getMetrics(token)
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
