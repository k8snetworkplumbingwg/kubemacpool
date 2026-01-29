/*
Copyright 2025 The KubeMacPool Authors.

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

package rules

import (
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/rhobs/operator-observability-toolkit/pkg/operatorrules"
)

const (
	PrometheusRuleName = "prometheus-rule"
)

func SetupRules() error {
	return nil
}

func BuildPrometheusRule(namespace string) (*promv1.PrometheusRule, error) {
	return operatorrules.BuildPrometheusRule(
		PrometheusRuleName,
		namespace,
		map[string]string{
			"prometheus.kubemacpool.io":                     "true",
			"openshift.io/prometheus-rule-evaluation-scope": "leaf-prometheus",
		},
	)
}

func ListAlerts() []promv1.Rule {
	return operatorrules.ListAlerts()
}

func ListRecordingRules() []operatorrules.RecordingRule {
	return operatorrules.ListRecordingRules()
}
