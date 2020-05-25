// +build tools

package tools

import (
	_ "github.com/mattn/goveralls"
	_ "github.com/onsi/ginkgo/ginkgo"
	_ "k8s.io/code-generator/cmd/deepcopy-gen"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
	_ "sigs.k8s.io/kustomize/kustomize/v3"
)
