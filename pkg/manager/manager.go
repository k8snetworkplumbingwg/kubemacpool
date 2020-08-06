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
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	kubevirt_api "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/webhook"
)

var log logr.Logger

type KubeMacPoolManager struct {
	clientset                *kubernetes.Clientset
	config                   *rest.Config
	metricsAddr              string
	continueToRunManager     bool
	restartChannel           chan struct{}   // Close the channel if we need to regenerate certs
	kubevirtInstalledChannel chan struct{}   // This channel is close after we found kubevirt to reload the manager
	stopSignalChannel        chan os.Signal  // stop channel signal
	podNamespace             string          // manager pod namespace
	podName                  string          // manager pod name
	waitingTime              int             // Duration in second to free macs of allocated vms that failed to start.
	runtimeManager           manager.Manager // Delegated controller-runtime manager
}

func NewKubeMacPoolManager(podNamespace, podName, metricsAddr string, waitingTime int) *KubeMacPoolManager {
	kubemacpoolManager := &KubeMacPoolManager{
		continueToRunManager:     true,
		restartChannel:           make(chan struct{}),
		kubevirtInstalledChannel: make(chan struct{}),
		stopSignalChannel:        make(chan os.Signal, 1),
		podNamespace:             podNamespace,
		podName:                  podName,
		metricsAddr:              metricsAddr,
		waitingTime:              waitingTime}

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
		err = k.initRuntimeManager()
		if err != nil {
			return errors.Wrap(err, "unable to set up manager")
		}

		err = kubevirt_api.AddToScheme(k.runtimeManager.GetScheme())
		if err != nil {
			return errors.Wrap(err, "unable to register kubevirt scheme")
		}

		isKubevirtInstalled := checkForKubevirt(k.clientset)
		poolManager, err := poolmanager.NewPoolManager(k.clientset, rangeStart, rangeEnd, k.podNamespace, isKubevirtInstalled, k.waitingTime)
		if err != nil {
			return errors.Wrap(err, "unable to create pool manager")
		}

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

		err = poolManager.Start()
		if err != nil {
			return errors.Wrap(err, "failed to start pool manager routines")
		}

		err = k.runtimeManager.Start(k.restartChannel)
		if err != nil {
			log.Error(err, "unable to run the manager")
		}

		// restart channels
		k.restartChannel = make(chan struct{})
		k.kubevirtInstalledChannel = make(chan struct{})
	}

	return nil
}

func checkForKubevirt(kubeClient *kubernetes.Clientset) bool {
	result := kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI("/apis/apiextensions.k8s.io/v1beta1/customresourcedefinitions/virtualmachines.kubevirt.io").Do(context.TODO())
	if result.Error() == nil {
		return true
	}

	return false
}

func (k *KubeMacPoolManager) initRuntimeManager() error {
	log.Info("Setting up Manager")
	var err error
	k.runtimeManager, err = manager.New(k.config, manager.Options{
		MetricsBindAddress: k.metricsAddr,
	})

	return err
}

// Check for Kubevirt CRD to be available
func (k *KubeMacPoolManager) waitForKubevirt() {
	for _ = range time.Tick(5 * time.Second) {
		kubevirtExist := checkForKubevirt(k.clientset)
		log.V(1).Info("kubevirt exist in the cluster", "kubevirtExist", kubevirtExist)
		if kubevirtExist {
			close(k.kubevirtInstalledChannel)
			break
		}
	}
}

// wait for the any interrupt to stop the manager and clean the webhook.
func (k *KubeMacPoolManager) waitForSignal() {
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

	close(k.restartChannel)
}

func init() {
	log = logf.Log.WithName("manager")
}
