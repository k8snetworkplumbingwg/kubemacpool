package tests

import (
	"context"
	"fmt"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cross-Type MAC Collision Detection",
	Label(PodMACCollisionDetectionLabel), Label(MACCollisionDetectionLabel), Ordered, func() {
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

		BeforeEach(func() {
			By("Opting in test namespace for pod MAC allocation")
			err := addLabelsToNamespace(TestNamespace, map[string]string{podNamespaceOptInLabel: "allocate"})
			Expect(err).ToNot(HaveOccurred(), "should be able to add pod opt-in label")
		})

		AfterEach(func() {
			cleanupTestPodsInNamespaces(testNamespaces)

			vmiList := &kubevirtv1.VirtualMachineInstanceList{}
			Expect(testClient.CRClient.List(context.TODO(), vmiList)).To(Succeed())
			for i := range vmiList.Items {
				err := testClient.CRClient.Delete(context.TODO(), &vmiList.Items[i])
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}
			}
			Eventually(func() []kubevirtv1.VirtualMachineInstance {
				list := &kubevirtv1.VirtualMachineInstanceList{}
				Expect(testClient.CRClient.List(context.TODO(), list)).To(Succeed())
				return list.Items
			}).WithTimeout(timeout).WithPolling(pollingInterval).Should(HaveLen(0), "failed to remove all VMI objects")
		})

		Context("and a Pod and VMI share the same MAC address", func() {
			var nadName string

			BeforeEach(func() {
				nadName = randName("br")
				By(fmt.Sprintf("Creating network attachment definition: %s", nadName))
				Expect(createNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
			})

			AfterEach(func() {
				Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
			})

			It("should detect collision between Pod and VMI with same MAC", Label(MetricsLabel), func() {
				const sharedMAC = "02:00:00:00:cc:01"

				pod1 := NewCollisionPod(TestNamespace, "test-cross-pod",
					WithMultusNetwork(nadName, sharedMAC))
				_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred(), "Pod should be created successfully")

				vmi1 := NewVMI(TestNamespace, "test-cross-vmi",
					WithInterface(newInterface(nadName, sharedMAC)),
					WithNetwork(newNetwork(nadName)))
				Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed(), "VMI should be created successfully")

				normalizedMAC, err := net.ParseMAC(sharedMAC)
				Expect(err).ToNot(HaveOccurred())

				pods := []podReference{{pod1.Namespace, pod1.Name}}
				vmis := []vmiReference{{vmi1.Namespace, vmi1.Name}}
				allRefs := []collisionRef{
					podRef(pod1.Namespace, pod1.Name),
					vmiRef(vmi1.Namespace, vmi1.Name),
				}

				waitForPodsRunning(pods)
				waitForVMIsRunning(vmis)

				By("Verifying collision events on Pod")
				expectPodMACCollisionEvents(normalizedMAC.String(), allRefs)

				By("Verifying collision events on VMI")
				expectedVMIMessage := buildCollisionMessage(normalizedMAC.String(), allRefs)
				Eventually(func(g Gomega) []corev1.Event {
					events, evErr := getVMIEvents(vmi1.Namespace, vmi1.Name)
					g.Expect(evErr).NotTo(HaveOccurred())
					return events.Items
				}).WithTimeout(timeout).WithPolling(pollingInterval).Should(ContainElement(And(
					HaveField("Reason", "MACCollision"),
					HaveField("Message", expectedVMIMessage),
				)))

				expectMACCollisionGauge(normalizedMAC.String(), len(allRefs))
			})

			It("should clear Pod collision when colliding VMI is deleted", Label(MetricsLabel), func() {
				const sharedMAC = "02:00:00:00:cc:10"

				pod1 := NewCollisionPod(TestNamespace, "test-clear-pod",
					WithMultusNetwork(nadName, sharedMAC))
				_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				vmi1 := NewVMI(TestNamespace, "test-clear-vmi",
					WithInterface(newInterface(nadName, sharedMAC)),
					WithNetwork(newNetwork(nadName)))
				Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed())

				normalizedMAC, err := net.ParseMAC(sharedMAC)
				Expect(err).ToNot(HaveOccurred())

				waitForPodsRunning([]podReference{{pod1.Namespace, pod1.Name}})
				waitForVMIsRunning([]vmiReference{{vmi1.Namespace, vmi1.Name}})

				By("Verifying collision is detected")
				expectMACCollisionGauge(normalizedMAC.String(), 2)

				By("Deleting the colliding VMI")
				Expect(testClient.CRClient.Delete(context.TODO(), vmi1)).To(Succeed())

				By("Waiting for VMI to be deleted")
				Eventually(func() bool {
					return apierrors.IsNotFound(testClient.CRClient.Get(context.TODO(), client.ObjectKey{
						Namespace: vmi1.Namespace,
						Name:      vmi1.Name,
					}, &kubevirtv1.VirtualMachineInstance{}))
				}).WithTimeout(timeout).WithPolling(pollingInterval).Should(BeTrue())

				By("Verifying collision is cleared")
				expectMACCollisionGauge(normalizedMAC.String(), 0)
			})

			It("should clear VMI collision when colliding Pod is deleted", Label(MetricsLabel), func() {
				const sharedMAC = "02:00:00:00:cc:20"

				pod1 := NewCollisionPod(TestNamespace, "test-clear-pod",
					WithMultusNetwork(nadName, sharedMAC))
				_, err := testClient.K8sClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), pod1, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				vmi1 := NewVMI(TestNamespace, "test-clear-vmi",
					WithInterface(newInterface(nadName, sharedMAC)),
					WithNetwork(newNetwork(nadName)))
				Expect(testClient.CRClient.Create(context.TODO(), vmi1)).To(Succeed())

				normalizedMAC, err := net.ParseMAC(sharedMAC)
				Expect(err).ToNot(HaveOccurred())

				waitForPodsRunning([]podReference{{pod1.Namespace, pod1.Name}})
				waitForVMIsRunning([]vmiReference{{vmi1.Namespace, vmi1.Name}})

				By("Verifying collision is detected")
				expectMACCollisionGauge(normalizedMAC.String(), 2)

				By("Deleting the colliding Pod")
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
		})
	})
