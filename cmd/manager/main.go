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
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/manager"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var log = logf.Log.WithName("Main")

func main() {
	var logType, metricsAddr string
	var waitingTime int
	envVariables := map[string]string{
		"rangeStart":     poolmanager.RangeStartEnv,
		"rangeEnd":       poolmanager.RangeEndEnv,
		"shardingFactor": poolmanager.ShardingFactor,
		"podNamespace":   "POD_NAMESPACE",
		"podName":        "POD_NAME",
	}

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&logType, "v", "production", "Log type (debug/production).")
	flag.IntVar(&waitingTime, "wait-time", 600, "waiting time to release the mac if object was not created")
	flag.Parse()

	setLogLevel(logType)

	err := loadEnvironmentVariables(envVariables)
	if err != nil {
		log.Error(err, "Failed to load environment variables")
		os.Exit(1)
	}

	macRangeHwAddresses, err := parseRangeAddresses([]string{"rangeStart", "rangeEnd"}, envVariables)
	if err != nil {
		log.Error(err, "Failed to parse mac range address")
		os.Exit(1)
	}

	kubemacpoolManager := manager.NewKubeMacPoolManager(envVariables["podNamespace"], envVariables["podName"], metricsAddr, waitingTime)
	shardingFactor, err := strconv.Atoi(envVariables["shardingFactor"])
	if err != nil {
		log.Error(err, "Failed to parse sharding factor")
		os.Exit(1)
	}

	err = kubemacpoolManager.Run(macRangeHwAddresses["rangeStart"], macRangeHwAddresses["rangeEnd"], shardingFactor)
	if err != nil {
		log.Error(err, "Failed to run the manager")
		os.Exit(1)
	}
}

func setLogLevel(logType string) {
	if logType == "debug" {
		logf.SetLogger(logf.ZapLogger(true))
	} else {
		logf.SetLogger(logf.ZapLogger(false))
	}
}

func loadEnvironmentVariables(envVariables map[string]string) error {
	for envVarName, _ := range envVariables {
		envVarValue, ok := os.LookupEnv(envVariables[envVarName])
		if !ok {
			return errors.New(fmt.Sprintf("Faild to load enviroment variable %s: %s", envVarName, envVariables[envVarName]))
		}
		envVariables[envVarName] = envVarValue
	}
	return nil
}

func parseRangeAddresses(variableNames []string, variablesMap map[string]string) (map[string]net.HardwareAddr, error) {
	macAddressMap := map[string]net.HardwareAddr{}
	for _, varName := range variableNames {
		macAddress, err := net.ParseMAC(variablesMap[varName])
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %s, %s", varName, err)
		}
		macAddressMap[varName] = macAddress
	}

	return macAddressMap, nil
}
