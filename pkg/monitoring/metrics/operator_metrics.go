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

package metrics

import "github.com/rhobs/operator-observability-toolkit/pkg/operatormetrics"

var (
	// Deprecated: this metric is deprecated and will be removed in a future release.
	// Use macCollisionMetric instead.
	duplicateMacsMetric = operatormetrics.NewCounter(
		operatormetrics.MetricOpts{
			Name: "kubevirt_kmp_duplicate_macs",
			Help: "[DEPRECATED] Total count of duplicate KubeMacPool MAC addresses. Use kmp_mac_collisions instead.",
		},
	)

	macCollisionMetric = operatormetrics.NewGaugeVec(
		operatormetrics.MetricOpts{
			Name: "kmp_mac_collisions",
			Help: "Count of running objects sharing the same MAC address (collision when > 1)",
		},
		[]string{"mac"},
	)
)

var operatorMetrics = []operatormetrics.Metric{
	duplicateMacsMetric,
	macCollisionMetric,
}
