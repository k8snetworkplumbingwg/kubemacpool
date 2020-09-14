module github.com/k8snetworkplumbingwg/kubemacpool

go 1.13

require (
	github.com/go-logr/logr v0.2.1-0.20200730175230-ee2de8da5be6
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/intel/multus-cni v0.0.0-20200316125841-bfaf22964b51
	github.com/mattn/goveralls v0.0.7
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/pkg/errors v0.9.1
	github.com/qinqon/kube-admission-webhook v0.12.0
	k8s.io/api v0.19.1
	k8s.io/apimachinery v0.19.1
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/code-generator v0.19.1
	kubevirt.io/client-go v0.29.0
	kubevirt.io/kubevirt v0.29.0
	kubevirt.io/qe-tools v0.1.6
	sigs.k8s.io/controller-runtime v0.6.2
	sigs.k8s.io/controller-tools v0.4.0
	sigs.k8s.io/kustomize/kustomize/v3 v3.3.0
)

replace (
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.2.0
	golang.org/x/text => golang.org/x/text v0.3.3
	// Pinned to kubernetes-1.19.1
	k8s.io/api => k8s.io/api v0.19.1
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.1
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.1
	k8s.io/apiserver => k8s.io/apiserver v0.19.1
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.19.1
	k8s.io/client-go => k8s.io/client-go v0.19.1
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.1
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.19.1
	k8s.io/component-base => k8s.io/component-base v0.19.1
	k8s.io/cri-api => k8s.io/cri-api v0.19.1
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.19.1
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.19.1
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.19.1
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.19.1
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.19.1
	k8s.io/kubelet => k8s.io/kubelet v0.19.1
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.19.1
	k8s.io/metrics => k8s.io/metrics v0.19.1
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.19.1
	kubevirt.io/client-go => github.com/kubevirt/client-go v0.29.0
)
