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
	"time"

	"github.com/pkg/errors"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/manager"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"

	"github.com/qinqon/kube-admission-webhook/pkg/certificate"
)

func loadMacAddressFromEnvVar(envName string) (net.HardwareAddr, error) {
	if value, ok := os.LookupEnv(envName); ok {
		poolRange, err := net.ParseMAC(value)
		if err != nil {
			return nil, err
		}

		return poolRange, nil
	}

	return nil, fmt.Errorf("Environment variable %s don't exist", envName)
}

func main() {
	var logType, metricsAddr string
	var waitingTime int

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&logType, "v", "production", "Log type (debug/production).")
	flag.IntVar(&waitingTime, names.WAIT_TIME_ARG, 600, "waiting time to release the mac if object was not created")
	flag.Parse()

	if logType == "debug" {
		logf.SetLogger(logf.ZapLogger(true))
	} else {
		logf.SetLogger(logf.ZapLogger(false))
	}

	log := logf.Log.WithName("main")

	rangeStart, err := loadMacAddressFromEnvVar(poolmanager.RangeStartEnv)
	if err != nil {
		log.Error(err, "Failed to load mac address from environment variable")
		os.Exit(1)
	}

	rangeEnd, err := loadMacAddressFromEnvVar(poolmanager.RangeEndEnv)
	if err != nil {
		log.Error(err, "Failed to load mac address from environment variable")
		os.Exit(1)
	}

	podNamespace, ok := os.LookupEnv("POD_NAMESPACE")
	if !ok {
		log.Error(err, "Failed to load pod namespace from environment variable")
		os.Exit(1)
	}

	podName, ok := os.LookupEnv("POD_NAME")
	if !ok {
		log.Error(err, "Failed to load pod name from environment variable")
		os.Exit(1)
	}

	caRotateInterval, err := lookupEnvAsDuration("CA_ROTATE_INTERVAL")
	if err != nil {
		log.Error(err, "Failed retrieving ca rotate interval")
		os.Exit(1)
	}

	caOverlapInterval, err := lookupEnvAsDuration("CA_OVERLAP_INTERVAL")
	if err != nil {
		log.Error(err, "Failed retrieving ca overlap interval")
		os.Exit(1)
	}

	certRotateInterval, err := lookupEnvAsDuration("CERT_ROTATE_INTERVAL")
	if err != nil {
		log.Error(err, "Failed retrieving cert rotate interval")
		os.Exit(1)
	}

	certOptions := certificate.Options{
		Namespace:          podNamespace,
		WebhookName:        names.MUTATE_WEBHOOK_CONFIG,
		WebhookType:        certificate.MutatingWebhook,
		CARotateInterval:   caRotateInterval,
		CAOverlapInterval:  caOverlapInterval,
		CertRotateInterval: certRotateInterval,
	}
	kubemacpoolManager := manager.NewKubeMacPoolManager(podNamespace, podName, metricsAddr, waitingTime, certOptions)

	err = kubemacpoolManager.Run(rangeStart, rangeEnd)
	if err != nil {
		log.Error(err, "Failed to run the kubemacpool manager")
		os.Exit(1)
	}
}

func lookupEnvAsDuration(varName string) (time.Duration, error) {
	duration := time.Duration(0)
	varValue, ok := os.LookupEnv(varName)
	if !ok {
		return duration, errors.Errorf("Failed to load %s from environment", varName)
	}

	duration, err := time.ParseDuration(varValue)
	if err != nil {
		return duration, errors.Wrapf(err, "Failed to convert %s value to time.Duration", varName)
	}
	return duration, nil
}
