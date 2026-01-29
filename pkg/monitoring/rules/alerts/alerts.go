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

package alerts

import (
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/rhobs/operator-observability-toolkit/pkg/operatorrules"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	severityAlertLabelKey        = "severity"
	operatorHealthImpactLabelKey = "operator_health_impact"
)

func Register() error {
	return operatorrules.RegisterAlerts(
		vmiCollisionAlerts(),
	)
}

func vmiCollisionAlerts() []promv1.Rule {
	return []promv1.Rule{
		{
			Alert: "KubemacpoolMACCollisionDetected",
			Expr:  intstr.FromString("count(kmp_mac_collisions > 1) > 0"),
			For:   promv1.DurationPointer("30s"),
			Annotations: map[string]string{
				"summary":     "MAC address collisions detected.",
				"description": "{{ $value }} MAC address(es) have collisions. Multiple running objects are using the same MAC address.",
			},
			Labels: map[string]string{
				severityAlertLabelKey:        "warning",
				operatorHealthImpactLabelKey: "warning",
			},
		},
	}
}
