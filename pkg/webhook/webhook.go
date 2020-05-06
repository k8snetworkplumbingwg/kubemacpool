/*
Copyright 2019 The KubeMacPool authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"fmt"

	webhookserver "github.com/qinqon/kube-admission-webhook/pkg/webhook/server"
	"github.com/qinqon/kube-admission-webhook/pkg/webhook/server/certificate"
	admissionregistration "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

const (
	WebhookServerPort = 8000
)

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations;validatingwebhookconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;update;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;create;update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get;list
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;create;update;patch;list;watch
// +kubebuilder:rbac:groups="kubevirt.io",resources=virtualmachines,verbs=get;list;watch;create;update;patch
var AddToWebhookFuncs []func(*webhookserver.Server, *pool_manager.PoolManager) error

// AddToManager adds all Controllers to the Manager
func AddToManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	s := webhookserver.New(mgr.GetClient(), names.MUTATE_WEBHOOK_CONFIG, certificate.MutatingWebhook, webhookserver.WithPort(WebhookServerPort))

	for _, f := range AddToWebhookFuncs {
		if err := f(s, poolManager); err != nil {
			return err
		}
	}

	s.Add(mgr)
	return nil
}

// This function creates or update the mutating webhook to add an owner reference
// pointing the statefulset object of the manager.
// This way when we remove the controller from the cluster it will also remove this webhook to allow the creating
// of new pods and virtual machines objects.
// We choose this solution because the sigs.k8s.io/controller-runtime package doesn't allow to customize
// the ServerOptions object
func CreateOwnerRefForMutatingWebhook(kubeClient *kubernetes.Clientset, managerNamespace string) error {
	ownerRefList, err := createDeploymentOwnerRef(kubeClient, managerNamespace)
	if err != nil {
		return err
	}

	mutatingWebHookObject, err := kubeClient.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Get(names.MUTATE_WEBHOOK_CONFIG, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			mutatingWebHookObject = &admissionregistration.MutatingWebhookConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: fmt.Sprintf("%s/%s", admissionregistration.GroupName, "v1beta1"),
					Kind:       "MutatingWebhookConfiguration",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            names.MUTATE_WEBHOOK_CONFIG,
					OwnerReferences: ownerRefList,
				}}
			_, err = kubeClient.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Create(mutatingWebHookObject)
			return err
		}
		return err
	}

	mutatingWebHookObject.OwnerReferences = ownerRefList
	_, err = kubeClient.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Update(mutatingWebHookObject)
	return err
}

func CreateOwnerRefForService(kubeClient *kubernetes.Clientset, managerNamespace string) error {
	ownerRefList, err := createDeploymentOwnerRef(kubeClient, managerNamespace)
	if err != nil {
		return err
	}

	svcObject, err := kubeClient.CoreV1().Services(managerNamespace).Get(names.WEBHOOK_SERVICE, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			svcObject = &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:            names.WEBHOOK_SERVICE,
					OwnerReferences: ownerRefList,
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{
						names.LEADER_LABEL: "true",
					}, Ports: []corev1.ServicePort{{
						Port: 443,
						TargetPort: intstr.IntOrString{
							Type:   intstr.Int,
							IntVal: WebhookServerPort,
						}}}}}
			_, err = kubeClient.CoreV1().Services(managerNamespace).Create(svcObject)
			return err
		}
		return err
	}

	svcObject.OwnerReferences = ownerRefList
	_, err = kubeClient.CoreV1().Services(managerNamespace).Update(svcObject)
	return err
}

func createDeploymentOwnerRef(kubeClient *kubernetes.Clientset, managerNamespace string) ([]metav1.OwnerReference, error) {
	managerDeployment, err := kubeClient.AppsV1().Deployments(managerNamespace).Get(names.MANAGER_DEPLOYMENT, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	ownerRefList := []metav1.OwnerReference{{Name: managerDeployment.Name, Kind: "Deployment", APIVersion: "apps/v1", UID: managerDeployment.UID}}

	return ownerRefList, nil
}
