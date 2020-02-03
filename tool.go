// +build tools

package tools

import (
	_ "github.com/kubernetes-sigs/kustomize"
	_ "k8s.io/code-generator"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
