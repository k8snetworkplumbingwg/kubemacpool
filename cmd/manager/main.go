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

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/manager"
	poolmanager "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
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

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&logType, "v", "production", "Log type (debug/production).")
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

	rangeEnd, err := loadMacAddressFromEnvVar(poolmanager.RangeEndEvn)
	if err != nil {
		log.Error(err, "Failed to load mac address from environment variable")
		os.Exit(1)
	}

	kubemacpoolManager := manager.NewKubeMacPoolManager(metricsAddr)

	err = kubemacpoolManager.Run(rangeStart, rangeEnd)
	if err != nil {
		os.Exit(1)
	}
}
