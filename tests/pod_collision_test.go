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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const PodMACCollisionDetectionLabel = "pod-mac-collision-detection"

var _ = Describe("Pod MAC Collision Detection",
	Label(PodMACCollisionDetectionLabel), Ordered, func() {
		testNamespaces := []string{TestNamespace, OtherTestNamespace}

		BeforeEach(func() {
			By("Verify that there are no test Pods left from previous tests")
			for _, ns := range testNamespaces {
				podList, err := testClient.K8sClient.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
				Expect(err).ToNot(HaveOccurred(), "Should successfully list Pods")
				Expect(len(podList.Items)).To(BeZero(), "There should be no Pods in namespace %s before a test", ns)

				err = cleanNamespaceLabels(ns)
				Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")
			}
		})

		Context("Pod-Pod Collision Detection", func() {
			BeforeEach(func() {
				By("Opting in test namespace for pod MAC allocation")
				err := addLabelsToNamespace(TestNamespace, map[string]string{podNamespaceOptInLabel: "allocate"})
				Expect(err).ToNot(HaveOccurred(), "should be able to add pod opt-in label")
			})

			AfterEach(func() {
				cleanupTestPodsInNamespaces(testNamespaces)
			})

			Context("and the client tries to assign the same MAC address for two different Pods", func() {
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
					DescribeTable("should detect inter-Pod MAC collisions", Label(MetricsLabel), func(separator string) {
						const sharedMAC = "02:00:00:00:bb:01"
						macWithSeparator := strings.Replace(sharedMAC, ":", separator, 5)

						pod1 := NewCollisionPod(TestNamespace, "test-pod-1",
							WithMultusNetwork(nadName1, sharedMAC))
						_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "First Pod should be created successfully")

						pod2 := NewCollisionPod(TestNamespace, "test-pod-2",
							WithMultusNetwork(nadName2, macWithSeparator))
						_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "Second Pod with duplicate MAC should be created successfully")

						normalizedMAC, err := net.ParseMAC(sharedMAC)
						Expect(err).ToNot(HaveOccurred(), "Should parse shared MAC")

						pods := []podReference{
							{pod1.Namespace, pod1.Name},
							{pod2.Namespace, pod2.Name},
						}
						refs := []collisionRef{
							podRef(pod1.Namespace, pod1.Name),
							podRef(pod2.Namespace, pod2.Name),
						}

						waitForPodsRunning(pods)
						expectPodMACCollisionEvents(normalizedMAC.String(), refs)
						expectMACCollisionGauge(normalizedMAC.String(), len(refs))
					},
						Entry("with the same mac format", ":"),
						Entry("with different mac format", "-"),
					)
				})

				Context("and the MAC address is out of range", func() {
					It("should detect inter-Pod MAC collisions for out-of-range MAC", Label(MetricsLabel), func() {
						outOfRangeMAC := "06:00:00:00:bb:00"

						pod1 := NewCollisionPod(TestNamespace, "test-pod-out-range-1",
							WithMultusNetwork(nadName1, outOfRangeMAC))
						_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "First Pod should be created successfully")

						pod2 := NewCollisionPod(TestNamespace, "test-pod-out-range-2",
							WithMultusNetwork(nadName2, outOfRangeMAC))
						_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "Second Pod with duplicate MAC should be created successfully")

						normalizedMAC, err := net.ParseMAC(outOfRangeMAC)
						Expect(err).ToNot(HaveOccurred(), "Should parse out-of-range MAC")

						pods := []podReference{
							{pod1.Namespace, pod1.Name},
							{pod2.Namespace, pod2.Name},
						}
						refs := []collisionRef{
							podRef(pod1.Namespace, pod1.Name),
							podRef(pod2.Namespace, pod2.Name),
						}

						waitForPodsRunning(pods)
						expectPodMACCollisionEvents(normalizedMAC.String(), refs)
						expectMACCollisionGauge(normalizedMAC.String(), len(refs))
					})

					It("should detect collision for 3+ Pods with same MAC", Label(MetricsLabel), func() {
						const sharedMAC = "02:00:00:00:bb:10"

						pod1 := NewCollisionPod(TestNamespace, "test-pod-multi-1",
							WithMultusNetwork(nadName1, sharedMAC))
						_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "First Pod should be created successfully")

						pod2 := NewCollisionPod(TestNamespace, "test-pod-multi-2",
							WithMultusNetwork(nadName2, sharedMAC))
						_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "Second Pod should be created successfully")

						pod3 := NewCollisionPod(TestNamespace, "test-pod-multi-3",
							WithMultusNetwork(nadName1, sharedMAC))
						_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod3, metav1.CreateOptions{})
						Expect(err).ToNot(HaveOccurred(), "Third Pod should be created successfully")

						normalizedMAC, err := net.ParseMAC(sharedMAC)
						Expect(err).ToNot(HaveOccurred(), "Should parse shared MAC")

						pods := []podReference{
							{pod1.Namespace, pod1.Name},
							{pod2.Namespace, pod2.Name},
							{pod3.Namespace, pod3.Name},
						}
						refs := []collisionRef{
							podRef(pod1.Namespace, pod1.Name),
							podRef(pod2.Namespace, pod2.Name),
							podRef(pod3.Namespace, pod3.Name),
						}

						waitForPodsRunning(pods)
						expectPodMACCollisionEvents(normalizedMAC.String(), refs)
						expectMACCollisionGauge(normalizedMAC.String(), len(refs))
					})
				})
			})

			Context("and Pods have multiple interfaces with partial MAC collisions", func() {
				var net1Name, net2Name, net3Name string

				BeforeEach(func() {
					net1Name = randName("net1")
					net2Name = randName("net2")
					net3Name = randName("net3")
					By(fmt.Sprintf("Creating network attachment definitions: %s, %s, %s", net1Name, net2Name, net3Name))
					Expect(createNetworkAttachmentDefinition(TestNamespace, net1Name)).To(Succeed())
					Expect(createNetworkAttachmentDefinition(TestNamespace, net2Name)).To(Succeed())
					Expect(createNetworkAttachmentDefinition(TestNamespace, net3Name)).To(Succeed())
				})

				AfterEach(func() {
					By("Deleting network attachment definitions")
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, net1Name)).To(Succeed())
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, net2Name)).To(Succeed())
					Expect(deleteNetworkAttachmentDefinition(TestNamespace, net3Name)).To(Succeed())
				})

				It("should detect collision only for the colliding MAC when Pod has multiple interfaces", Label(MetricsLabel), func() {
					const (
						collidingMAC = "02:00:00:00:bb:20"
						uniqueMAC1   = "02:00:00:00:bb:21"
						uniqueMAC2   = "02:00:00:00:bb:22"
					)

					pod1 := NewCollisionPod(TestNamespace, "test-pod-partial-1",
						WithMultusNetwork(net1Name, collidingMAC),
						WithMultusNetwork(net2Name, uniqueMAC1))
					_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod2 := NewCollisionPod(TestNamespace, "test-pod-partial-2",
						WithMultusNetwork(net1Name, collidingMAC),
						WithMultusNetwork(net3Name, uniqueMAC2))
					_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pods := []podReference{
						{pod1.Namespace, pod1.Name},
						{pod2.Namespace, pod2.Name},
					}
					refs := []collisionRef{
						podRef(pod1.Namespace, pod1.Name),
						podRef(pod2.Namespace, pod2.Name),
					}

					waitForPodsRunning(pods)

					By("Verifying collision event only for the colliding MAC")
					normalizedCollidingMAC, err := net.ParseMAC(collidingMAC)
					Expect(err).ToNot(HaveOccurred())
					normalizedUniqueMAC2, err := net.ParseMAC(uniqueMAC2)
					Expect(err).ToNot(HaveOccurred())

					expectPodMACCollisionEvents(normalizedCollidingMAC.String(), refs)
					expectMACCollisionGauge(normalizedCollidingMAC.String(), len(refs))
					expectNoMACCollisionGaugeConsistently(normalizedUniqueMAC2.String())
				})

				It("should detect multiple collisions on same Pod when it has multiple colliding MACs", Label(MetricsLabel), func() {
					const (
						macA = "02:00:00:00:bb:30"
						macB = "02:00:00:00:bb:31"
					)

					pod1 := NewCollisionPod(TestNamespace, "test-pod-double-1",
						WithMultusNetwork(net1Name, macA),
						WithMultusNetwork(net2Name, macB))
					_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod2 := NewCollisionPod(TestNamespace, "test-pod-double-2",
						WithMultusNetwork(net1Name, macA))
					_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod3 := NewCollisionPod(TestNamespace, "test-pod-double-3",
						WithMultusNetwork(net2Name, macB))
					_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod3, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					waitForPodsRunning([]podReference{
						{pod1.Namespace, pod1.Name},
						{pod2.Namespace, pod2.Name},
						{pod3.Namespace, pod3.Name},
					})

					By("Verifying collision events for macA")
					normalizedMacA, err := net.ParseMAC(macA)
					Expect(err).ToNot(HaveOccurred())
					expectPodMACCollisionEvents(normalizedMacA.String(),
						[]collisionRef{
							podRef(pod1.Namespace, pod1.Name),
							podRef(pod2.Namespace, pod2.Name),
						})
					expectMACCollisionGauge(normalizedMacA.String(), 2)

					By("Verifying collision events for macB")
					normalizedMacB, err := net.ParseMAC(macB)
					Expect(err).ToNot(HaveOccurred())
					expectPodMACCollisionEvents(normalizedMacB.String(),
						[]collisionRef{
							podRef(pod1.Namespace, pod1.Name),
							podRef(pod3.Namespace, pod3.Name),
						})
					expectMACCollisionGauge(normalizedMacB.String(), 2)
				})

				It("should clear collision when colliding Pod is deleted", Label(MetricsLabel), func() {
					const sharedMAC = "02:00:00:00:bb:40"

					pod1 := NewCollisionPod(TestNamespace, "test-pod-delete-1",
						WithMultusNetwork(net1Name, sharedMAC))
					_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod2 := NewCollisionPod(TestNamespace, "test-pod-delete-2",
						WithMultusNetwork(net2Name, sharedMAC))
					_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					normalizedMAC, err := net.ParseMAC(sharedMAC)
					Expect(err).ToNot(HaveOccurred())

					waitForPodsRunning([]podReference{
						{pod1.Namespace, pod1.Name},
						{pod2.Namespace, pod2.Name},
					})

					By("Verifying collision is detected")
					expectMACCollisionGauge(normalizedMAC.String(), 2)

					By("Deleting one of the colliding Pods")
					err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Delete(context.TODO(), pod1.Name, metav1.DeleteOptions{})
					Expect(err).ToNot(HaveOccurred())

					By("Waiting for Pod to be deleted")
					Eventually(func() bool {
						_, getErr := testClient.K8sClient.CoreV1().Pods(pod1.Namespace).Get(context.TODO(), pod1.Name, metav1.GetOptions{})
						return apierrors.IsNotFound(getErr)
					}).WithTimeout(timeout).WithPolling(pollingInterval).Should(BeTrue())

					By("Verifying collision is cleared")
					expectMACCollisionGauge(normalizedMAC.String(), 0)
				})

				It("should clear only specific collision when one of multiple colliding Pods is deleted", Label(MetricsLabel), func() {
					const (
						macA = "02:00:00:00:bb:50"
						macB = "02:00:00:00:bb:51"
					)

					pod1 := NewCollisionPod(TestNamespace, "test-pod-multi-collision-1",
						WithMultusNetwork(net1Name, macA),
						WithMultusNetwork(net2Name, macB))
					_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod2 := NewCollisionPod(TestNamespace, "test-pod-multi-collision-2",
						WithMultusNetwork(net1Name, macA))
					_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod3 := NewCollisionPod(TestNamespace, "test-pod-multi-collision-3",
						WithMultusNetwork(net2Name, macB))
					_, err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod3, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					normalizedMacA, err := net.ParseMAC(macA)
					Expect(err).ToNot(HaveOccurred())
					normalizedMacB, err := net.ParseMAC(macB)
					Expect(err).ToNot(HaveOccurred())

					waitForPodsRunning([]podReference{
						{pod1.Namespace, pod1.Name},
						{pod2.Namespace, pod2.Name},
						{pod3.Namespace, pod3.Name},
					})

					By("Verifying both collisions are detected")
					expectMACCollisionGauge(normalizedMacA.String(), 2)
					expectMACCollisionGauge(normalizedMacB.String(), 2)

					By("Deleting pod2 (involved only in macA collision)")
					err = testClient.K8sClient.CoreV1().Pods(TestNamespace).Delete(context.TODO(), pod2.Name, metav1.DeleteOptions{})
					Expect(err).ToNot(HaveOccurred())

					By("Waiting for pod2 to be deleted")
					Eventually(func() bool {
						_, getErr := testClient.K8sClient.CoreV1().Pods(pod2.Namespace).Get(context.TODO(), pod2.Name, metav1.GetOptions{})
						return apierrors.IsNotFound(getErr)
					}).WithTimeout(timeout).WithPolling(pollingInterval).Should(BeTrue())

					By("Verifying macA collision is cleared but macB collision remains")
					expectMACCollisionGauge(normalizedMacA.String(), 0)
					expectMACCollisionGauge(normalizedMacB.String(), 2)
				})
			})
		})

		Context("Pod collision detection and namespace management", Label("pod-opt-in"), func() {
			managedNamespace := OtherTestNamespace
			notManagedNamespace := TestNamespace

			BeforeEach(func() {
				By("Setting pod webhook to opt-in mode")
				Expect(setPodWebhookOptMode(optInMode)).To(Succeed(),
					"should set pod opt-mode to opt-in in mutatingwebhookconfiguration")
			})

			AfterEach(func() {
				cleanupTestPodsInNamespaces(testNamespaces)

				By("Restoring pod webhook to opt-out mode")
				Expect(setPodWebhookOptMode(optOutMode)).To(Succeed(),
					"should restore pod opt-mode to opt-out in mutatingwebhookconfiguration")
			})

			Context("and both Pods are created in an opted-in namespace", func() {
				var nadName string

				BeforeEach(func() {
					optInNamespaceForPods(managedNamespace)

					nadName = randName("br")
					Expect(createNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
				})
				AfterEach(func() {
					Expect(deleteNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
				})

				It("should detect collision for Pods in opted-in namespace with duplicate MAC", Label(MetricsLabel), func() {
					const sharedMAC = "02:00:00:00:bb:60"

					pod1 := NewCollisionPod(managedNamespace, "test-pod-optin-1",
						WithMultusNetwork(nadName, sharedMAC))
					_, err := testClient.K8sClient.CoreV1().Pods(managedNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					pod2 := NewCollisionPod(managedNamespace, "test-pod-optin-2",
						WithMultusNetwork(nadName, sharedMAC))
					_, err = testClient.K8sClient.CoreV1().Pods(managedNamespace).Create(context.TODO(), pod2, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					normalizedMAC, err := net.ParseMAC(sharedMAC)
					Expect(err).ToNot(HaveOccurred())

					pods := []podReference{
						{pod1.Namespace, pod1.Name},
						{pod2.Namespace, pod2.Name},
					}
					refs := []collisionRef{
						podRef(pod1.Namespace, pod1.Name),
						podRef(pod2.Namespace, pod2.Name),
					}

					waitForPodsRunning(pods)
					expectPodMACCollisionEvents(normalizedMAC.String(), refs)
					expectMACCollisionGauge(normalizedMAC.String(), len(refs))
				})
			})

			Context("and one Pod is in an opted-in namespace and the other in a non-opted-in namespace", func() {
				var nadName string

				BeforeEach(func() {
					optInNamespaceForPods(managedNamespace)

					nadName = randName("br")
					Expect(createNetworkAttachmentDefinition(notManagedNamespace, nadName)).To(Succeed())
					Expect(createNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
				})
				AfterEach(func() {
					Expect(deleteNetworkAttachmentDefinition(notManagedNamespace, nadName)).To(Succeed())
					Expect(deleteNetworkAttachmentDefinition(managedNamespace, nadName)).To(Succeed())
				})

				It("should not report collision for Pod in managed vs unmanaged namespace with same MAC", Label(MetricsLabel), func() {
					const sharedMAC = "02:00:00:00:bb:70"

					By("Creating Pod in unmanaged (non-opted-in) namespace")
					podUnmanaged := NewCollisionPod(notManagedNamespace, "test-pod-unmanaged",
						WithMultusNetwork(nadName, sharedMAC))
					_, err := testClient.K8sClient.CoreV1().Pods(notManagedNamespace).Create(context.TODO(), podUnmanaged, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					By("Creating Pod in managed (opted-in) namespace with same MAC")
					podManaged := NewCollisionPod(managedNamespace, "test-pod-managed",
						WithMultusNetwork(nadName, sharedMAC))
					_, err = testClient.K8sClient.CoreV1().Pods(managedNamespace).Create(context.TODO(), podManaged, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					allPods := []podReference{
						{podManaged.Namespace, podManaged.Name},
						{podUnmanaged.Namespace, podUnmanaged.Name},
					}

					waitForPodsRunning(allPods)
					expectNoPodMACCollisionEvents(allPods, "unmanaged namespace is ignored")
					expectNoMACCollisionGaugeConsistently(sharedMAC)
				})
			})
		})

	})

type podReference struct {
	namespace string
	name      string
}

type collisionRef struct {
	objectType string
	namespace  string
	name       string
}

func podRef(namespace, name string) collisionRef {
	return collisionRef{objectType: "pod", namespace: namespace, name: name}
}

func vmiRef(namespace, name string) collisionRef {
	return collisionRef{objectType: "vmi", namespace: namespace, name: name}
}

func buildCollisionMessage(mac string, refs []collisionRef) string {
	keys := make([]string, len(refs))
	for i, ref := range refs {
		keys[i] = fmt.Sprintf("%s/%s/%s", ref.objectType, ref.namespace, ref.name)
	}
	sort.Strings(keys)
	return fmt.Sprintf("MAC %s: Collision between %s", mac, strings.Join(keys, ", "))
}

func waitForPodsRunning(pods []podReference) {
	By("Waiting for Pods to reach Running phase")
	for _, pod := range pods {
		Eventually(func() corev1.PodPhase {
			p, err := testClient.K8sClient.CoreV1().Pods(pod.namespace).Get(context.TODO(), pod.name, metav1.GetOptions{})
			if err != nil {
				return ""
			}
			return p.Status.Phase
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(Equal(corev1.PodRunning),
			"Pod %s/%s should reach Running phase", pod.namespace, pod.name)
	}
}

// expectPodMACCollisionEvents checks collision events on all Pod refs in allRefs.
// Non-Pod refs are only used to construct the expected collision message.
func expectPodMACCollisionEvents(mac string, allRefs []collisionRef) {
	By("checking for collision event on Pods")
	expectedMessage := buildCollisionMessage(mac, allRefs)

	for _, ref := range allRefs {
		if ref.objectType != "pod" {
			continue
		}

		By(fmt.Sprintf("Checking for MACCollision event on Pod %s/%s", ref.namespace, ref.name))
		Eventually(func(g Gomega) []corev1.Event {
			events, err := getPodEvents(ref.namespace, ref.name)
			g.Expect(err).NotTo(HaveOccurred())
			return events.Items
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(ContainElement(And(
			HaveField("Reason", "MACCollision"),
			HaveField("Message", expectedMessage),
		)), fmt.Sprintf("Pod %s/%s should have MACCollision event", ref.namespace, ref.name))
	}
}

func expectNoPodMACCollisionEvents(pods []podReference, reason string) {
	By(fmt.Sprintf("checking that Pods do NOT have collision events: %s", reason))
	const consistentlyTimeout = 5 * time.Second
	for _, pod := range pods {
		By(fmt.Sprintf("Checking that Pod %s/%s does NOT have MACCollision event", pod.namespace, pod.name))
		Consistently(func(g Gomega) []corev1.Event {
			events, err := getPodEvents(pod.namespace, pod.name)
			g.Expect(err).NotTo(HaveOccurred())
			return events.Items
		}).WithTimeout(consistentlyTimeout).WithPolling(pollingInterval).ShouldNot(ContainElement(
			HaveField("Reason", "MACCollision"),
		), fmt.Sprintf("Pod %s/%s should NOT have MACCollision event: %s", pod.namespace, pod.name, reason))
	}
}
