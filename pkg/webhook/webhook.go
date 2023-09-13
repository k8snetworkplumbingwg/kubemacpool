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
	"crypto/tls"
	"os"
	"strings"

	"github.com/pkg/errors"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	kawtls "github.com/qinqon/kube-admission-webhook/pkg/tls"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
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
var AddToWebhookFuncs []func(*crwebhook.Server, *pool_manager.PoolManager) error

// AddToManager adds all Controllers to the Manager
func AddToManager(mgr manager.Manager, poolManager *pool_manager.PoolManager) error {

	tlsMinVersion, err := kawtls.TLSVersion(tlsMinVersion())
	if err != nil {
		return err
	}

	s := &crwebhook.Server{
		Port: WebhookServerPort,
		TLSOpts: []func(*tls.Config){func(tlsConfig *tls.Config) {
			tlsConfig.CipherSuites = kawtls.CipherSuitesIDs(cipherSuites())
			tlsConfig.MinVersion = tlsMinVersion
		}},
	}
	s.Register("/readyz", healthz.CheckHandler{Checker: healthz.Ping})

	for _, f := range AddToWebhookFuncs {
		if err := f(s, poolManager); err != nil {
			return err
		}
	}

	err = mgr.Add(s)
	if err != nil {
		return errors.Wrap(err, "failed adding webhook server to manager")
	}
	return nil
}

// cipherSuites read the TLS handshake ciphers from a environment variable if
// empty the decision is delegated to go tls package.
func cipherSuites() []string {
	cipherSuitesEnv := os.Getenv("TLS_CIPHERS")
	if cipherSuitesEnv == "" {
		return nil
	}
	return strings.Split(cipherSuitesEnv, ",")
}

// tlsMinVersion read the TLS minimal version from environment a environment
// variable, if it's empty the webhook server will fallback to "1.0"
func tlsMinVersion() string {
	return os.Getenv("TLS_MIN_VERSION")
}
