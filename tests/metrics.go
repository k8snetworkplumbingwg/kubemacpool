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

package tests

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	"github.com/k8snetworkplumbingwg/kubemacpool/tests/kubectl"
)

const macCollisionMetric = "kmp_mac_collisions"

func getMetrics(token string) (string, error) {
	podList, err := getManagerPods()
	if err != nil {
		return "", err
	}

	bearer := "Authorization: Bearer " + token
	stdout, _, err := kubectl.Kubectl("exec", "-n", managerNamespace, podList.Items[0].Name, "--",
		"curl", "-s", "-k", "--header", bearer, "https://127.0.0.1:8443/metrics")

	return stdout, err
}

func getManagerPods() (*v1.PodList, error) {
	deployment, err := testClient.K8sClient.AppsV1().Deployments(managerNamespace).Get(
		context.TODO(), names.MANAGER_DEPLOYMENT, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	labelSelector := labels.Set(deployment.Spec.Selector.MatchLabels).String()
	podList, err := testClient.K8sClient.CoreV1().Pods(managerNamespace).List(context.TODO(),
		metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}

	return podList, err
}

func findMetric(metrics, expectedMetric string) string {
	for _, line := range strings.Split(metrics, "\n") {
		if strings.HasPrefix(line, expectedMetric+" ") {
			return line
		}
	}

	return ""
}

func findMetricWithMAC(metrics, metricName, mac string) string {
	prefix := fmt.Sprintf(`%s{mac=%q}`, metricName, strings.ToLower(mac))
	for _, line := range strings.Split(metrics, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// getMACCollisionCount returns the collision count for a specific MAC address.
// Returns 0 if the MAC is not found in the metrics (no collision).
func getMACCollisionCount(mac string) (int, error) {
	token, stderr, err := getPrometheusToken()
	if err != nil {
		return 0, fmt.Errorf("failed to get prometheus token: %s: %w", stderr, err)
	}

	metrics, err := getMetrics(token)
	if err != nil {
		return 0, fmt.Errorf("failed to get metrics: %w", err)
	}

	line := findMetricWithMAC(metrics, macCollisionMetric, mac)
	if line == "" {
		return 0, nil // MAC not found means no collision tracked
	}

	// Parse value from line like: kmp_mac_collisions{mac="aa:bb:cc:dd:ee:ff"} 2
	const expectedParts = 2
	parts := strings.Split(line, "} ")
	if len(parts) != expectedParts {
		return 0, fmt.Errorf("unexpected metric format: %s", line)
	}

	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse metric value: %w", err)
	}

	return count, nil
}

func getPrometheusToken() (token, stderr string, err error) {
	const (
		monitoringNamespace = "monitoring"
		prometheusPod       = "prometheus-k8s-0"
		container           = "prometheus"
		tokenPath           = "/var/run/secrets/kubernetes.io/serviceaccount/token" // #nosec G101 --
		// Standard Kubernetes service account token path, not hardcoded credentials
	)

	return kubectl.Kubectl("exec", "-n", monitoringNamespace, prometheusPod, "-c", container, "--", "cat", tokenPath)
}
