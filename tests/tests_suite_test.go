package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

func TestTests(t *testing.T) {
	RegisterFailHandler(KubemacPoolFailedFunction)
	RunSpecs(t, "E2E Test Suite")
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

func getPodLogs(podName, containerName string) (string, error) {
	req := testClient.VirtClient.CoreV1().Pods(managerNamespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
	})
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", err
	}
	defer func(podLogs io.ReadCloser) {
		err := podLogs.Close()
		if err != nil {
			panic(err)
		}
	}(podLogs)

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	output := buf.String()

	return output, nil
}

func KubemacPoolFailedFunction(message string, callerSkip ...int) {
	podList, err := testClient.VirtClient.CoreV1().Pods(managerNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	for _, pod := range podList.Items {
		podYaml, err := testClient.VirtClient.CoreV1().Pods(managerNamespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			Fail(message, callerSkip...)
		}

		fmt.Printf("\nPod Name: %s \n", pod.Name)
		fmt.Printf("Pod Yaml:\n%v \n", *podYaml)

		for i := range pod.Spec.Containers {
			containerName := pod.Spec.Containers[i].Name
			podLogs, err := getPodLogs(pod.Name, containerName)
			if err != nil {
				fmt.Println(err)
				Fail(message, callerSkip...)
			}

			fmt.Printf("\nPod container %q Logs:\n%s \n", containerName, podLogs)
		}
	}

	service, err := testClient.VirtClient.CoreV1().Services(managerNamespace).Get(context.TODO(), names.WEBHOOK_SERVICE, metav1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	fmt.Printf("Service: %v\n", service)

	endpoint, err := testClient.VirtClient.CoreV1().Endpoints(managerNamespace).Get(context.TODO(), names.WEBHOOK_SERVICE, metav1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		Fail(message, callerSkip...)
	}

	fmt.Printf("Endpoint: %v\n", endpoint)

	Fail(message, callerSkip...)
}
