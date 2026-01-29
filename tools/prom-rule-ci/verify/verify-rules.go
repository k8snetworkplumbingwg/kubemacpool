package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/monitoring/rules"
)

const promImage = "quay.io/prometheus/prometheus:v2.25.2"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: verify-rules <oci-bin> <target-file> <tests-file>")
	}

	ociBin := os.Args[1]
	targetFile := os.Args[2]
	testsFile := os.Args[3]

	if err := verify(ociBin, targetFile, testsFile); err != nil {
		return err
	}

	return nil
}

func verify(ociBin, targetFile, testsFile string) error {
	defer deleteRulesFile(targetFile)

	if err := createRulesFile(targetFile); err != nil {
		return err
	}

	if err := lint(ociBin, targetFile); err != nil {
		return err
	}

	if err := unitTest(ociBin, targetFile, testsFile); err != nil {
		return err
	}

	return nil
}

func createRulesFile(targetFile string) error {
	if err := rules.SetupRules(); err != nil {
		return err
	}

	promRule, err := rules.BuildPrometheusRule("ci")
	if err != nil {
		if err.Error() != "no registered recording rule or alert" {
			return err
		}
		promRule = emptyPrometheusRule("ci")
	}

	b, err := json.Marshal(promRule.Spec)
	if err != nil {
		return err
	}

	if err := os.WriteFile(targetFile, b, 0644); err != nil {
		return err
	}

	return nil
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

func deleteRulesFile(targetFile string) {
	if err := os.Remove(targetFile); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "failed to remove %s: %v\n", targetFile, err)
	}
}

func lint(ociBin, targetFile string) error {
	cmd := exec.Command(
		ociBin,
		"run",
		"--rm",
		"--entrypoint=/bin/promtool",
		"-v", targetFile+":/tmp/rules.verify:ro,Z",
		promImage,
		"check",
		"rules",
		"/tmp/rules.verify",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func unitTest(ociBin, targetFile, testsFile string) error {
	cmd := exec.Command(
		ociBin,
		"run",
		"--rm",
		"--entrypoint=/bin/promtool",
		"-v", testsFile+":/tmp/rules.test:ro,Z",
		"-v", targetFile+":/tmp/rules.verify:ro,Z",
		promImage,
		"test",
		"rules",
		"/tmp/rules.test",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
