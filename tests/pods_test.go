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
		AfterEach(func() {
			// Clean pods from our test namespaces after every test to start clean
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				podList := &corev1.PodList{}
				err := testClient.VirtClient.List(context.TODO(), &client.ListOptions{Namespace: namespace}, podList)
				Expect(err).ToNot(HaveOccurred())

				for _, podObject := range podList.Items {
					err = testClient.VirtClient.Delete(context.TODO(), &podObject)
					Expect(err).ToNot(HaveOccurred())
				}

				Eventually(func() int {
					podList := &corev1.PodList{}
					err := testClient.VirtClient.List(context.TODO(), &client.ListOptions{Namespace: namespace}, podList)
					Expect(err).ToNot(HaveOccurred())
					return len(podList.Items)

				}, timeout, pollingInterval).Should(Equal(0), fmt.Sprintf("failed to remove all pod objects from namespace %s", namespace))

				// This function remove all the labels from the namespace
				err = cleanNamespaceLabels(namespace)
			}

			// Restore the default number of managers
			err := changeManagerReplicas(defaultNumberOfReplicas)
			Expect(err).ToNot(HaveOccurred())
		})

		testCriticalNamespace := func(namespace, label string, matcher types.GomegaMatcher) {
			err := changeManagerReplicas(0)
			Expect(err).ToNot(HaveOccurred())

			err = addLabelsToNamespace(OtherTestNamespace, map[string]string{label: "0"})

			podObject := createPodObject()

			Eventually(func() bool {
				_, err := testClient.KubeClient.CoreV1().Pods(OtherTestNamespace).Create(podObject)
				if err != nil && strings.Contains(err.Error(), "connection refused") {
					return false
				}

				return true
			}, timeout, pollingInterval).Should(matcher, "failed to apply the new pod object")
		}

		It("should create a pod when mac pool is running in a regular namespace", func() {
			err := setRange(rangeStart, rangeEnd)
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

		It("should fail to create a pod on a regular namespace when mac pool is down", func() {
			testCriticalNamespace(OtherTestNamespace, "not-critical", BeFalse())
		})

		It("should create a pod on a critical k8s namespaces when mac pool is down", func() {
			testCriticalNamespace(OtherTestNamespace, names.K8S_RUNLABEL, BeTrue())
		})

		It("should create a pod on a critical openshift namespaces when mac pool is down", func() {
			testCriticalNamespace(OtherTestNamespace, names.OPENSHIFT_RUNLABEL, BeTrue())
		})
	})
})
