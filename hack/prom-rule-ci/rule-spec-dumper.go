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
	"os"

	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/rules"
)

const emptyRulesetError = "no registered recording rule or alert"

func verifyArgs(args []string) error {
	numOfArgs := len(args[1:])
	if numOfArgs != 1 {
		return fmt.Errorf("expected exactly 1 argument, got: %d", numOfArgs)
	}
	return nil
}

func main() {
	if err := verifyArgs(os.Args); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}

	targetFile := os.Args[1]

	if err := rules.SetupRules(); err != nil {
		panic(err)
	}

	promRule, err := rules.BuildPrometheusRule("ci")
	if err != nil {
		if err.Error() != emptyRulesetError {
			panic(err)
		}
		promRule = emptyPrometheusRule("ci")
	}

	b, err := json.Marshal(promRule.Spec)
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile(targetFile, b, 0644); err != nil {
		panic(err)
	}
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
