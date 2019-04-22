package tests

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"k8s.io/client-go/kubernetes/scheme"
	kubevirtv1 "kubevirt.io/kubevirt/pkg/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubevirtutils "kubevirt.io/kubevirt/tools/vms-generator/utils"
)

const (
	TestNamespace      = "kubemacpool-test"
	OtherTestNamespace = "kubemacpool-test-alternative"

	postUrl    = "/apis/k8s.cni.cncf.io/v1/namespaces/%s/network-attachment-definitions/%s"
	ovsConfCRD = `{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition","metadata":{"name":"%s","namespace":"%s"},"spec":{"config":"{ \"cniVersion\": \"0.3.1\", \"type\": \"ovs\", \"bridge\": \"br1\", \"vlan\": 100 }"}}`
)

var testClient *TestClient

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
	_, err := testClient.KubeClient.CoreV1().Namespaces().Create(&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: TestNamespace}})
	if err != nil {
		return err
	}
	_, err = testClient.KubeClient.CoreV1().Namespaces().Create(&v1.Namespace{ObjectMeta: v12.ObjectMeta{Name: OtherTestNamespace}})
	return err
}

func deleteTestNamespaces(namespace string) error {
	return testClient.KubeClient.CoreV1().Namespaces().Delete(namespace, &v12.DeleteOptions{})
}

func removeTestNamespaces() {
	fmt.Printf("Waiting for namespace %s to be removed, this can take a while ...\n", TestNamespace)
	EventuallyWithOffset(1, func() bool { return errors.IsNotFound(deleteTestNamespaces(TestNamespace)) }, 120*time.Second, 5*time.Second).
		Should(BeTrue())

	fmt.Printf("Waiting for namespace %s to be removed, this can take a while ...\n", OtherTestNamespace)
	EventuallyWithOffset(1, func() bool { return errors.IsNotFound(deleteTestNamespaces(OtherTestNamespace)) }, 120*time.Second, 5*time.Second).
		Should(BeTrue())
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

func BeforeAll(fn func()) {
	first := true
	BeforeEach(func() {
		if first {
			fn()
			first = false
		}
	})
}
