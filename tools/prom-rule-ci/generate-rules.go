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

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/rules"
)

func main() {
	namespace := flag.String("namespace", "kubemacpool-system", "Namespace for the PrometheusRule")
	outputFile := flag.String("output", "config/monitoring/prometheus-rule.yaml", "Output file path")
	flag.Parse()

	if err := rules.SetupRules(); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up rules: %v\n", err)
		os.Exit(1)
	}

	rule, err := rules.BuildPrometheusRule(*namespace)
	if err != nil {
		if err.Error() != "no registered recording rule or alert" {
			fmt.Fprintf(os.Stderr, "Error building PrometheusRule: %v\n", err)
			os.Exit(1)
		}
		rule = emptyPrometheusRule(*namespace)
	}

	yamlData, err := yaml.Marshal(rule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling PrometheusRule: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll("config/monitoring", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating monitoring directory: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outputFile, yamlData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("PrometheusRule generated successfully: %s\n", *outputFile)
}

func emptyPrometheusRule(namespace string) *v1.PrometheusRule {
	return &v1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       v1.PrometheusRuleKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rules.PrometheusRuleName,
			Namespace: namespace,
			Labels: map[string]string{
				"prometheus.kubemacpool.io":                     "true",
				"openshift.io/prometheus-rule-evaluation-scope": "leaf-prometheus",
			},
		},
		Spec: v1.PrometheusRuleSpec{
			Groups: []v1.RuleGroup{},
		},
	}
}
