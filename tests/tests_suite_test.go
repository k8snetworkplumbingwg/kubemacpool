package tests

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(KubemacPoolFailedFunction)
	RunSpecs(t, "Tests Suite")
}

var _ = BeforeSuite(func() {
	var err error
	testClient, err = NewTestClient()
	Expect(err).ToNot(HaveOccurred())

	removeTestNamespaces()
	err = createTestNamespaces()
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	removeTestNamespaces()
})

func KubemacPoolFailedFunction(message string, callerSkip ...int) {
	podList, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).List(metav1.ListOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	for _, pod := range podList.Items {
		req := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).GetLogs(pod.Name, &corev1.PodLogOptions{})
		output, err := req.DoRaw()
		if err != nil {
			fmt.Println(err)
			Fail(message, callerSkip...)
		}

		fmt.Printf("Pod Name: %s \n", pod.Name)
		fmt.Println(string(output))
	}

	Fail(message, callerSkip...)
}
