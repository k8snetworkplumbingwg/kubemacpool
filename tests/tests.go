package tests

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/pkg/errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubevirtv1 "kubevirt.io/client-go/api/v1"
	kubevirtutils "kubevirt.io/kubevirt/tools/vms-generator/utils"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

const (
	TestNamespace                = "kubemacpool-test"
	OtherTestNamespace           = "kubemacpool-test-alternative"
	nadPostUrl                   = "/apis/k8s.cni.cncf.io/v1/namespaces/%s/network-attachment-definitions/%s"
	linuxBridgeConfCRD           = `{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition","metadata":{"name":"%s","namespace":"%s"},"spec":{"config":"{ \"cniVersion\": \"0.3.1\", \"type\": \"bridge\", \"bridge\": \"br1\"}"}}`
	podNamespaceOptInLabel       = "mutatepods.kubemacpool.io"
	vmNamespaceOptInLabel        = "mutatevirtualmachines.kubemacpool.io"
	deploymentContainerName      = "manager"
	optInMode                    = "opt-in"
	optOutMode                   = "opt-out"
	mutatingWebhookConfiguration = "kubemacpool-mutator"
)

var (
	managerNamespace         = ""
	gracePeriodSeconds int64 = 3
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
	_, err := testClient.KubeClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: TestNamespace}}, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = testClient.KubeClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: OtherTestNamespace}}, metav1.CreateOptions{})
	return err
}

func deleteTestNamespaces(namespace string) error {
	return testClient.KubeClient.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
}

func removeTestNamespaces() {
	By(fmt.Sprintf("Waiting for namespace %s to be removed, this can take a while ...\n", TestNamespace))
	EventuallyWithOffset(1, func() bool { return apierrors.IsNotFound(deleteTestNamespaces(TestNamespace)) }, 120*time.Second, 5*time.Second).
		Should(BeTrue(), "Namespace %s haven't been deleted within the given timeout", TestNamespace)

	By(fmt.Sprintf("Waiting for namespace %s to be removed, this can take a while ...\n", OtherTestNamespace))
	EventuallyWithOffset(1, func() bool { return apierrors.IsNotFound(deleteTestNamespaces(OtherTestNamespace)) }, 120*time.Second, 5*time.Second).
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

func findPodByName(pods *corev1.PodList, podToFind corev1.Pod) *corev1.Pod {
	for _, pod := range pods.Items {
		if pod.Name == podToFind.Name {
			return &pod
		}
	}
	return nil
}

func getKubemacpoolPods() (*corev1.PodList, error) {
	filterByApp := client.MatchingLabels{
		"app": "kubemacpool",
	}
	pods := &corev1.PodList{}
	err := testClient.VirtClient.List(context.TODO(), pods, filterByApp)
	if err != nil {
		return nil, err
	}
	return pods, nil
}

func findManagerNamespace() string {
	// Retrieve manager namespace serching for pods
	// with app=kubemacpool and getting the env var POD_NAMESPACE
	pods, err := getKubemacpoolPods()
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "should be able to retrieve manager pods")
	ExpectWithOffset(1, pods.Items).ToNot(BeEmpty(), "should have multiple manager pods")
	namespace := pods.Items[0].ObjectMeta.Namespace
	ExpectWithOffset(1, namespace).ToNot(BeEmpty(), "should be a namespace at manager pods")
	return namespace
}

func restartKubemacpoolManagerPods() error {
	// Remove all replicas
	err := changeManagerReplicas(0)
	if err != nil {
		return errors.Wrap(err, "failed stopping manager pods")
	}

	err = changeManagerReplicas(1)
	if err != nil {
		return errors.Wrap(err, "failed starting manager pods")
	}

	return nil
}

func initKubemacpoolParams() error {
	By("Restart Kubemacpool Pods")
	err := restartKubemacpoolManagerPods()
	Expect(err).ToNot(HaveOccurred(), "Should succeed resetting the kubemacpool pods")

	return nil
}

func getMacPoolSize() int64 {
	configMap, err := testClient.KubeClient.CoreV1().ConfigMaps(managerNamespace).Get(context.TODO(), "kubemacpool-mac-range-config", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should succeed getting kubemacpool range configmap")

	rangeStart, err := net.ParseMAC(configMap.Data["RANGE_START"])
	rangeEnd, err := net.ParseMAC(configMap.Data["RANGE_END"])

	pooSize, err := poolmanager.GetMacPoolSize(rangeStart, rangeEnd)
	Expect(err).ToNot(HaveOccurred(), "Should succeed getting the mac pool size")

	return pooSize
}

func getWaitTimeValueFromArguments(args []string) (time.Duration, bool) {
	r := regexp.MustCompile(fmt.Sprintf("--%s=(\\d+)", names.WAIT_TIME_ARG))
	for _, arg := range args {
		match := r.FindStringSubmatch(arg)
		if match != nil {
			waitTimeValue, err := strconv.Atoi(match[1])
			Expect(err).ToNot(HaveOccurred(), "Should successfully parse wait-time argument")
			return time.Duration(waitTimeValue) * time.Second, true
		}
	}

	return 0, false
}

func getVmFailCleanupWaitTime() time.Duration {
	managerDeployment := v1.Deployment{}
	err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: managerNamespace, Name: names.MANAGER_DEPLOYMENT}, &managerDeployment)
	Expect(err).ToNot(HaveOccurred(), "Should successfully get manager's Deployment")
	Expect(managerDeployment.Spec.Template.Spec.Containers).ToNot(BeEmpty(), "Manager's deployment should contain containers")

	for _, container := range managerDeployment.Spec.Template.Spec.Containers {
		if container.Name == deploymentContainerName {
			if waitTimeDuration, found := getWaitTimeValueFromArguments(container.Args); found {
				return waitTimeDuration
			}
		}
	}

	Fail(fmt.Sprintf("Failed to find wait-time argument in %s container inside %s deployment", deploymentContainerName, names.MANAGER_DEPLOYMENT))
	return 0
}

func changeManagerReplicas(numOfReplica int32) error {
	By(fmt.Sprintf("updating deployment pod replicas to be %d", numOfReplica))
	Eventually(func() error {
		managerDeployment, err := testClient.KubeClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(), names.MANAGER_DEPLOYMENT, metav1.GetOptions{})
		if err != nil {
			return err
		}

		managerDeployment.Spec.Replicas = &numOfReplica

		_, err = testClient.KubeClient.AppsV1().Deployments(managerNamespace).Update(context.TODO(), managerDeployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}

		return nil
	}, 30*time.Second, 3*time.Second).ShouldNot(HaveOccurred(), "failed to update number of replicas on manager")

	By(fmt.Sprintf("Waiting for expected ready pods to be %d", numOfReplica))
	Eventually(func() bool {
		managerDeployment, err := testClient.KubeClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(), names.MANAGER_DEPLOYMENT, metav1.GetOptions{})
		if err != nil {
			return false
		}

		if managerDeployment.Status.ReadyReplicas != numOfReplica {
			return false
		}

		return true

	}, 2*time.Minute, 3*time.Second).Should(BeTrue(), "failed to change kubemacpool deployment number of replicas")

	return nil
}

func cleanNamespaceLabels(namespace string) error {
	nsObject, err := testClient.KubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return err
	}

	nsObject.Labels = make(map[string]string)

	_, err = testClient.KubeClient.CoreV1().Namespaces().Update(context.TODO(), nsObject, metav1.UpdateOptions{})
	return err
}

func addLabelsToNamespace(namespace string, labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}
	nsObject, err := testClient.KubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
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

	_, err = testClient.KubeClient.CoreV1().Namespaces().Update(context.TODO(), nsObject, metav1.UpdateOptions{})
	return err
}

func getOptInLabel(webhookName string) metav1.LabelSelectorRequirement {
	return metav1.LabelSelectorRequirement{Key: webhookName, Operator: "In", Values: []string{"allocate"}}
}

func getOptOutLabel(webhookName string) metav1.LabelSelectorRequirement {
	return metav1.LabelSelectorRequirement{Key: webhookName, Operator: "NotIn", Values: []string{"ignore"}}
}

func createVmWaitConfigMap() error {
	vmWaitConfigMap := &corev1.ConfigMap{Data: map[string]string{}}
	vmWaitConfigMap.SetName(names.WAITING_VMS_CONFIGMAP)
	vmWaitConfigMap.SetNamespace(managerNamespace)
	_, err := testClient.KubeClient.CoreV1().ConfigMaps(managerNamespace).Create(context.TODO(), vmWaitConfigMap, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to create %s ConfigMap", names.WAITING_VMS_CONFIGMAP)
	}

	return nil
}

func deleteVmWaitConfigMap() error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return testClient.KubeClient.CoreV1().ConfigMaps(managerNamespace).Delete(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.DeleteOptions{})
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "failed to create %s ConfigMap", names.WAITING_VMS_CONFIGMAP)
	}
	return nil
}

func getVmWaitConfigMap() (*corev1.ConfigMap, error) {
	vmWaitConfigMap, err := testClient.KubeClient.CoreV1().ConfigMaps(managerNamespace).Get(context.TODO(), names.WAITING_VMS_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get %s ConfigMap", names.WAITING_VMS_CONFIGMAP)
	}

	return vmWaitConfigMap, nil
}

func updateVmWaitConfigMap(f func(vmWaitConfigMap *corev1.ConfigMap) error) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		vmWaitConfigMap, err := getVmWaitConfigMap()
		if err != nil {
			return err
		}

		err = f(vmWaitConfigMap)
		if err != nil {
			return errors.Wrap(err, "got an error in updating function")
		}

		_, err = testClient.KubeClient.CoreV1().ConfigMaps(managerNamespace).Update(context.TODO(), vmWaitConfigMap, metav1.UpdateOptions{})
		return err
	})
	return err
}

func simulateSoonToBeStaleEntryInConfigMap(macAddress string) error {
	waitTime := getVmFailCleanupWaitTime()
	macAddressDashes := strings.Replace(macAddress, ":", "-", 5)

	err := updateVmWaitConfigMap(func(vmWaitConfigMap *corev1.ConfigMap) error {
		if vmWaitConfigMap.Data == nil {
			vmWaitConfigMap.Data = map[string]string{}
		}
		// legacy configMap uses time.RFC3339 format timestamps
		// This entry should go stale after 1 minute
		vmWaitConfigMap.Data[macAddressDashes] = time.Now().Add(-waitTime + time.Minute).Format(time.RFC3339)
		return nil
	})

	if err != nil {
		return errors.Wrap(err, "Failed to add soon to be stale entry to configMap")
	}
	return nil
}

// function checks what is the currently configured opt-mode configured in a specific webhook
func getOptMode(webhookName string) (string, error) {
	mutatingWebhook, err := testClient.KubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), mutatingWebhookConfiguration, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	for _, webhook := range mutatingWebhook.Webhooks {
		if webhook.Name == webhookName {
			for _, matchExpression := range webhook.NamespaceSelector.MatchExpressions {
				if matchExpression.Key == webhookName {
					if reflect.DeepEqual(matchExpression, getOptInLabel(webhookName)) {
						return optInMode, nil
					} else if reflect.DeepEqual(matchExpression, getOptOutLabel(webhookName)) {
						return optOutMode, nil
					} else {
						return "", fmt.Errorf("webhook %s opt-in label expression selector does not match any opt-mode", webhookName)
					}
				}
			}
		}
	}

	return "", fmt.Errorf("webhook %s not found in mutatingWebhookConfiguration", webhookName)
}

func setWebhookOptMode(webhookName, optMode string) error {
	By(fmt.Sprintf("Setting webhook %s to %s in MutatingWebhookConfigurations instance", webhookName, optMode))
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mutatingWebhook, err := testClient.KubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), mutatingWebhookConfiguration, metav1.GetOptions{})
		if err != nil {
			return err
		}

		for webhookIdx, webhook := range mutatingWebhook.Webhooks {
			if webhook.Name == webhookName {
				var expressions []metav1.LabelSelectorRequirement
				for _, matchExpression := range webhook.NamespaceSelector.MatchExpressions {
					if matchExpression.Key != webhookName {
						expressions = append(expressions, matchExpression)
					}
				}

				switch optMode {
				case optInMode:
					expressions = append(expressions, getOptInLabel(webhookName))
				case optOutMode:
					expressions = append(expressions, getOptOutLabel(webhookName))
				default:
					return fmt.Errorf("undefined opt-in mode %s", optMode)
				}

				mutatingWebhook.Webhooks[webhookIdx].NamespaceSelector.MatchExpressions = expressions
				_, err = testClient.KubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(), mutatingWebhook, metav1.UpdateOptions{})

				return err
			}
		}
		return nil
	})

	if err != nil {
		return errors.Wrap(err, "failed to perform Retry on Conflict to set opt mode")
	}
	return nil
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
