/*
Copyright 2019 The KubeMacPool Authors.

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
package manager

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/rhobs/operator-observability-toolkit/pkg/operatormetrics"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kubevirt_api "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller/maccollision"
	monitoringmetrics "github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/metrics"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	kmptls "github.com/k8snetworkplumbingwg/kubemacpool/pkg/tls"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/webhook"
)

var log logr.Logger

type KubeMacPoolManager struct {
	clientset                *kubernetes.Clientset
	config                   *rest.Config
	metricsAddr              string
	continueToRunManager     bool
	kubevirtInstalledChannel chan struct{}      // This channel is close after we found kubevirt to reload the manager
	stopSignalChannel        chan os.Signal     // stop channel signal
	cancel                   context.CancelFunc // cancel() closes the controller-runtime context internal channel
	podNamespace             string             // manager pod namespace
	podName                  string             // manager pod name
	waitingTime              int                // Duration in second to free macs of allocated vms that failed to start.
	runtimeManager           manager.Manager    // Delegated controller-runtime manager
	// tlsConfig contains TLS settings for servers (e.g.: webhook and metrics servers)
	tlsConfig kmptls.Config
}

func NewKubeMacPoolManager(
	podNamespace string,
	podName string,
	metricsAddr string,
	waitingTime int,
	tlsConfig kmptls.Config,
) *KubeMacPoolManager {
	kubemacpoolManager := &KubeMacPoolManager{
		continueToRunManager:     true,
		kubevirtInstalledChannel: make(chan struct{}),
		stopSignalChannel:        make(chan os.Signal, 1),
		podNamespace:             podNamespace,
		podName:                  podName,
		metricsAddr:              metricsAddr,
		waitingTime:              waitingTime,
		tlsConfig:                tlsConfig,
	}

	signal.Notify(kubemacpoolManager.stopSignalChannel, os.Interrupt, os.Kill)

	return kubemacpoolManager
}

func (k *KubeMacPoolManager) Run(rangeStart, rangeEnd net.HardwareAddr) error {
	// Get a config to talk to the apiserver
	var err error
	log.Info("Setting up client for manager")
	k.config, err = config.GetConfig()
	if err != nil {
		return errors.Wrap(err, "unable to set up client config")
	}

	k.clientset, err = kubernetes.NewForConfig(k.config)
	if err != nil {
		return errors.Wrap(err, "unable to create a kubernetes client")
	}

	for k.continueToRunManager {
		isKubevirtInstalled := checkForKubevirt(k.clientset.Discovery())

		err = k.initRuntimeManager(isKubevirtInstalled)
		if err != nil {
			return errors.Wrap(err, "unable to set up manager")
		}

		log.Info("Building client")

		client, err := client.New(k.config, client.Options{
			Scheme: k.runtimeManager.GetScheme(),
			Mapper: k.runtimeManager.GetRESTMapper(),
		})
		if err != nil {
			return errors.Wrap(err, "failed creating pool manager client")
		}
		poolManager, err := poolmanager.NewPoolManager(
			client, client,
			rangeStart, rangeEnd,
			k.podNamespace,
			isKubevirtInstalled,
			k.waitingTime,
			poolmanager.WithTLSConfig(k.tlsConfig))
		if err != nil {
			return errors.Wrap(err, "unable to create pool manager")
		}

		var ctx context.Context
		ctx, k.cancel = context.WithCancel(context.Background())
		if !isKubevirtInstalled {
			log.Info("kubevirt was not found in the cluster start a watching process")
			go k.waitForKubevirt()
		}
		go k.waitForSignal()

		log.Info("Setting up controllers")
		err = controller.AddToManager(k.runtimeManager, poolManager)
		if err != nil {
			return errors.Wrap(err, "unable to register controllers to the manager")
		}

		log.Info("Setting up webhooks")
		err = webhook.AddToManager(k.runtimeManager, poolManager)
		if err != nil {
			return errors.Wrap(err, "unable to register webhooks to the manager")
		}

		log.Info("Setting up Pool Manager")
		go func() {
			err := poolManager.Start()
			if err != nil {
				log.Error(err, "failed to start pool manager routines")
				k.cancel()
				return
			}
		}()

		log.Info("Starting Runtime Manager")
		err = k.runtimeManager.Start(ctx)
		if err != nil {
			log.Error(err, "unable to run the manager")
		}

		// restart channels
		k.kubevirtInstalledChannel = make(chan struct{})
	}

	return nil
}

func checkForKubevirt(discoveryClient discovery.DiscoveryInterface) bool {
	resourceList, err := discoveryClient.ServerResourcesForGroupVersion("kubevirt.io/v1")
	if err != nil {
		return false
	}
	for _, resource := range resourceList.APIResources {
		if resource.Name == "virtualmachines" {
			return true
		}
	}
	return false
}

func (k *KubeMacPoolManager) initRuntimeManager(isKubevirtInstalled bool) error {
	log.Info("Setting up Manager", "isKubevirtInstalled", isKubevirtInstalled)
	var err error

	// Register kubevirt types in the scheme before creating the manager
	// This is required because we reference VMI in the cache config when kubevirt is installed
	if err := kubevirt_api.AddToScheme(scheme.Scheme); err != nil {
		return errors.Wrap(err, "unable to register kubevirt scheme")
	}

	configMapNameFieldSelector := fields.OneTermEqualSelector("metadata.name", names.MAC_RANGE_CONFIGMAP)

	cacheOptions := cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.ConfigMap{}: {
				Namespaces: map[string]cache.Config{
					k.podNamespace: {},
				},
				Field: configMapNameFieldSelector,
			},
			&corev1.Pod{}: {
				Transform: maccollision.StripPodForCollisionDetection,
			},
		},
	}

	// Only configure VMI cache when kubevirt is installed, otherwise
	// manager.New() fails on REST mapping discovery for the missing CRD
	// When kubevirt is installed then the Manager will restart.
	if isKubevirtInstalled {
		cacheOptions.ByObject[&kubevirt_api.VirtualMachineInstance{}] = cache.ByObject{
			Transform: maccollision.StripVMIForCollisionDetection,
		}
	}

	k.runtimeManager, err = manager.New(k.config, manager.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress:    k.metricsAddr,
			SecureServing:  true,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
			TLSOpts: []func(*tls.Config){func(cfg *tls.Config) {
				cfg.CipherSuites = k.tlsConfig.CipherSuites
				cfg.MinVersion = k.tlsConfig.MinTLSVersion
			}},
		},
		Cache: cacheOptions,
	})
	if err != nil {
		return err
	}

	// Ensure operator-observability-toolkit metrics are registered in the same registry
	// that controller-runtime exposes on /metrics.
	operatormetrics.Register = crmetrics.Registry.Register
	operatormetrics.Unregister = crmetrics.Registry.Unregister

	if err := monitoringmetrics.SetupMetrics(); err != nil {
		return errors.Wrap(err, "failed to register monitoring metrics")
	}

	return nil
}

// Check for Kubevirt CRD to be available
func (k *KubeMacPoolManager) waitForKubevirt() {
	for _ = range time.Tick(5 * time.Second) {
		kubevirtExist := checkForKubevirt(k.clientset.Discovery())
		log.V(1).Info("kubevirt exist in the cluster", "kubevirtExist", kubevirtExist)
		if kubevirtExist {
			close(k.kubevirtInstalledChannel)
			break
		}
	}
}

// wait for the any interrupt to stop the manager and clean the webhook.
func (k *KubeMacPoolManager) waitForSignal() {
	defer k.cancel()

	select {
	// This channel is a system interrupt this will stop the container
	case <-k.stopSignalChannel:
		log.Info("received stop signal interrupt. exiting.")
		k.continueToRunManager = false
	// This interrupt occurred when kubevirt is installed in the cluster and will restart the manager code only
	// The container will not restart in this scenario
	case <-k.kubevirtInstalledChannel:
		log.Info("found kubevirt restarting the manager")
	}
}

func init() {
	log = logf.Log.WithName("manager")
}
