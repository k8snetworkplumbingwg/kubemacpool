package metrics

import "github.com/rhobs/operator-observability-toolkit/pkg/operatormetrics"

var (
	duplicateMacsMetric = operatormetrics.NewCounter(
		operatormetrics.MetricOpts{
			Name: "kubevirt_kmp_duplicate_macs",
			Help: "Kubemacpool duplicate macs counter",
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
