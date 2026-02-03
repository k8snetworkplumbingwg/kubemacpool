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

var metrics = [][]operatormetrics.Metric{
	operatorMetrics,
}

func SetupMetrics() error {
	return operatormetrics.RegisterMetrics(metrics...)
}

func ListMetrics() []operatormetrics.Metric {
	return operatormetrics.ListMetrics()
}

func IncDuplicateMacs() {
	duplicateMacsMetric.Inc()
}

func SetMacCollision(mac string, count int) {
	macCollisionMetric.WithLabelValues(mac).Set(float64(count))
}

func DeleteMacCollision(mac string) {
	macCollisionMetric.DeleteLabelValues(mac)
}
