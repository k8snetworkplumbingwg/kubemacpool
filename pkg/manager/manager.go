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
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	kubevirt_api "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/controller"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
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

func (k *KubeMacPoolManager) Run(rangeStart, rangeEnd net.HardwareAddr, shardingFactor int) error {
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

	log.Info("Setting up leader electionManager")
	leaderElectionManager, err := manager.New(k.config, manager.Options{MetricsBindAddress: k.metricsAddr})
	if err != nil {
		return fmt.Errorf("unable to set up manager error %v", err)
	}

	err = k.newLeaderElection(k.config, leaderElectionManager.GetScheme())
	if err != nil {
		return fmt.Errorf("unable to create a leader election resource lock error %v", err)
	}

	for k.continueToRunManager {
		log.Info("waiting for manager to become leader")
		err = k.waitToStartLeading()
		if err != nil {
			log.Error(err, "failed to wait for leader election")
			continue
		}

		healthProbeHost, ok := os.LookupEnv("HEALTH_PROBE_HOST")
		if !ok {
			log.Error(err, "Failed to load HEALTH_PROBE_HOST from environment variable")
			os.Exit(1)
		}

		healthProbePort, ok := os.LookupEnv("HEALTH_PROBE_PORT")
		if !ok {
			log.Error(err, "Failed to load HEALTH_PROBE_PORT from environment variable")
			os.Exit(1)
		}

		log.Info("Setting up Manager")
		mgr, err := manager.New(k.config, manager.Options{
			MetricsBindAddress:     k.metricsAddr,
			HealthProbeBindAddress: fmt.Sprintf("%s:%s", healthProbeHost, healthProbePort),
		})
		if err != nil {
			return fmt.Errorf("unable to set up manager error %v", err)
		}

		err = kubevirt_api.AddToScheme(mgr.GetScheme())
		if err != nil {
			return fmt.Errorf("unable to register kubevirt scheme error %v", err)
		}

		// create a owner ref on the mutating webhook
		// this way when we remove the deployment of the manager the webhook will be also removed from the cluster
		err = webhook.CreateOwnerRefForMutatingWebhook(k.clientset, k.podNamespace)
		if err != nil {
			return fmt.Errorf("unable to create owner reference for mutating webhook object error %v", err)
		}

		err = webhook.CreateOwnerRefForService(k.clientset, k.podNamespace)
		if err != nil {
			return fmt.Errorf("unable to create owner reference for service object error %v", err)
		}

		macRangeShard, err := NewMacRangeShard(k.podName, rangeStart.String(), rangeEnd.String(), shardingFactor)
		if err != nil {
			return fmt.Errorf("failed to create mac range shard, %s", err)
		}
		// update the mac range shard managed by the current pod
		err = k.updateMacRangeConfigMapData(names.MAC_RANGE_CONFIG_CONFIGMAP, names.MANAGER_NAMESPACE, macRangeShard.ShardName, macRangeShard)
		if err != nil {
			return fmt.Errorf("failed to update %s configmap with the shard range details, %s",names.MAC_RANGE_CONFIG_CONFIGMAP, err)
		}
		log.Info(fmt.Sprintf("%s configmap updated", names.MAC_RANGE_CONFIG_CONFIGMAP))
		// update sharding factor environment variable
		err = os.Setenv(poolmanager.ShardingFactor, macRangeShard.shardingFactorStr)
		if err != nil {
			log.Error(err, "failed to update sharding factor env variable")
			return err
		}
		// scale the replicas to the sharding factor value
		err = k.scaleManagerPods(macRangeShard.shardingFactor)
		if err != nil {
			return fmt.Errorf("failed to update manager replicas number, %v", err)
		}
		log.Info(fmt.Sprintf("%s replicas number updated: %d", names.MANAGER_STATEFULSET, macRangeShard.shardingFactor))

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

		if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
			log.Info("Unable to create health check", "error", err)
			os.Exit(1)
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
	result := kubeClient.ExtensionsV1beta1().RESTClient().Get().RequestURI("/apis/apiextensions.k8s.io/v1beta1/customresourcedefinitions/virtualmachines.kubevirt.io").Do()
	if result.Error() == nil {
		return true
	}

	return false
}

func (k KubeMacPoolManager) updateMacRangeConfigMapData(name, namespace, key string, data MacRangeShard) error {
	configMapData := struct {
		PodName string
		Start   string
		End     string
	}{
		data.PodName,
		data.ShardStart.String(),
		data.ShardEnd.String(),
	}

	dataJson, err := json.Marshal(configMapData)
	if err != nil {
		return err
	}
	macRangesConfigMap, err := k.clientset.CoreV1().ConfigMaps(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	macRangesConfigMap.Data[key] = string(dataJson)
	macRangesConfigMap, err = k.clientset.CoreV1().ConfigMaps(namespace).Update(macRangesConfigMap)
	if err != nil {
		return err
	}
	log.Info(fmt.Sprintf("mac address shard range managed updated in %s configMap:%s", name, dataJson[:]))

	return nil
}

func (k *KubeMacPoolManager) scaleManagerPods(targetReplicas int64) error {
	data := []byte(fmt.Sprintf("[ { \"op\": \"replace\", \"path\": \"/spec/replicas\", \"value\": %d } ]", targetReplicas))
	_, err := k.clientset.AppsV1().StatefulSets(names.MANAGER_NAMESPACE).Patch(names.MANAGER_STATEFULSET, types.JSONPatchType, data)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to patch %s statefulset to %d replicas", names.MANAGER_STATEFULSET, targetReplicas))
		return err
	}

	return nil
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
