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

package rules

import (
	"strings"
	"testing"
)

func TestAlertsHaveRunbookURL(t *testing.T) {
	if err := SetupRules(); err != nil {
		t.Fatalf("setup rules: %v", err)
	}

	alerts := ListAlerts()
	if len(alerts) == 0 {
		t.Fatal("no alerts registered")
	}

	for _, alert := range alerts {
		alert := alert
		if alert.Alert == "" {
			continue
		}

		t.Run(alert.Alert, func(t *testing.T) {
			t.Parallel()
			if alert.Annotations == nil {
				t.Fatal("annotations are missing")
			}

			runbookURL, ok := alert.Annotations["runbook_url"]
			if !ok {
				t.Fatal("runbook_url annotation is missing")
			}

			trimmedURL := strings.TrimSpace(runbookURL)
			if trimmedURL == "" {
				t.Fatal("runbook_url is empty")
			}

			if !strings.Contains(trimmedURL, alert.Alert) {
				t.Errorf("runbook_url %q does not include alert name %q", trimmedURL, alert.Alert)
			}
		})
	}
}
