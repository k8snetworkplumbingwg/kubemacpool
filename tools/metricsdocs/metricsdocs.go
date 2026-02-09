/*
Copyright 2026 The KubeMacPool Authors.

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
	"fmt"

	"github.com/rhobs/operator-observability-toolkit/pkg/docs"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/metrics"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/rules"
)

const title = "KubeMacPool Metrics"

func main() {
	if err := metrics.SetupMetrics(); err != nil {
		panic(err)
	}

	if err := rules.SetupRules(); err != nil {
		panic(err)
	}

	fmt.Print(docs.BuildMetricsDocs(title, metrics.ListMetrics(), rules.ListRecordingRules()))
}
