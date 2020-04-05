package tests

import (
	"fmt"
	"math"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/rand"
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
	gracePeriodSeconds int64 = 3
	rangeStart               = "02:00:00:00:00:00"
	rangeEnd                 = "02:FF:FF:FF:FF:FF"
	testClient         *TestClient
)

type TestClient struct {
	VirtClient client.Client
	KubeClient *kubernetes.Clientset
}

func NewTestClient() (*TestClient, error) {
	trueBoolean := true
	t := &envtest.Environment{
		UseExistingCluster: &trueBoolean,
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

func createPodObject() *corev1.Pod {
	podName := "testpod" + rand.String(32)
	podObject := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName},
		Spec: corev1.PodSpec{TerminationGracePeriodSeconds: &gracePeriodSeconds,
			Containers: []corev1.Container{{Name: "test",
				Image:   "centos",
				Command: []string{"/bin/bash", "-c", "sleep INF"}}}}}

	return &podObject
}

func addNetworksToPod(pod *corev1.Pod, networks []map[string]string) {
	if networks != nil && len(networks) > 0 {
		pod.Annotations = map[string]string{"k8s.v1.cni.cncf.io/networks": fmt.Sprintf("%v", networks)}
	}
}

func findPodByUid(pods *corev1.PodList, podToFind corev1.Pod) *corev1.Pod {
	for _, pod := range pods.Items {
		if pod.ObjectMeta.UID == podToFind.ObjectMeta.UID {
			return &pod
		}
	}
	return nil
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

		for _, oldPod := range oldPods.Items {
			if findPodByUid(currentPods, oldPod) != nil {
				return fmt.Errorf("old pod %s has not yet been removed", oldPod.Name)
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
	leaderPodUid := types.UID("")
	for _, pod := range pods.Items {
		if _, ok := pod.Labels[names.LEADER_LABEL]; ok {
			leaderPodName = pod.Name
			leaderPodUid = pod.ObjectMeta.UID
			break
		}
	}

	Expect(leaderPodName).ToNot(BeEmpty())

	err = testClient.KubeClient.CoreV1().Pods(ManagerNamespce).Delete(leaderPodName, &metav1.DeleteOptions{})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() bool {
		pod, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).Get(leaderPodName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		if pod.ObjectMeta.UID != leaderPodUid {
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

func changeManagerReplicas(numOfReplica int32) error {
	Eventually(func() error {
		managerStatefulset, err := testClient.KubeClient.AppsV1().StatefulSets(ManagerNamespce).Get(names.MANAGER_STATEFULSET, metav1.GetOptions{})
		if err != nil {
			return err
		}

		managerStatefulset.Spec.Replicas = &numOfReplica

		_, err = testClient.KubeClient.AppsV1().StatefulSets(ManagerNamespce).Update(managerStatefulset)
		if err != nil {
			return err
		}

		return nil
	}, 30*time.Second, 3*time.Second).ShouldNot(HaveOccurred(), "failed to update number of replicas on manager")

	Eventually(func() bool {
		managerStatefulset, err := testClient.KubeClient.AppsV1().StatefulSets(ManagerNamespce).Get(names.MANAGER_STATEFULSET, metav1.GetOptions{})
		if err != nil {
			return false
		}

		if managerStatefulset.Status.Replicas != numOfReplica {
			return false
		}
		//due to readiness probe only 1 (the leader) pod will be ready (if any)
		if float64(managerStatefulset.Status.ReadyReplicas) != math.Min(float64(1), float64(numOfReplica)) {
			return false
		}

		podsList, err := testClient.KubeClient.CoreV1().Pods(ManagerNamespce).List(metav1.ListOptions{})
		if err != nil {
			return false
		}

		if len(podsList.Items) != int(numOfReplica) {
			return false
		}

		for _, podObject := range podsList.Items {
			if podObject.Status.Phase != corev1.PodRunning {
				return false
			}
		}

		return true

	}, 2*time.Minute, 3*time.Second).Should(BeTrue(), "failed to change kubemacpool statefulset number of replicas")

	return nil
}

func cleanNamespaceLabels(namespace string) error {
	nsObject, err := testClient.KubeClient.CoreV1().Namespaces().Get(namespace, metav1.GetOptions{})
	if err != nil {
		return err
	}

	nsObject.Labels = make(map[string]string)

	_, err = testClient.KubeClient.CoreV1().Namespaces().Update(nsObject)
	return err
}

func addLabelsToNamespace(namespace string, labels map[string]string) error {
	nsObject, err := testClient.KubeClient.CoreV1().Namespaces().Get(namespace, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if nsObject.Labels == nil {
		nsObject.Labels = labels
	} else {
		for key, value := range labels {
			nsObject.Labels[key] = value
		}
	}

	_, err = testClient.KubeClient.CoreV1().Namespaces().Update(nsObject)
	return err
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
