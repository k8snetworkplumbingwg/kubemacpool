package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/pkg/errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	helper "github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

const (
	TestNamespace      = "kubemacpool-test"
	OtherTestNamespace = "kubemacpool-test-alternative"
	nadPostURL         = "/apis/k8s.cni.cncf.io/v1/namespaces/%s/network-attachment-definitions/%s"
	linuxBridgeConfCRD = `{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition",` +
		`"metadata":{"name":"%s","namespace":"%s"},` +
		`"spec":{"config":"{ \"cniVersion\": \"0.3.1\", \"type\": \"bridge\", \"bridge\": \"br1\"}"}}`
	podNamespaceOptInLabel       = "mutatepods.kubemacpool.io"
	vmNamespaceOptInLabel        = "mutatevirtualmachines.kubemacpool.io"
	deploymentContainerName      = "manager"
	optInMode                    = "opt-in"
	optOutMode                   = "opt-out"
	mutatingWebhookConfiguration = "kubemacpool-mutator"

	// Test timeouts and intervals
	namespaceDeleteTimeout     = 120 * time.Second
	namespaceDeleteInterval    = 5 * time.Second
	deploymentUpdateTimeout    = 30 * time.Second
	deploymentUpdateInterval   = 3 * time.Second
	replicaWaitTimeout         = 2 * time.Minute
	podDeleteTimeout           = 2 * time.Minute
	podDeleteInterval          = 1 * time.Second
	deploymentAvailableTimeout = 2 * time.Minute

	// String and format constants
	randomStringLength     = 5
	macAddressReplaceCount = 5
)

var (
	managerNamespace         = ""
	gracePeriodSeconds int64 = 3
	testClient         *TestClient
)

type TestClient struct {
	// Deprecated: VirtClient is deprecated. Use K8sClient for standard Kubernetes operations
	// and CRClient (controller-runtime client) for KubeVirt VirtualMachine operations instead.
	VirtClient kubecli.KubevirtClient
	K8sClient  kubernetes.Interface
	CRClient   client.Client
}

func NewTestClient() (*TestClient, error) {
	config, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		return nil, err
	}

	// Keep old client for backward compatibility during migration
	oldVirtClient, err := kubecli.GetKubevirtClientFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		return nil, err
	}

	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	_ = kubevirtv1.AddToScheme(scheme)

	crClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	return &TestClient{
		VirtClient: oldVirtClient,
		K8sClient:  k8sClient,
		CRClient:   crClient,
	}, nil
}

func createTestNamespaces() error {
	_, err := testClient.K8sClient.CoreV1().Namespaces().Create(context.TODO(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: TestNamespace}}, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	_, err = testClient.K8sClient.CoreV1().Namespaces().Create(context.TODO(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: OtherTestNamespace}}, metav1.CreateOptions{})
	return err
}

func deleteTestNamespaces(namespace string) error {
	err := testClient.K8sClient.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
	return err
}

func removeTestNamespaces() {
	By(fmt.Sprintf("Waiting for namespace %s to be removed, this can take a while ...\n", TestNamespace))
	EventuallyWithOffset(1, func() bool { return apierrors.IsNotFound(deleteTestNamespaces(TestNamespace)) },
		namespaceDeleteTimeout, namespaceDeleteInterval).
		Should(BeTrue(), "Namespace %s haven't been deleted within the given timeout", TestNamespace)

	By(fmt.Sprintf("Waiting for namespace %s to be removed, this can take a while ...\n", OtherTestNamespace))
	EventuallyWithOffset(1, func() bool { return apierrors.IsNotFound(deleteTestNamespaces(OtherTestNamespace)) },
		namespaceDeleteTimeout, namespaceDeleteInterval).
		Should(BeTrue(), "Namespace %s haven't been deleted within the given timeout", TestNamespace)
}

func CreateVMObject(namespace string, interfaces []kubevirtv1.Interface, networks []kubevirtv1.Network) *kubevirtv1.VirtualMachine {
	vm := getVMCirros()
	vm.Name = randName("testvm")
	vm.Namespace = namespace
	vm.Spec.Template.Spec.Domain.Devices.Interfaces = interfaces
	vm.Spec.Template.Spec.Networks = networks

	return vm
}

func createPodObject() *corev1.Pod {
	podName := randName("testpod")
	podObject := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName},
		Spec: corev1.PodSpec{TerminationGracePeriodSeconds: &gracePeriodSeconds,
			Containers: []corev1.Container{{Name: "test",
				Image:   "centos",
				Command: []string{"/bin/bash", "-c", "sleep INF"}}}}}

	return &podObject
}

func randName(name string) string {
	return name + "-" + rand.String(randomStringLength)
}

func getKubemacpoolPods() (*corev1.PodList, error) {
	filterByApp := fmt.Sprintf("%s=%s", "app", "kubemacpool")
	pods, err := testClient.K8sClient.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{LabelSelector: filterByApp})
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
	// Delete all kmp manager pods
	err := restartPodsFromDeployment(names.MANAGER_DEPLOYMENT)
	if err != nil {
		return errors.Wrap(err, "failed deleting mac manager pods")
	}

	// Delete all cert-manager pods
	err = restartPodsFromDeployment(names.CERT_MANAGER_DEPLOYMENT)
	if err != nil {
		return errors.Wrap(err, "failed deleting cert manager pods")
	}

	return nil
}

func initKubemacpoolParams() error {
	By("Restart Kubemacpool Pods")
	err := restartKubemacpoolManagerPods()
	if err != nil {
		return fmt.Errorf("should succeed resetting the kubemacpool pods: %w", err)
	}
	return nil
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

func getVMFailCleanupWaitTime() time.Duration {
	managerDeployment, err := testClient.K8sClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(),
		names.MANAGER_DEPLOYMENT, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should successfully get manager's Deployment")
	Expect(managerDeployment.Spec.Template.Spec.Containers).ToNot(BeEmpty(), "Manager's deployment should contain containers")

	for _, container := range managerDeployment.Spec.Template.Spec.Containers {
		if container.Name == deploymentContainerName {
			if waitTimeDuration, found := getWaitTimeValueFromArguments(container.Args); found {
				return waitTimeDuration
			}
		}
	}

	Fail(fmt.Sprintf("Failed to find wait-time argument in %s container inside %s deployment",
		deploymentContainerName, names.MANAGER_DEPLOYMENT))
	return 0
}

func checkKubemacpoolCrash() error {
	kubemacpoolPods, err := getKubemacpoolPods()
	if err != nil {
		return err
	}
	for i := range kubemacpoolPods.Items {
		pod := &kubemacpoolPods.Items[i]
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.RestartCount != 0 {
				return errors.New(fmt.Sprintf("Kubemacpool container crashed %d times", containerStatus.RestartCount))
			}
		}
	}
	return nil
}

func changeManagerReplicas(numOfReplica int32) error {
	return changeReplicas(names.MANAGER_DEPLOYMENT, numOfReplica)
}

func changeReplicas(managerName string, numOfReplica int32) error {
	By(fmt.Sprintf("updating deployment %s, pod replicas to be %d", managerName, numOfReplica))
	var indentedDeployment []byte
	Eventually(func() error {
		managerDeployment, err := testClient.K8sClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(),
			managerName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		indentedDeployment, err = json.MarshalIndent(managerDeployment, "", "\t")
		if err != nil {
			return err
		}

		managerDeployment.Spec.Replicas = &numOfReplica

		_, err = testClient.K8sClient.AppsV1().Deployments(managerNamespace).Update(context.TODO(), managerDeployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}

		return nil
	}, deploymentUpdateTimeout, deploymentUpdateInterval).ShouldNot(HaveOccurred(),
		"failed to update number of replicas on deployment:\n%v", string(indentedDeployment))

	By(fmt.Sprintf("Waiting for expected ready pods to be %d", numOfReplica))
	Eventually(func() bool {
		managerDeployment, err := testClient.K8sClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(),
			managerName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		indentedDeployment, err = json.MarshalIndent(managerDeployment, "", "\t")
		if err != nil {
			return false
		}

		if managerDeployment.Status.ReadyReplicas != numOfReplica {
			return false
		}

		return true
	}, replicaWaitTimeout, deploymentUpdateInterval).Should(BeTrue(),
		"failed to change kubemacpool deployment number of replicas.\n deployment:\n%v", string(indentedDeployment))

	return nil
}

func restartPodsFromDeployment(deploymentName string) error {
	deployment, err := testClient.K8sClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(), deploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	labelSelector := labels.Set(deployment.Spec.Selector.MatchLabels).String()

	podList, err := testClient.K8sClient.CoreV1().Pods(managerNamespace).List(context.TODO(),
		metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return err
	}

	By(fmt.Sprintf("Deleting pods from deployment %s with label selector %s", deploymentName, labelSelector))
	err = testClient.K8sClient.CoreV1().Pods(managerNamespace).DeleteCollection(context.TODO(),
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return err
	}

	By(fmt.Sprintf("Checking that all the old pods from deployment %s with label selector %s have being deleted",
		deploymentName, labelSelector))
	for i := range podList.Items {
		pod := &podList.Items[i]
		Eventually(func() error {
			_, err = testClient.K8sClient.CoreV1().Pods(managerNamespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			return err
		}, podDeleteTimeout, podDeleteInterval).Should(SatisfyAll(HaveOccurred(), WithTransform(apierrors.IsNotFound, BeTrue())),
			"should have delete the old pods from deployment %s", deploymentName)
	}

	deploymentConditionAvailability := func(conditionStatus corev1.ConditionStatus, timeout, interval time.Duration) {
		By(fmt.Sprintf("Waiting for deployment %s Available condition to be %s", deploymentName, conditionStatus))
		Eventually(func() bool {
			deployment, err := testClient.K8sClient.AppsV1().Deployments(managerNamespace).Get(context.TODO(), deploymentName, metav1.GetOptions{})
			if err != nil {
				return false
			}
			for _, condition := range deployment.Status.Conditions {
				if condition.Type == v1.DeploymentAvailable {
					return condition.Status == conditionStatus
				}
			}

			return true
		}, timeout, interval).Should(BeTrue(), "Failed waiting readiness at for deployment:\n%v", deploymentName)
	}
	deploymentConditionAvailability(corev1.ConditionTrue, deploymentAvailableTimeout, deploymentUpdateInterval)
	return nil
}

func cleanNamespaceLabels(namespace string) error {
	nsObject, err := testClient.K8sClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return err
	}

	nsObject.Labels = make(map[string]string)

	_, err = testClient.K8sClient.CoreV1().Namespaces().Update(context.TODO(), nsObject, metav1.UpdateOptions{})
	return err
}

func addLabelsToNamespace(namespace string, labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}
	nsObject, err := testClient.K8sClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
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

	_, err = testClient.K8sClient.CoreV1().Namespaces().Update(context.TODO(), nsObject, metav1.UpdateOptions{})
	return err
}

func getOptInLabel(webhookName string) metav1.LabelSelectorRequirement {
	return metav1.LabelSelectorRequirement{Key: webhookName, Operator: "In", Values: []string{"allocate"}}
}

func getOptOutLabel(webhookName string) metav1.LabelSelectorRequirement {
	return metav1.LabelSelectorRequirement{Key: webhookName, Operator: "NotIn", Values: []string{"ignore"}}
}

// function checks what is the currently configured opt-mode configured in a specific webhook
func getOptMode(webhookName string) (string, error) {
	mutatingWebhook, err := testClient.K8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(),
		mutatingWebhookConfiguration, metav1.GetOptions{})
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
		mutatingWebhook, err := testClient.K8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(),
			mutatingWebhookConfiguration, metav1.GetOptions{})
		if err != nil {
			return err
		}

		for webhookIdx, webhook := range mutatingWebhook.Webhooks {
			if webhook.Name != webhookName {
				continue
			}
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
			_, err = testClient.K8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(),
				mutatingWebhook, metav1.UpdateOptions{})

			return err
		}
		return nil
	})

	if err != nil {
		return errors.Wrap(err, "failed to perform Retry on Conflict to set opt mode")
	}
	return nil
}

func addFinalizer(virtualMachine *kubevirtv1.VirtualMachine, finalizerName string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		err = testClient.CRClient.Get(context.TODO(),
			client.ObjectKey{Namespace: virtualMachine.Namespace, Name: virtualMachine.Name}, virtualMachine)
		if err != nil {
			return err
		}
		finalizersList := virtualMachine.GetFinalizers()
		if helper.ContainsString(finalizersList, finalizerName) {
			return nil
		}
		virtualMachine.ObjectMeta.Finalizers = append(virtualMachine.ObjectMeta.Finalizers, finalizerName)
		err = testClient.CRClient.Update(context.TODO(), virtualMachine)
		return err
	})

	if err != nil {
		return errors.Wrap(err, "failed to apply finalizer to vm")
	}
	return nil
}

func getVMCirros() *kubevirtv1.VirtualMachine {
	return &kubevirtv1.VirtualMachine{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VirtualMachine",
			APIVersion: kubevirtv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "vm-cirros",
			Labels: map[string]string{
				"kubevirt.io/vm": "vm-cirros",
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: ptr.To(kubevirtv1.RunStrategyHalted),
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kubevirt.io/vm": "vm-cirros",
					},
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Resources: kubevirtv1.ResourceRequirements{
							Requests: map[corev1.ResourceName]resource.Quantity{
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
						Devices: kubevirtv1.Devices{
							Disks: []kubevirtv1.Disk{
								{
									Name: "containerdisk",
									DiskDevice: kubevirtv1.DiskDevice{
										Disk: &kubevirtv1.DiskTarget{
											Bus: kubevirtv1.VirtIO,
										},
									},
								},
								{
									Name: "cloudinitdisk",
									DiskDevice: kubevirtv1.DiskDevice{
										Disk: &kubevirtv1.DiskTarget{
											Bus: kubevirtv1.VirtIO,
										},
									},
								},
							},
						},
					},
					TerminationGracePeriodSeconds: ptr.To[int64](0),
					Volumes: []kubevirtv1.Volume{
						{
							Name: "containerdisk",
							VolumeSource: kubevirtv1.VolumeSource{
								ContainerDisk: &kubevirtv1.ContainerDiskSource{
									Image: "registry:5000/kubevirt/cirros-container-disk-demo:devel",
								},
							},
						},
						{
							Name: "cloudinitdisk",
							VolumeSource: kubevirtv1.VolumeSource{
								CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
									UserData: "#!/bin/sh\n\necho 'printed from cloud-init userdata'\n",
								},
							},
						},
					},
				},
			},
		},
	}
}
