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
	"github.com/qinqon/kube-admission-webhook/pkg/certificate"
	webhookserver "github.com/qinqon/kube-admission-webhook/pkg/webhook/server"
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
