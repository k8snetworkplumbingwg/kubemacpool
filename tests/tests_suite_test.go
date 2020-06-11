package tests

import (
	"context"
	"fmt"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
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

	managerNamespace = findManagerNamespace()
})

var _ = AfterSuite(func() {
	removeTestNamespaces()
})

func KubemacPoolFailedFunction(message string, callerSkip ...int) {
	podList, err := testClient.KubeClient.CoreV1().Pods(managerNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	for _, pod := range podList.Items {
		podYaml, err := testClient.KubeClient.CoreV1().Pods(managerNamespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})

		req := testClient.KubeClient.CoreV1().Pods(managerNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
		output, err := req.DoRaw(context.TODO())
		if err != nil {
			fmt.Println(err)
			Fail(message, callerSkip...)
		}

		fmt.Printf("Pod Name: %s \n", pod.Name)
		fmt.Printf("%v \n", *podYaml)
		fmt.Println(string(output))
	}

	service, err := testClient.KubeClient.CoreV1().Services(managerNamespace).Get(context.TODO(), names.WEBHOOK_SERVICE, metav1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	fmt.Printf("Service: %v", service)

	endpoint, err := testClient.KubeClient.CoreV1().Endpoints(managerNamespace).Get(context.TODO(), names.WEBHOOK_SERVICE, metav1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	fmt.Printf("Endpoint: %v", endpoint)

	Fail(message, callerSkip...)
}
