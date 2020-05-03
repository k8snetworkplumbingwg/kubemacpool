package tests

import (
	"context"
	"fmt"
	"github.com/onsi/gomega/types"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const defaultNumberOfReplicas = 2

var _ = Describe("Pods", func() {
	Context("Check the pod mutating webhook", func() {
		BeforeEach(func() {
			// update namespaces opt in labels before every test
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				err := addLabelsToNamespace(namespace, map[string]string{names.NAMESPACE_OPT_IN_LABEL_PODS: "allocateForAll"})
				Expect(err).ToNot(HaveOccurred())
			}
		})

		AfterEach(func() {
			// Clean pods from our test namespaces after every test to start clean
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				podList := &corev1.PodList{}
				err := testClient.VirtClient.List(context.TODO(), podList, &client.ListOptions{Namespace: namespace})
				Expect(err).ToNot(HaveOccurred())

				for _, podObject := range podList.Items {
					err = testClient.VirtClient.Delete(context.TODO(), &podObject)
					Expect(err).ToNot(HaveOccurred())
				}

				Eventually(func() int {
					podList := &corev1.PodList{}
					err := testClient.VirtClient.List(context.TODO(), podList, &client.ListOptions{Namespace: namespace})
					Expect(err).ToNot(HaveOccurred())
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

			err = addLabelsToNamespace(OtherTestNamespace, namespaceLabelMap)
			Expect(err).ToNot(HaveOccurred())

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.KubeClient.CoreV1().Pods(OtherTestNamespace).Create(podObject)
				if err != nil && strings.Contains(err.Error(), "connection refused") {
					return false
				}

				return true
			}, timeout, pollingInterval).Should(matcher, "failed to apply the new pod object")
		}

		It("should create a pod when mac pool is running in a regular opted-in namespace", func() {
			err := initKubemacpoolParams(rangeStart, rangeEnd)
			Expect(err).ToNot(HaveOccurred())

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.KubeClient.CoreV1().Pods(TestNamespace).Create(podObject)
				if err != nil && strings.Contains(err.Error(), "connection refused") {
					return false
				}

				return true
			}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new pod object")
		})

		It("should create a pod when mac pool is running in a regular opted-out namespace (disabled label)", func() {
			err := initKubemacpoolParams(rangeStart, rangeEnd)
			Expect(err).ToNot(HaveOccurred())

			By("updating the namespace opt-in label to disabled")
			err = cleanNamespaceLabels(TestNamespace)
			Expect(err).ToNot(HaveOccurred())
			err = addLabelsToNamespace(TestNamespace, map[string]string{names.NAMESPACE_OPT_IN_LABEL_PODS: "disable"})
			Expect(err).ToNot(HaveOccurred())

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.KubeClient.CoreV1().Pods(TestNamespace).Create(podObject)
				if err != nil && strings.Contains(err.Error(), "connection refused") {
					return false
				}

				return true
			}, timeout, pollingInterval).Should(BeTrue(), "failed to apply the new pod object")
		})

		It("should create a pod when mac pool is running in a regular opted-out namespace (no label)", func() {
			err := initKubemacpoolParams(rangeStart, rangeEnd)
			Expect(err).ToNot(HaveOccurred())

			By("removing the namespace opt-in label")
			err = cleanNamespaceLabels(TestNamespace)
			Expect(err).ToNot(HaveOccurred())

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.KubeClient.CoreV1().Pods(TestNamespace).Create(podObject)
				if err != nil && strings.Contains(err.Error(), "connection refused") {
					return false
				}

				return true
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
