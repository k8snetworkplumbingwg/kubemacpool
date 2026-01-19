package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	"github.com/k8snetworkplumbingwg/kubemacpool/tests/reporter"
)

var failureCount int

const artifactDir = "_out/"

const kubevirtNamespace = "kubevirt"

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
	crashErr := checkKubemacpoolCrash()
	testFailed := CurrentSpecReport().Failed() || crashErr != nil

	if testFailed {
		failureCount++
		dumpKubemacpoolLogs(failureCount)
		By(fmt.Sprintf("Test failed, collected logs and artifacts, failure count %d", failureCount))
	}

	Expect(crashErr).To(Succeed(), "Kubemacpool should not crash during test")
})

func getPodContainerLogs(podName, containerName string) (string, error) {
	req := testClient.K8sClient.CoreV1().Pods(managerNamespace).GetLogs(podName, &corev1.PodLogOptions{
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

	if err := logConfigMaps(managerNamespace, failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logVirtualMachines(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logVirtualMachineInstances(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logAllPods(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logNetworkAttachmentDefinitions(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logNamespaces(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logVirtualMachineInstanceMigrations(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logKubevirtPods(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logKubevirtConfig(failureCount); err != nil {
		fmt.Println(err)
	}

	if err := logNodes(failureCount); err != nil {
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
	service, err := testClient.K8sClient.CoreV1().Services(serviceNamespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	byteString, err := json.MarshalIndent(*service, "", "  ")
	if err != nil {
		return err
	}

	err = reporter.LogToFile(fmt.Sprintf("service_%s", serviceName), string(byteString), artifactDir, failureCount)
	if err != nil {
		return err
	}

	return nil
}

func logEndpoints(endpointNamespace, endpointName string, failureCount int) error {
	endpoint, err := testClient.K8sClient.CoreV1().Endpoints(endpointNamespace).Get(context.TODO(), endpointName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	byteString, err := json.MarshalIndent(*endpoint, "", "  ")
	if err != nil {
		return err
	}

	err = reporter.LogToFile(fmt.Sprintf("endpoint_%s", endpointName), string(byteString), artifactDir, failureCount)
	if err != nil {
		return err
	}

	return nil
}

func logPods(podsNamespace string, failureCount int) error {
	var errs []error
	podList, err := testClient.K8sClient.CoreV1().Pods(podsNamespace).List(context.TODO(),
		metav1.ListOptions{LabelSelector: "app=kubemacpool"})
	if err != nil {
		return err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		podYaml, err := testClient.K8sClient.CoreV1().Pods(podsNamespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			errs = append(errs, err)
		}

		byteString, err := json.MarshalIndent(*podYaml, "", "  ")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = reporter.LogToFile(fmt.Sprintf("pod_%s", pod.Name), string(byteString), artifactDir, failureCount)
		if err != nil {
			errs = append(errs, err)
		}

		if err := logPodContainersLogs(pod.Name, pod.Spec.Containers, failureCount); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("multiple pod logging errors: %v", errs)
	}
	return nil
}

func logNetworkPolicies(namespace string, failureCount int) error {
	npList, err := testClient.K8sClient.NetworkingV1().NetworkPolicies(namespace).List(context.TODO(), metav1.ListOptions{})
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

func logConfigMaps(namespace string, failureCount int) error {
	var errs []error
	configMapList, err := testClient.K8sClient.CoreV1().ConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	for i := range configMapList.Items {
		configMap := &configMapList.Items[i]
		configMapYaml, err := testClient.K8sClient.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configMap.Name, metav1.GetOptions{})
		if err != nil {
			errs = append(errs, err)
			continue
		}

		bytes, err := json.MarshalIndent(*configMapYaml, "", "  ")
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = reporter.LogToFile(fmt.Sprintf("configmap_%s", configMap.Name), string(bytes), artifactDir, failureCount)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("multiple configmap logging errors: %v", errs)
	}
	return nil
}

func logVirtualMachines(failureCount int) error {
	var errs []error
	vmList := &kubevirtv1.VirtualMachineList{}
	err := testClient.CRClient.List(context.TODO(), vmList)
	if err != nil {
		return err
	}

	for i := range vmList.Items {
		vm := &vmList.Items[i]
		bytes, err := json.MarshalIndent(*vm, "", "  ")
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = reporter.LogToFile(fmt.Sprintf("vm_%s_%s", vm.Namespace, vm.Name), string(bytes), artifactDir, failureCount)
		if err != nil {
			errs = append(errs, err)
		}

		events, err := testClient.K8sClient.CoreV1().Events(vm.Namespace).List(context.TODO(), metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=VirtualMachine", vm.Name),
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}

		eventBytes, err := json.MarshalIndent(*events, "", "  ")
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = reporter.LogToFile(fmt.Sprintf("vm_events_%s_%s", vm.Namespace, vm.Name), string(eventBytes), artifactDir, failureCount)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("multiple VM logging errors: %v", errs)
	}
	return nil
}

func logVirtualMachineInstances(failureCount int) error {
	vmiList := &kubevirtv1.VirtualMachineInstanceList{}
	err := testClient.CRClient.List(context.TODO(), vmiList)
	if err != nil {
		return err
	}

	// Log all VMIs to a single file
	vmiBytes, err := json.MarshalIndent(*vmiList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal VMI list: %v", err)
	}

	err = reporter.LogToFile("vmis", string(vmiBytes), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log VMIs: %v", err)
	}

	// Collect all VMI events
	var allEvents []corev1.Event
	for i := range vmiList.Items {
		vmi := &vmiList.Items[i]
		events, eventsErr := testClient.K8sClient.CoreV1().Events(vmi.Namespace).List(context.TODO(), metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=VirtualMachineInstance", vmi.Name),
		})
		if eventsErr != nil {
			return fmt.Errorf("failed to get events for VMI %s/%s: %v", vmi.Namespace, vmi.Name, err)
		}
		allEvents = append(allEvents, events.Items...)
	}

	// Log all VMI events to a single file
	eventBytes, err := json.MarshalIndent(allEvents, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal VMI events: %v", err)
	}

	err = reporter.LogToFile("vmi_events", string(eventBytes), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log VMI events: %v", err)
	}

	return nil
}

func logAllPods(failureCount int) error {
	// Log all pods from all namespaces
	podList, err := testClient.K8sClient.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list all pods: %v", err)
	}

	podBytes, err := json.MarshalIndent(*podList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal pod list: %v", err)
	}

	err = reporter.LogToFile("pods", string(podBytes), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log all pods: %v", err)
	}

	return nil
}

func logNetworkAttachmentDefinitions(failureCount int) error {
	// Log all NADs from all namespaces using REST client
	result := testClient.K8sClient.ExtensionsV1beta1().RESTClient().
		Get().
		RequestURI("/apis/k8s.cni.cncf.io/v1/network-attachment-definitions").
		Do(context.TODO())

	if result.Error() != nil {
		return fmt.Errorf("failed to list network attachment definitions: %v", result.Error())
	}

	nadBytes, err := result.Raw()
	if err != nil {
		return fmt.Errorf("failed to get raw NAD response: %v", err)
	}

	// Pretty print the JSON
	var prettyJSON bytes.Buffer
	err = json.Indent(&prettyJSON, nadBytes, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to indent NAD JSON: %v", err)
	}

	err = reporter.LogToFile("nads", prettyJSON.String(), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log NADs: %v", err)
	}

	return nil
}

func logNamespaces(failureCount int) error {
	// Log all namespaces
	namespaceList, err := testClient.K8sClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}

	namespaceBytes, err := json.MarshalIndent(*namespaceList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal namespace list: %v", err)
	}

	err = reporter.LogToFile("namespaces", string(namespaceBytes), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log namespaces: %v", err)
	}

	return nil
}

func logVirtualMachineInstanceMigrations(failureCount int) error {
	// Log all VMIMs from all namespaces
	vmimList := &kubevirtv1.VirtualMachineInstanceMigrationList{}
	err := testClient.CRClient.List(context.TODO(), vmimList)
	if err != nil {
		return fmt.Errorf("failed to list virtual machine instance migrations: %v", err)
	}

	vmimBytes, err := json.MarshalIndent(*vmimList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal VMIM list: %v", err)
	}

	err = reporter.LogToFile("vmims", string(vmimBytes), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log VMIMs: %v", err)
	}

	return nil
}

func logKubevirtPods(failureCount int) error {
	kubevirtArtifactDir := artifactDir + "kubevirt/"

	// Get all pods in kubevirt namespace
	podList, err := testClient.K8sClient.CoreV1().Pods(kubevirtNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods in kubevirt namespace: %v", err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if strings.Contains(pod.Name, "virt-controller") ||
			strings.Contains(pod.Name, "virt-handler") ||
			strings.Contains(pod.Name, "virt-api") ||
			strings.Contains(pod.Name, "virt-synchronization") {
			for _, container := range pod.Spec.Containers {
				containerName := container.Name
				podLogs, err := getPodContainerLogsFromNamespace(pod.Name, containerName, kubevirtNamespace)
				if err != nil {
					fmt.Printf("Failed to get logs for kubevirt pod %s container %s: %v\n", pod.Name, containerName, err)
					continue
				}

				fileName := fmt.Sprintf("pod_%s_container_%s", pod.Name, containerName)
				err = reporter.LogToFile(fileName, podLogs, kubevirtArtifactDir, failureCount)
				if err != nil {
					fmt.Printf("Failed to write logs for kubevirt pod %s container %s: %v\n", pod.Name, containerName, err)
				}
			}
		}
	}

	return nil
}

func getPodContainerLogsFromNamespace(podName, containerName, namespace string) (string, error) {
	req := testClient.K8sClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
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

func logKubevirtConfig(failureCount int) error {
	kubevirtArtifactDir := artifactDir + "kubevirt/"

	// Get kubevirt-config ConfigMap if it exists
	configMap, err := testClient.K8sClient.CoreV1().ConfigMaps(kubevirtNamespace).Get(context.TODO(), "kubevirt-config", metav1.GetOptions{})
	if err != nil {
		// ConfigMap might not exist, that's okay
		fmt.Printf("Note: kubevirt-config ConfigMap not found (this is okay if using defaults): %v\n", err)
	} else {
		configBytes, marshalErr := json.MarshalIndent(*configMap, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal kubevirt-config: %v", err)
		}

		err = reporter.LogToFile("config", string(configBytes), kubevirtArtifactDir, failureCount)
		if err != nil {
			return fmt.Errorf("failed to log kubevirt-config: %v", err)
		}
	}

	// Get KubeVirt CR
	kubevirtList := &kubevirtv1.KubeVirtList{}
	err = testClient.CRClient.List(context.TODO(), kubevirtList)
	if err != nil {
		return fmt.Errorf("failed to list KubeVirt CRs: %v", err)
	}

	kubevirtBytes, err := json.MarshalIndent(*kubevirtList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal KubeVirt CR: %v", err)
	}

	err = reporter.LogToFile("cr", string(kubevirtBytes), kubevirtArtifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log KubeVirt CR: %v", err)
	}

	return nil
}

func logNodes(failureCount int) error {
	nodeList, err := testClient.K8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %v", err)
	}

	nodesBytes, err := json.MarshalIndent(*nodeList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal nodes: %v", err)
	}

	err = reporter.LogToFile("nodes", string(nodesBytes), artifactDir, failureCount)
	if err != nil {
		return fmt.Errorf("failed to log nodes: %v", err)
	}

	return nil
}
