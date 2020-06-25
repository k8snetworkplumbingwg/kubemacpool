package cmd

import (
	"github.com/k8snetworkplumbingwg/kubemacpool/tests/environment"
)

func Kubectl(arguments ...string) (string, error) {
	kubectl := environment.GetVarWithDefault("KUBECTL", "cluster/kubectl.sh")
	return Run(kubectl, false, arguments...)
}
