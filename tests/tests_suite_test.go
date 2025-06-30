package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	"github.com/k8snetworkplumbingwg/kubemacpool/tests/reporter"
)

var failureCount int

const artifactDir = "_out/"

func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Test Suite")
}

var _ = BeforeSuite(func() {
	var err error
	testClient, err = NewTestClient()
	Expect(err).ToNot(HaveOccurred())

	Expect(os.RemoveAll(artifactDir)).To(Succeed())
	Expect(os.Mkdir(artifactDir, 0755)).To(Succeed())

	removeTestNamespaces()
	err = createTestNamespaces()
	Expect(err).ToNot(HaveOccurred())

	managerNamespace = findManagerNamespace()
})

var _ = AfterSuite(func() {
	removeTestNamespaces()
})

var _ = JustAfterEach(func() {
	if CurrentSpecReport().Failed() {
		failureCount++
		dumpKubemacpoolLogs(failureCount)
		By(fmt.Sprintf("Test failed, collected logs and artifacts, failure count %d", failureCount))
	}
})

func getPodContainerLogs(podName, containerName string) (string, error) {
	req := testClient.VirtClient.CoreV1().Pods(managerNamespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
	})
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", err
	}
	defer func(podLogs io.ReadCloser) {
		if closeErr := podLogs.Close(); closeErr != nil {
			return
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

func dumpKubemacpoolLogs(failureCount int) {
	if err := logPods(managerNamespace, failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logService(managerNamespace, names.WEBHOOK_SERVICE, failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logEndpoints(managerNamespace, names.WEBHOOK_SERVICE, failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logNetworkPolicies(managerNamespace, failureCount); err != nil {
		fmt.Println(err)
	}
}

func logPodContainersLogs(podName string, containers []corev1.Container, failureCount int) error {
	for i := range containers {
		containerName := containers[i].Name
		podLogs, err := getPodContainerLogs(podName, containerName)
		if err != nil {
			return err
		}

		if err := reporter.LogToFile(fmt.Sprintf("%s_container_%s", podName, containerName), podLogs, artifactDir, failureCount); err != nil {
			return err
		}
	}
	return nil
}

func logService(serviceNamespace, serviceName string, failureCount int) error {
	service, err := testClient.VirtClient.CoreV1().Services(serviceNamespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	byteString, err := json.MarshalIndent(*service, "", "  ")
	if err != nil {
		return err
	}

	err = reporter.LogToFile(fmt.Sprintf("service"+serviceName), string(byteString), artifactDir, failureCount)
	if err != nil {
		return err
	}

	return nil
}

func logEndpoints(endpointNamespace, endpointName string, failureCount int) error {
	endpoint, err := testClient.VirtClient.CoreV1().Endpoints(endpointNamespace).Get(context.TODO(), endpointName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	byteString, err := json.MarshalIndent(*endpoint, "", "  ")
	if err != nil {
		return err
	}

	err = reporter.LogToFile(fmt.Sprintf("endpoint_"+endpointName), string(byteString), artifactDir, failureCount)
	if err != nil {
		return err
	}

	return nil
}

func logPods(podsNamespace string, failureCount int) error {
	var errs []error
	podList, err := testClient.VirtClient.CoreV1().Pods(podsNamespace).List(context.TODO(),
		metav1.ListOptions{LabelSelector: "app=kubemacpool"})
	if err != nil {
		return err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		podYaml, err := testClient.VirtClient.CoreV1().Pods(podsNamespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			errs = append(errs, err)
		}

		byteString, err := json.MarshalIndent(*podYaml, "", "  ")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = reporter.LogToFile(fmt.Sprintf("pod_"+pod.Name), string(byteString), artifactDir, failureCount)
		if err != nil {
			errs = append(errs, err)
		}

		if err := logPodContainersLogs(pod.Name, pod.Spec.Containers, failureCount); err != nil {
			return err
		}
	}
	return nil
}

func logNetworkPolicies(namespace string, failureCount int) error {
	npList, err := testClient.VirtClient.NetworkingV1().NetworkPolicies(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	bytes, err := json.MarshalIndent(*npList, "", "  ")
	if err != nil {
		return err
	}

	err = reporter.LogToFile("network-policies", string(bytes), artifactDir, failureCount)
	if err != nil {
		return err
	}

	return nil
}
