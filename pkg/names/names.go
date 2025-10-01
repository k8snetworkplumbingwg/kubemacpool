package names

import (
	"os"
)

const MANAGER_DEPLOYMENT = "kubemacpool-mac-controller-manager"

const CERT_MANAGER_DEPLOYMENT = "kubemacpool-cert-manager"

const WEBHOOK_SERVICE = "kubemacpool-service"

const MUTATE_WEBHOOK = "kubemacpool-webhook"

const MUTATE_WEBHOOK_CONFIG = "kubemacpool-mutator"

const K8S_RUNLABEL = "runlevel"

const OPENSHIFT_RUNLABEL = "openshift.io/run-level"

const WAIT_TIME_ARG = "wait-time"

const MAC_RANGE_CONFIGMAP = "kubemacpool-mac-range-config"

// Relationship labels
const COMPONENT_LABEL_KEY = "app.kubernetes.io/component"
const PART_OF_LABEL_KEY = "app.kubernetes.io/part-of"
const VERSION_LABEL_KEY = "app.kubernetes.io/version"
const MANAGED_BY_LABEL_KEY = "app.kubernetes.io/managed-by"

func IncludeRelationshipLabels(labels map[string]string) map[string]string {
	if labels == nil {
		labels = map[string]string{}
	}

	mapLabelKeys := map[string]string{
		"COMPONENT":  COMPONENT_LABEL_KEY,
		"PART_OF":    PART_OF_LABEL_KEY,
		"VERSION":    VERSION_LABEL_KEY,
		"MANAGED_BY": MANAGED_BY_LABEL_KEY,
	}

	for key, label := range mapLabelKeys {
		envVar := os.Getenv(key)
		if envVar != "" {
			labels[label] = envVar
		}
	}

	return labels
}
