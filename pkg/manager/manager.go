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
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
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
	restartChannel           chan struct{}  // Close the channel if we need to regenerate certs
	kubevirtInstalledChannel chan struct{}  // This channel is close after we found kubevirt to reload the manager
	stopSignalChannel        chan os.Signal // stop channel signal
	leaderElectionChannel    chan error     // Channel used by the leader election function send error if we lose the election
	podNamespace             string         // manager pod namespace
	podName                  string         // manager pod name
	waitingTime              int            // Duration in second to lock a mac address before it was saved to etcd
	leaderElection           *leaderelection.LeaderElector
}

func NewKubeMacPoolManager(podNamespace, podName, metricsAddr string, waitingTime int) *KubeMacPoolManager {
	kubemacpoolManager := &KubeMacPoolManager{
		continueToRunManager:     true,
		restartChannel:           make(chan struct{}),
		kubevirtInstalledChannel: make(chan struct{}),
		stopSignalChannel:        make(chan os.Signal, 1),
		leaderElectionChannel:    make(chan error, 1),
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
		return fmt.Errorf("unable to set up client config error %v", err)
	}

	k.clientset, err = kubernetes.NewForConfig(k.config)
	if err != nil {
		return fmt.Errorf("unable to create a kubernetes client error %v", err)
	}

	for k.continueToRunManager {
		log.Info("waiting for manager to become leader")
		err = k.waitToStartLeading()
		if err != nil {
			log.Error(err, "failed to wait for leader election")
			continue
		}
		log.Info("Setting up Manager")
		mgr, err := manager.New(k.config, manager.Options{
			MetricsBindAddress: k.metricsAddr,
		})
		if err != nil {
			return fmt.Errorf("unable to set up manager error %v", err)
		}

		err = kubevirt_api.AddToScheme(mgr.GetScheme())
		if err != nil {
			return fmt.Errorf("unable to register kubevirt scheme error %v", err)
		}

		isKubevirtInstalled := checkForKubevirt(k.clientset)
		poolManager, err := poolmanager.NewPoolManager(k.clientset, rangeStart, rangeEnd, k.podNamespace, isKubevirtInstalled, k.waitingTime)
		if err != nil {
			return fmt.Errorf("unable to create pool manager error %v", err)
		}

		if !isKubevirtInstalled {
			log.Info("kubevirt was not found in the cluster start a watching process")
			go k.waitForKubevirt()
		}
		go k.waitForSignal()

		log.Info("Setting up controller")
		err = controller.AddToManager(mgr, poolManager)
		if err != nil {
			return fmt.Errorf("unable to register controllers to the manager error %v", err)
		}

		err = webhook.AddToManager(mgr, poolManager)
		if err != nil {
			return fmt.Errorf("unable to register webhooks to the manager error %v", err)
		}

		err = mgr.Start(k.restartChannel)
		if err != nil {
			return fmt.Errorf("unable to run the manager error %v", err)
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
		k.continueToRunManager = false
	// This interrupt occurred when kubevirt is installed in the cluster and will restart the manager code only
	// The container will not restart in this scenario
	case <-k.kubevirtInstalledChannel:
		log.Info("found kubevirt restarting the manager")
	// This interrupt occurred if we load the leader election for the manager and we need to restart it
	// This will wait to get the election lock again
	case <-k.restartChannel:
		log.Info("leader election lost")
	}

	close(k.restartChannel)
}

func init() {
	log = logf.Log.WithName("manager")
}
