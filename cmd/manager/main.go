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

package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/controller"
	poolmanager "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
	"github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/webhook"
)

var log logr.Logger
var metricsAddr string
var restartChannel = make(chan struct{})

func loadMacAddressFromEnvVar(envName string) net.HardwareAddr {
	if value, ok := os.LookupEnv(envName); ok {
		poolRange, err := net.ParseMAC(value)
		if err != nil {
			log.Error(err, "Failed to parse mac address", "mac address", value)
			os.Exit(1)
		}

		return poolRange
	}

	log.Error(fmt.Errorf("Environment variable %s don't exist", envName), "")
	os.Exit(1)

	// with out the the compilation failed
	return nil
}

func checkForKubevirt(kubeClient *kubernetes.Clientset) bool {
	result := kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI("/apis/apiextensions.k8s.io/v1beta1/customresourcedefinitions/virtualmachines.kubevirt.io").Do()
	if result.Error() == nil {
		return true
	}

	return false
}

// Check for Kubevirt CRD to be available
func waitForKubevirt(kubeClient *kubernetes.Clientset) {
	for _ = range time.Tick(5 * time.Second) {
		kubevirtExist := checkForKubevirt(kubeClient)
		log.V(1).Info("kubevirt exist in the cluster", "kubevirtExist", kubevirtExist)
		if kubevirtExist {
			log.Info("found kubevirt restarting the manager")
			close(restartChannel)
			break
		}
	}
}

// wait for the any interrupt to stop the manager and clean the webhook.
func waitForSignal() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c
	close(restartChannel)
}

func main() {
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	var logType string
	flag.StringVar(&logType, "v", "production", "Log type (debug/production).")
	flag.Parse()

	if logType == "debug" {
		logf.SetLogger(logf.ZapLogger(true))
	} else {
		logf.SetLogger(logf.ZapLogger(false))
	}
	log = logf.Log.WithName("Main")

	// Get a config to talk to the apiserver
	log.Info("Setting up client for manager")
	cfg, err := config.GetConfig()
	ExitIfError(err, "unable to set up client config")

	clientset, err := kubernetes.NewForConfig(cfg)
	ExitIfError(err, "unable to create a kubernetes client")
	// setup Pool Manager
	startPoolRange := loadMacAddressFromEnvVar(poolmanager.StartPoolRangeEnv)
	endPoolRange := loadMacAddressFromEnvVar(poolmanager.EndPoolRangeEnv)

	isKubevirtInstalled := checkForKubevirt(clientset)
	poolManager, err := poolmanager.NewPoolManager(clientset, startPoolRange, endPoolRange, isKubevirtInstalled)
	ExitIfError(err, "unable to create pool manager")
	if !isKubevirtInstalled {
		log.Info("kubevirt was not found in the cluster start a watching process")
		go waitForKubevirt(clientset)
	}
	go waitForSignal()

	log.Info("Setting up manager")
	mgr, err := manager.New(cfg, manager.Options{MetricsBindAddress: metricsAddr,
		LeaderElection:          true,
		LeaderElectionID:        "kubemacpool-mac-election",
		LeaderElectionNamespace: "kubemacpool-system"})
	ExitIfError(err, "unable to set up manager")

	log.Info("Setting up controller")
	ExitIfError(controller.AddToManager(mgr, poolManager), "Unable to register controllers to the manager")

	ExitIfError(webhook.AddToManager(mgr, poolManager), "Unable to register webhooks to the manager")

	ExitIfError(mgr.Start(restartChannel), "Unable to run the manager")
}

func ExitIfError(err error, message string) {
	if err != nil {
		log.Error(err, message)
		os.Exit(1)
	}
}
