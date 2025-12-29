package tests

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	v1 "k8s.io/api/core/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

const MACCollisionDetectionLabel = "mac-collision-detection"

// TODO: the rfe_id was taken from kubernetes-nmstate we have to discover the right parameters here
var _ = Describe("[rfe_id:3503][crit:medium][vendor:cnv-qe@redhat.com][level:component]Virtual Machine Instances",
	Label(MACCollisionDetectionLabel), Ordered, func() {
		BeforeEach(func() {
			By("Verify that there are no VMIs left from previous tests")
			currentVMIList := &kubevirtv1.VirtualMachineInstanceList{}
			err := testClient.CRClient.List(context.TODO(), currentVMIList)
			Expect(err).ToNot(HaveOccurred(), "Should successfully list VMIs")
			Expect(len(currentVMIList.Items)).To(BeZero(), "There should be no VMIs in the cluster before a test")

			// remove all the labels from the test namespaces
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				err = cleanNamespaceLabels(namespace)
				Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")
			}
		})

		Context("VMI Collision Detection", func() {
			AfterEach(func() {
				vmiList := &kubevirtv1.VirtualMachineInstanceList{}
				Expect(testClient.CRClient.List(context.TODO(), vmiList)).To(Succeed())

				for i := range vmiList.Items {
					vmiObject := &vmiList.Items[i]
					err := testClient.CRClient.Delete(context.TODO(), vmiObject)
					if err != nil && !apierrors.IsNotFound(err) {
						Expect(err).ToNot(HaveOccurred())
					}
				}

				Eventually(func() []kubevirtv1.VirtualMachineInstance {
					vmiList := &kubevirtv1.VirtualMachineInstanceList{}
					Expect(testClient.CRClient.List(context.TODO(), vmiList)).To(Succeed())
					return vmiList.Items
				}).WithTimeout(timeout).WithPolling(pollingInterval).Should(HaveLen(0), "failed to remove all vmi objects")
			})

			AfterEach(func() {
				Expect(checkKubemacpoolCrash()).To(Succeed(), "Kubemacpool should not crash during test")
			})

			Context("and the client tries to assign the same MAC address for two different VMI. Within Range and out of range", func() {
				var nadName1, nadName2 string

				BeforeEach(func() {
					nadName1 = randName("br")
					nadName2 = randName("br-overlap")
					By(fmt.Sprintf("Creating network attachment definitions: %s, %s", nadName1, nadName2))
					Expect(createNetworkAttachmentDefinition(TestNamespace, nadName1)).To(Succeed())
					Expect(createNetworkAttachmentDefinition(TestNamespace, nadName2)).To(Succeed())
				})

				AfterEach(func() {
					By("Deleting network attachment definitions")
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName1)).To(Succeed())
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName2)).To(Succeed())
				})

				Context("When the MAC address is within range", func() {
					DescribeTable("[test_id:2166]should detect inter-VMI MAC collisions", func(separator string) {
						const sharedMAC = "02:00:00:00:00:01"
						macWithSeparator := strings.Replace(sharedMAC, ":", separator, 5)

						vmi1 := NewVMI(TestNamespace, "test-vmi-1",
							WithInterface(newInterface(nadName1, sharedMAC)),
							WithNetwork(newNetwork(nadName1)))
						Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed(), "First VMI should be created successfully")

						vmi2 := NewVMI(TestNamespace, "test-vmi-2",
							WithInterface(newInterface(nadName2, macWithSeparator)),
							WithNetwork(newNetwork(nadName2)))
						Expect(testClient.CRClient.Create(context.TODO(), vmi2)).To(Succeed(), "Second VMI with duplicate MAC should be created successfully")

						// Check for collision events on both VMIs
						normalizedMAC, err := net.ParseMAC(sharedMAC)
						Expect(err).ToNot(HaveOccurred(), "Should parse shared MAC")

						conflictingVMIs := []vmiReference{
							{vmi1.Namespace, vmi1.Name},
							{vmi2.Namespace, vmi2.Name},
						}

						waitForVMIsRunning(conflictingVMIs)
						expectMACCollisionEvents(normalizedMAC.String(), conflictingVMIs)
					},
						Entry("with the same mac format", ":"),
						Entry("with different mac format", "-"),
					)
				})
				Context("and the MAC address is out of range", func() {
					It("[test_id:2167]should allow and detect inter-VMI MAC collisions for out-of-range MAC", func() {
						outOfRangeMAC := "06:00:00:00:00:00"

						vmi1 := NewVMI(TestNamespace, "test-vmi-out-range-1",
							WithInterface(newInterface(nadName1, outOfRangeMAC)),
							WithNetwork(newNetwork(nadName1)))
						Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed(), "First VMI should be created successfully")

						vmi2 := NewVMI(TestNamespace, "test-vmi-out-range-2",
							WithInterface(newInterface(nadName2, outOfRangeMAC)),
							WithNetwork(newNetwork(nadName2)))
						Expect(testClient.CRClient.Create(context.TODO(), vmi2)).To(Succeed(), "Second VMI with duplicate MAC should be created successfully")

						// Check for collision events on both VMIs
						normalizedMAC, err := net.ParseMAC(outOfRangeMAC)
						Expect(err).ToNot(HaveOccurred(), "Should parse out-of-range MAC")

						conflictingVMIs := []vmiReference{
							{vmi1.Namespace, vmi1.Name},
							{vmi2.Namespace, vmi2.Name},
						}

						waitForVMIsRunning(conflictingVMIs)
						expectMACCollisionEvents(normalizedMAC.String(), conflictingVMIs)
					})
				})
			})

			Context("and when restarting kubeMacPool and trying to create a VMI with the same manually configured MAC as an older VMI", func() {
				var nadName1, nadName2 string

				BeforeEach(func() {
					nadName1 = randName("br1")
					nadName2 = randName("br2")
					By(fmt.Sprintf("Creating network attachment definitions for restart test: %s, %s", nadName1, nadName2))
					Expect(createNetworkAttachmentDefinition(TestNamespace, nadName1)).To(Succeed())
					Expect(createNetworkAttachmentDefinition(TestNamespace, nadName2)).To(Succeed())
				})
				AfterEach(func() {
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName1)).To(Succeed())
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName2)).To(Succeed())
				})
				It("[test_id:2179]should detect collision after kubeMacPool restart", func() {
					vmi1 := NewVMI(TestNamespace, "test-vmi-restart-1",
						WithInterface(newInterface(nadName1, testMacAddress)),
						WithNetwork(newNetwork(nadName1)))
					Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed())

					waitForVMIsRunning([]vmiReference{{vmi1.Namespace, vmi1.Name}})

					// restart kubeMacPool
					Expect(initKubemacpoolParams()).To(Succeed())

					vmi2 := NewVMI(TestNamespace, "test-vmi-restart-2",
						WithInterface(newInterface(nadName2, testMacAddress)),
						WithNetwork(newNetwork(nadName2)))
					Expect(testClient.CRClient.Create(context.TODO(), vmi2)).To(Succeed())

					waitForVMIsRunning([]vmiReference{{vmi2.Namespace, vmi2.Name}})

					normalizedMAC, err := net.ParseMAC(testMacAddress)
					Expect(err).ToNot(HaveOccurred(), "Should parse test MAC")
					conflictingVMIs := []vmiReference{
						{vmi1.Namespace, vmi1.Name},
						{vmi2.Namespace, vmi2.Name},
					}

					expectMACCollisionEvents(normalizedMAC.String(), conflictingVMIs)
				})
			})

			Context("When running in opt-in mode", Label("vmi-opt-in"), func() {
				BeforeEach(func() {
					By("setting vm webhook to not accept all namespaces unless they include opt-in label")
					err := setVMWebhookOptMode(optInMode)
					Expect(err).ToNot(HaveOccurred(), "should set opt-mode to mutatingwebhookconfiguration")
				})

				Context("VMI collision detection in opted-in namespace", func() {
					var allocatedMac string
					notManagedNamespace := TestNamespace
					managedNamespace := OtherTestNamespace

					Context("and both VMIs are created in an opted-in namespace", func() {
						var nadName string

						BeforeEach(func() {
							optInNamespaceForVMs(managedNamespace)
							allocatedMac = "02:00:00:11:22:33"
							nadName = randName("br")
							Expect(createNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
						})
						AfterEach(func() {
							Expect(deleteNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
						})
						It("should detect collision for VMIs in opted-in namespace with duplicate MAC", func() {
							vmi1 := NewVMI(managedNamespace, "test-vmi-optin-1",
								WithInterface(newInterface(nadName, allocatedMac)),
								WithNetwork(newNetwork(nadName)))
							Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed())

							vmi2 := NewVMI(managedNamespace, "test-vmi-optin-2",
								WithInterface(newInterface(nadName, allocatedMac)),
								WithNetwork(newNetwork(nadName)))
							Expect(testClient.CRClient.Create(context.TODO(), vmi2)).To(Succeed())

							normalizedMAC, err := net.ParseMAC(allocatedMac)
							Expect(err).ToNot(HaveOccurred())

							conflictingVMIs := []vmiReference{
								{vmi1.Namespace, vmi1.Name},
								{vmi2.Namespace, vmi2.Name},
							}

							waitForVMIsRunning(conflictingVMIs)
							expectMACCollisionEvents(normalizedMAC.String(), conflictingVMIs)
						})
					})

					Context("and one VMI is created in an opted-in namespace and the other in a non-opted-in namespace", func() {
						var nadName string

						BeforeEach(func() {
							optInNamespaceForVMs(managedNamespace)
							nadName = randName("br")
							Expect(createNetworkAttachmentDefinition(notManagedNamespace, nadName)).To(Succeed())
							Expect(createNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
						})
						AfterEach(func() {
							Expect(deleteNetworkAttachmentDefinition(notManagedNamespace, nadName)).To(Succeed())
							Expect(deleteNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
						})
						It("should not report collision for VMI in managed vs unmanaged namespace with same MAC", func() {
							const sharedMAC = "02:00:00:77:88:99"

							By("Creating VMI in unmanaged (non-opted-in) namespace")
							vmiUnmanaged := NewVMI(notManagedNamespace, "test-vmi-unmanaged",
								WithInterface(newInterface(nadName, sharedMAC)),
								WithNetwork(newNetwork(nadName)))
							Expect(testClient.CRClient.Create(context.TODO(), vmiUnmanaged)).To(Succeed())

							By("Creating VMI in managed (opted-in) namespace with same MAC")
							vmiManaged := NewVMI(managedNamespace, "test-vmi-managed",
								WithInterface(newInterface(nadName, sharedMAC)),
								WithNetwork(newNetwork(nadName)))
							Expect(testClient.CRClient.Create(context.TODO(), vmiManaged)).To(Succeed())

							nonConflictingVMIs := []vmiReference{
								{vmiManaged.Namespace, vmiManaged.Name},
								{vmiUnmanaged.Namespace, vmiUnmanaged.Name},
							}

							waitForVMIsRunning(nonConflictingVMIs)
							expectNoMACCollisionEvents(nonConflictingVMIs, "unmanaged namespace is ignored")
						})
					})

				})
			})

			Context("When running in opt-out mode", Label("vmi-opt-out"), func() {
				BeforeEach(func() {
					By("setting vm webhook to accept all namespaces unless they include opt-out label")
					Expect(setVMWebhookOptMode(optOutMode)).To(Succeed(), "should set opt-mode to mutatingwebhookconfiguration")
				})

				Context("and VMIs are created in a non-opted-out namespace", func() {
					var nadName string

					BeforeEach(func() {
						nadName = randName("br")
						By(fmt.Sprintf("Creating NAD %s in both namespaces", nadName))
						Expect(createNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
						Expect(createNetworkAttachmentDefinition(OtherTestNamespace, nadName)).To(Succeed())
					})
					AfterEach(func() {
						Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
						Expect(deleteNetworkAttachmentDefinition(OtherTestNamespace, nadName)).To(Succeed())
					})
					It("should detect collision for VMIs with duplicate MAC", func() {
						const sharedMAC = "02:00:00:44:55:66"

						vmi1 := NewVMI(TestNamespace, "test-vmi-optout-1",
							WithInterface(newInterface(nadName, sharedMAC)),
							WithNetwork(newNetwork(nadName)))
						Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed())

						vmi2 := NewVMI(TestNamespace, "test-vmi-optout-2",
							WithInterface(newInterface(nadName, sharedMAC)),
							WithNetwork(newNetwork(nadName)))
						Expect(testClient.CRClient.Create(context.TODO(), vmi2)).To(Succeed())

						normalizedMAC, err := net.ParseMAC(sharedMAC)
						Expect(err).ToNot(HaveOccurred())

						conflictingVMIs := []vmiReference{
							{vmi1.Namespace, vmi1.Name},
							{vmi2.Namespace, vmi2.Name},
						}

						waitForVMIsRunning(conflictingVMIs)
						expectMACCollisionEvents(normalizedMAC.String(), conflictingVMIs)
					})

					It("should not report collision for VMI in managed vs unmanaged namespace with same MAC", func() {
						const sharedMAC = "02:00:00:88:99:aa"

						By("Opting out OtherTestNamespace to make it unmanaged")
						optOutNamespaceForVMs(OtherTestNamespace)

						By("Creating VMI in unmanaged (opted-out) namespace")
						vmiUnmanaged := NewVMI(OtherTestNamespace, "test-vmi-unmanaged",
							WithInterface(newInterface(nadName, sharedMAC)),
							WithNetwork(newNetwork(nadName)))
						Expect(testClient.CRClient.Create(context.TODO(), vmiUnmanaged)).To(Succeed())

						By("Creating VMI in managed (non-opted-out) namespace with same MAC")
						vmiManaged := NewVMI(TestNamespace, "test-vmi-managed",
							WithInterface(newInterface(nadName, sharedMAC)),
							WithNetwork(newNetwork(nadName)))
						Expect(testClient.CRClient.Create(context.TODO(), vmiManaged)).To(Succeed())

						nonConflictingVMIs := []vmiReference{
							{vmiManaged.Namespace, vmiManaged.Name},
							{vmiUnmanaged.Namespace, vmiUnmanaged.Name},
						}

						waitForVMIsRunning(nonConflictingVMIs)
						expectNoMACCollisionEvents(nonConflictingVMIs, "unmanaged namespace is ignored")
					})
				})

			})

		})
	})

// vmiReference holds namespace and name of a VMI for collision testing
type vmiReference struct {
	namespace string
	name      string
}

// buildExpectedCollisionMessage builds the expected collision event message for given VMIs
func buildExpectedCollisionMessage(mac string, vmis []vmiReference) string {
	vmiRefs := make([]string, len(vmis))
	for i, vmi := range vmis {
		vmiRefs[i] = fmt.Sprintf("%s/%s", vmi.namespace, vmi.name)
	}
	sort.Strings(vmiRefs)
	return fmt.Sprintf("MAC %s: Collision between %s", mac, strings.Join(vmiRefs, ", "))
}

// waitForVMIsRunning waits for all VMIs to reach Running phase
func waitForVMIsRunning(vmis []vmiReference) {
	By("Waiting for VMIs to reach Running phase")
	for _, vmi := range vmis {
		Eventually(func() kubevirtv1.VirtualMachineInstancePhase {
			return getVMIPhase(vmi.namespace, vmi.name)
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(Equal(kubevirtv1.Running),
			"VMI %s/%s should reach Running phase", vmi.namespace, vmi.name)
	}
}

// expectMACCollisionEvents verifies that all VMIs have received the expected MACCollision event
func expectMACCollisionEvents(mac string, vmis []vmiReference) {
	By("checking for collision event on VMIs")
	expectedMessage := buildExpectedCollisionMessage(mac, vmis)

	for _, vmi := range vmis {
		By(fmt.Sprintf("Checking for MACCollision event on VMI %s/%s", vmi.namespace, vmi.name))
		Eventually(func(g Gomega) []v1.Event {
			events, err := getVMIEvents(vmi.namespace, vmi.name)
			g.Expect(err).NotTo(HaveOccurred())
			return events.Items
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(ContainElement(And(
			HaveField("Reason", "MACCollision"),
			HaveField("Message", expectedMessage),
		)), fmt.Sprintf("VMI %s/%s should have MACCollision event", vmi.namespace, vmi.name))
	}
}

// expectNoMACCollisionEvents verifies that VMIs do NOT have MACCollision events
func expectNoMACCollisionEvents(vmis []vmiReference, reason string) {
	By(fmt.Sprintf("checking that VMIs do NOT have collision events: %s", reason))
	const consistentlyTimeout = 5 * time.Second
	for _, vmi := range vmis {
		By(fmt.Sprintf("Checking that VMI %s/%s does NOT have MACCollision event", vmi.namespace, vmi.name))
		Consistently(func(g Gomega) []v1.Event {
			events, err := getVMIEvents(vmi.namespace, vmi.name)
			g.Expect(err).NotTo(HaveOccurred())
			return events.Items
		}).WithTimeout(consistentlyTimeout).WithPolling(pollingInterval).ShouldNot(ContainElement(
			HaveField("Reason", "MACCollision"),
		), fmt.Sprintf("VMI %s/%s should NOT have MACCollision event: %s", vmi.namespace, vmi.name, reason))
	}
}
