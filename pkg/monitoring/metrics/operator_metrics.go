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
