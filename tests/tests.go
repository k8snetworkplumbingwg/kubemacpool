package tests

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubevirtv1 "kubevirt.io/client-go/api/v1"
	kubevirtutils "kubevirt.io/kubevirt/tools/vms-generator/utils"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const (
	TestNamespace      = "kubemacpool-test"
	OtherTestNamespace = "kubemacpool-test-alternative"
	ManagerNamespce    = names.MANAGER_NAMESPACE
	nadPostUrl         = "/apis/k8s.cni.cncf.io/v1/namespaces/%s/network-attachment-definitions/%s"
	linuxBridgeConfCRD = `{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition","metadata":{"name":"%s","namespace":"%s"},"spec":{"config":"{ \"cniVersion\": \"0.3.1\", \"type\": \"bridge\", \"bridge\": \"br1\"}"}}`
)

var (
	gracePeriodSeconds int64 = 10
	rangeStart               = "02:00:00:00:00:00"
	rangeEnd                 = "02:FF:FF:FF:FF:FF"
	testClient         *TestClient
)

type TestClient struct {
	VirtClient client.Client
	KubeClient *kubernetes.Clientset
}

func NewTestClient() (*TestClient, error) {
	t := &envtest.Environment{
		UseExistingCluster: true,
	}

	var cfg *rest.Config
	var err error

	if cfg, err = t.Start(); err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	err = kubevirtv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, err
	}

	var c client.Client
	if c, err = client.New(cfg, client.Options{Scheme: scheme.Scheme}); err != nil {
		return nil, err
	}

	return &TestClient{VirtClient: c, KubeClient: kubeClient}, nil
}

func createTestNamespaces() error {
	_, err := testClient.KubeClient.CoreV1().Namespaces().Create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: TestNamespace}})
	if err != nil {
		return err
	}
	_, err = testClient.KubeClient.CoreV1().Namespaces().Create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: OtherTestNamespace}})
	return err
}

func deleteTestNamespaces(namespace string) error {
	return testClient.KubeClient.CoreV1().Namespaces().Delete(namespace, &metav1.DeleteOptions{})
}

func removeTestNamespaces() {

	By(fmt.Sprintf("Waiting for namespace %s to be removed, this can take a while ...\n", TestNamespace))
	EventuallyWithOffset(1, func() bool { return errors.IsNotFound(deleteTestNamespaces(TestNamespace)) }, 120*time.Second, 5*time.Second).
		Should(BeTrue(), "Namespace %s haven't been deleted within the given timeout", TestNamespace)

	By(fmt.Sprintf("Waiting for namespace %s to be removed, this can take a while ...\n", OtherTestNamespace))
	EventuallyWithOffset(1, func() bool { return errors.IsNotFound(deleteTestNamespaces(OtherTestNamespace)) }, 120*time.Second, 5*time.Second).
		Should(BeTrue(), "Namespace %s haven't been deleted within the given timeout", TestNamespace)
}

func CreateVmObject(namespace string, running bool, interfaces []kubevirtv1.Interface, networks []kubevirtv1.Network) *kubevirtv1.VirtualMachine {
	vm := kubevirtutils.GetVMCirros()
	vm.Name = "testvm" + rand.String(32)
	vm.Namespace = namespace
	vm.Spec.Running = &running
	vm.Spec.Template.Spec.Domain.Devices.Interfaces = interfaces
	vm.Spec.Template.Spec.Networks = networks

	return vm
}

func setRange(rangeStart, rangeEnd string) error {
	configMap, err := testClient.KubeClient.CoreV1().ConfigMaps(ManagerNamespce).Get("kubemacpool-mac-range-config", metav1.GetOptions{})
	if err != nil {
		return err
	}

	configMap.Data["RANGE_START"] = rangeStart
	configMap.Data["RANGE_END"] = rangeEnd

	_, err = testClient.KubeClient.CoreV1().ConfigMaps(ManagerNamespce).Update(configMap)
	if err != nil {
		return err
	}

	oldPods, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, pod := range oldPods.Items {
		err = testClient.KubeClient.CoreV1().Pods(ManagerNamespce).Delete(pod.Name, &metav1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds})
		if err != nil {
			return err
		}
	}

	Eventually(func() error {
		currentPods, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).List(metav1.ListOptions{})
		if err != nil {
			return err
		}

		for _, currentPod := range currentPods.Items {
			for _, oldPod := range oldPods.Items {
				if currentPod.Name == oldPod.Name {
					return fmt.Errorf("old pod %s has not yet been removed", oldPod.Name)
				}
			}
		}

		for _, currentPod := range currentPods.Items {
			if len(currentPod.Status.ContainerStatuses) >= 1 && currentPod.Status.ContainerStatuses[0].Ready {
				return nil
			}
		}

		return fmt.Errorf("have not found any manager in the ready state")

	}, 2*time.Minute, 3*time.Second).Should(Not(HaveOccurred()), "failed to start new set of manager pods within the given timeout")

	return nil
}

func DeleteLeaderManager() {
	pods, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).List(metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())

	leaderPodName := ""
	for _, pod := range pods.Items {
		if _, ok := pod.Labels[names.LEADER_LABEL]; ok {
			leaderPodName = pod.Name
			break
		}
	}

	Expect(leaderPodName).ToNot(BeEmpty())

	err = testClient.KubeClient.CoreV1().Pods(ManagerNamespce).Delete(leaderPodName, &metav1.DeleteOptions{})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() bool {
		_, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).Get(leaderPodName, metav1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true
		}

		return false
	}, 30*time.Second, 3*time.Second).Should(BeTrue(), "failed to delete kubemacpool leader pod")

	Eventually(func() error {
		currentPods, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).List(metav1.ListOptions{})
		if err != nil {
			return err
		}

		for _, currentPod := range currentPods.Items {
			if len(currentPod.Status.ContainerStatuses) >= 1 && currentPod.Status.ContainerStatuses[0].Ready {
				return nil
			}
		}

		return fmt.Errorf("have not found any manager in the ready state")
	}, 2*time.Minute, 3*time.Second).ShouldNot(HaveOccurred(), "timed out while waiting for a new leader")
}

func BeforeAll(fn func()) {
	first := true
	BeforeEach(func() {
		if first {
			fn()
			first = false
		}
	})
}
