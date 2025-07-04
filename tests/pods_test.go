package tests

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const defaultNumberOfReplicas = 1

var _ = Describe("Pods", func() {
	Context("Check the pod mutating webhook", func() {
		BeforeEach(func() {
			// update namespaces opt in labels before every test
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				err := addLabelsToNamespace(namespace, map[string]string{podNamespaceOptInLabel: "allocate"})
				Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")
			}

			err := initKubemacpoolParams()
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			// Clean pods from our test namespaces after every test to start clean
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				podList, err := testClient.VirtClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).ToNot(HaveOccurred())

				for _, podObject := range podList.Items {
					err = testClient.VirtClient.CoreV1().Pods(namespace).Delete(context.TODO(), podObject.Name, metav1.DeleteOptions{})
					Expect(err).ToNot(HaveOccurred())
				}

				Eventually(func() int {
					podList, listErr := testClient.VirtClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
					Expect(listErr).ToNot(HaveOccurred())
					return len(podList.Items)

				}, timeout, pollingInterval).Should(Equal(0), fmt.Sprintf("failed to remove all pod objects from namespace %s", namespace))

				// This function remove all the labels from the namespace
				err = cleanNamespaceLabels(namespace)
				Expect(err).ToNot(HaveOccurred())
			}

			// Restore the default number of managers
			err := changeManagerReplicas(defaultNumberOfReplicas)
			Expect(err).ToNot(HaveOccurred())
		})

		testCriticalNamespace := func(namespace string, namespaceLabelMap map[string]string, matcher types.GomegaMatcher) {
			err := changeManagerReplicas(0)
			Expect(err).ToNot(HaveOccurred())

			err = addLabelsToNamespace(namespace, namespaceLabelMap)
			Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.VirtClient.CoreV1().Pods(namespace).Create(context.TODO(), podObject, metav1.CreateOptions{})
				return err == nil
			}, timeout, pollingInterval).Should(matcher, "failed to apply the new pod object")
		}

		It("should create a pod when mac pool is running in a regular opted-in namespace", func() {
			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.VirtClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), podObject, metav1.CreateOptions{})
				return err == nil
			}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new pod object")
		})

		It("should create a pod when mac pool is running in a regular opted-out namespace (disabled label)", func() {
			By("updating the namespace opt-in label to disabled")
			err := cleanNamespaceLabels(TestNamespace)
			Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")
			err = addLabelsToNamespace(TestNamespace, map[string]string{podNamespaceOptInLabel: "disable"})
			Expect(err).ToNot(HaveOccurred(), "should be able to add the namespace labels")

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.VirtClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), podObject, metav1.CreateOptions{})
				return err == nil
			}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new pod object")
		})

		It("should create a pod when mac pool is running in a regular opted-out namespace (no label)", func() {
			By("removing the namespace opt-in label")
			err := cleanNamespaceLabels(TestNamespace)
			Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.VirtClient.CoreV1().Pods(TestNamespace).Create(context.TODO(), podObject, metav1.CreateOptions{})
				return err == nil
			}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new pod object")
		})

		It("should fail to create a pod on a regular opted-in namespace when mac pool is down", func() {
			testCriticalNamespace(OtherTestNamespace, map[string]string{"not-critical": "0"}, BeFalse())
		})

		It("should create a pod on a critical k8s namespaces when mac pool is down", func() {
			testCriticalNamespace(OtherTestNamespace, map[string]string{names.K8S_RUNLABEL: "0"}, BeTrue())
		})

		It("should create a pod on a critical openshift namespaces when mac pool is down", func() {
			testCriticalNamespace(OtherTestNamespace, map[string]string{names.OPENSHIFT_RUNLABEL: "0"}, BeTrue())
		})
	})
})
