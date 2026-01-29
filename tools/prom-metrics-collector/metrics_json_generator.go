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
	metricType := toPromType(metric.GetBaseType())
	return &dto.MetricFamily{
		Name: &name,
		Help: &help,
		Type: &metricType,
	}
}

func toPromType(metricType operatormetrics.MetricType) dto.MetricType {
	switch metricType {
	case operatormetrics.CounterType:
		return dto.MetricType_COUNTER
	case operatormetrics.GaugeType:
		return dto.MetricType_GAUGE
	case operatormetrics.HistogramType:
		return dto.MetricType_HISTOGRAM
	case operatormetrics.SummaryType:
		return dto.MetricType_SUMMARY
	default:
		return dto.MetricType_UNTYPED
	}
}
