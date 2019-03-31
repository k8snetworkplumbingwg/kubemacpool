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
	"sigs.k8s.io/controller-runtime/pkg/manager"
	runtimewebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
	apitypes "k8s.io/apimachinery/pkg/types"
)

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
var AddToManagerFuncs []func(manager.Manager, *pool_manager.PoolManager) (*admission.Webhook, error)

// AddToManager adds all Controllers to the Manager
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations;validatingwebhookconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;update;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;create;update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get;list
// +kubebuilder:rbac:groups="kubevirt.io",resources=virtualmachines,verbs=get;list;watch;create;update;patch
func AddToManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {
	svr, err := runtimewebhook.NewServer("kubemacpool-webhook", mgr, runtimewebhook.ServerOptions{
		CertDir: "/tmp/cert",
		BootstrapOptions: &runtimewebhook.BootstrapOptions{
			Secret: &apitypes.NamespacedName{
				Namespace: "kubemacpool-system",
				Name:      "kubemacpool-webhook-secret",
			},
			Service: &runtimewebhook.Service{
				Namespace: "kubemacpool-system",
				Name:      "kubemacpool-service",
				// Selectors should select the pods that runs this webhook server.
				Selectors: map[string]string{
					"control-plane": "mac-controller-manager",
				},
			},
		},
	})
	if err != nil {
		return err
	}

	webhooks := []runtimewebhook.Webhook{}
	for _, f := range AddToManagerFuncs {
		if webhooktoRegister, err := f(mgr, poolManager); err != nil {
			return err
		} else if webhooktoRegister != nil {
			webhooks = append(webhooks, webhooktoRegister)
		}
	}

	err = svr.Register(webhooks...)
	if err != nil {
		return err
	}

	return nil
}
