package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/metrics"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/rules"
)

const title = "KubeMacPool Metrics"

type docRow struct {
	Name        string
	Kind        string
	Type        string
	Description string
}

func main() {
	if err := metrics.SetupMetrics(); err != nil {
		panic(err)
	}

	if err := rules.SetupRules(); err != nil {
		panic(err)
	}

	rows := collectRows()
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	fmt.Printf("# %s\n\n", title)
	fmt.Println("| Name | Kind | Type | Description |")
	fmt.Println("|------|------|------|-------------|")
	for _, row := range rows {
		fmt.Printf("| %s | %s | %s | %s |\n",
			row.Name,
			row.Kind,
			row.Type,
			sanitizeCell(row.Description),
		)
	}

	fmt.Println()
	fmt.Println("## Developing new metrics")
	fmt.Println()
	fmt.Println("All metrics documented here are auto-generated and reflect exactly what is being")
	fmt.Println("exposed. After developing new metrics or changing old ones please regenerate")
	fmt.Println("this document.")
}

func collectRows() []docRow {
	var rows []docRow
	for _, metric := range metrics.ListMetrics() {
		rows = append(rows, docRow{
			Name:        metric.GetOpts().Name,
			Kind:        "Metric",
			Type:        string(metric.GetBaseType()),
			Description: metric.GetOpts().Help,
		})
	}

	for _, rule := range rules.ListRecordingRules() {
		rows = append(rows, docRow{
			Name:        rule.GetOpts().Name,
			Kind:        "Recording rule",
			Type:        string(rule.GetType()),
			Description: rule.GetOpts().Help,
		})
	}

	return rows
}

func sanitizeCell(value string) string {
	cleaned := strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(cleaned)
}
