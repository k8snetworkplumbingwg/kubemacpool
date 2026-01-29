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
	"encoding/json"
	"fmt"
	"strings"

	dto "github.com/prometheus/client_model/go"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/metrics"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/rules"
	"github.com/rhobs/operator-observability-toolkit/pkg/operatormetrics"
)

type RecordingRule struct {
	Record string `json:"record,omitempty"`
	Expr   string `json:"expr,omitempty"`
	Type   string `json:"type,omitempty"`
}

type Output struct {
	MetricFamilies []*dto.MetricFamily `json:"metricFamilies,omitempty"`
	RecordingRules []RecordingRule     `json:"recordingRules,omitempty"`
}

// Metrics excluded from the linter checks.
var excludedMetrics = map[string]bool{
	"kubevirt_kmp_duplicate_macs": true,
}

func main() {
	if err := metrics.SetupMetrics(); err != nil {
		panic(err)
	}

	if err := rules.SetupRules(); err != nil {
		panic(err)
	}

	var metricFamilies []*dto.MetricFamily
	for _, metric := range metrics.ListMetrics() {
		if excludedMetrics[metric.GetOpts().Name] {
			continue
		}
		metricFamilies = append(metricFamilies, buildMetricFamily(metric))
	}

	var recordingRules []RecordingRule
	for _, rule := range rules.ListRecordingRules() {
		if excludedMetrics[rule.GetOpts().Name] {
			continue
		}
		recordingRules = append(recordingRules, RecordingRule{
			Record: rule.GetOpts().Name,
			Expr:   rule.Expr.String(),
			Type:   strings.ToUpper(string(rule.GetType())),
		})
	}

	out := Output{
		MetricFamilies: metricFamilies,
		RecordingRules: recordingRules,
	}

	jsonBytes, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(jsonBytes))
}

func buildMetricFamily(metric operatormetrics.Metric) *dto.MetricFamily {
	name := metric.GetOpts().Name
	help := metric.GetOpts().Help
	metricType := strings.ToUpper(string(metric.GetBaseType()))
	promMetricType := dto.MetricType_UNTYPED
	if v, ok := dto.MetricType_value[metricType]; ok {
		promMetricType = dto.MetricType(v)
	}
	return &dto.MetricFamily{
		Name: &name,
		Help: &help,
		Type: &promMetricType,
	}
}
