module github.com/k8snetworkplumbingwg/kubemacpool

go 1.12

require (
	github.com/Azure/go-autorest v11.2.8+incompatible // indirect
	github.com/NYTimes/gziphandler v1.1.1 // indirect
	github.com/containernetworking/cni v0.6.0 // indirect
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f // indirect
	github.com/go-logr/logr v0.1.0
	github.com/gopherjs/gopherjs v0.0.0-20181103185306-d547d1d9531e // indirect
	github.com/intel/multus-cni v0.0.0-20190127194101-cd6f9880ac19
	github.com/mattn/go-runewidth v0.0.4 // indirect
	github.com/olekukonko/tablewriter v0.0.1 // indirect
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/openshift/custom-resource-status v0.0.0-20190822192428-e62f2f3b79f3 // indirect
	github.com/prometheus/procfs v0.0.8 // indirect
	github.com/qinqon/kube-admission-webhook v0.3.0
	k8s.io/api v0.18.0-alpha.2
	k8s.io/apiextensions-apiserver v0.17.2 // indirect
	k8s.io/apimachinery v0.18.0-alpha.2
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/code-generator v0.18.0-alpha.2
	k8s.io/gengo v0.0.0-20190327210449-e17681d19d3a // indirect
	k8s.io/utils v0.0.0-20200109141947-94aeca20bf09 // indirect
	kubevirt.io/client-go v0.20.4
	kubevirt.io/containerized-data-importer v1.10.6 // indirect
	kubevirt.io/kubevirt v0.20.4
	sigs.k8s.io/controller-runtime v0.4.0
	sigs.k8s.io/controller-tools v0.2.4
)

replace (
	// Pinned to kubernetes-1.16.0
	k8s.io/api => k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.18.0-alpha.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190918160949-bfa5e2e684ad
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.0.0-20190918162238-f783a3654da8
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20190918203125-ae665f80358a
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.0.0-20190918202959-c340507a5d48
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20190612205613-18da4a14b22b
	k8s.io/component-base => k8s.io/component-base v0.0.0-20190918200425-ed2f0867c778
	k8s.io/cri-api => k8s.io/cri-api v0.0.0-20190817025403-3ae76f584e79
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.0.0-20190918203248-97c07dcbb623
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190918201136-c3a845f1fbb2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.0.0-20190918202837-c54ce30c680e
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.0.0-20190918202429-08c8357f8e2d
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.0.0-20190918202713-c34a54b3ec8e
	k8s.io/kubelet => k8s.io/kubelet v0.0.0-20190918202550-958285cf3eef
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.0.0-20190918203421-225f0541b3ea
	k8s.io/metrics => k8s.io/metrics v0.0.0-20190918202012-3c1ca76f5bda
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.0.0-20190918201353-5cc279503896
	k8s.io/utils => k8s.io/utils v0.0.0-20190801114015-581e00157fb1
	kubevirt.io/client-go v0.0.0-00010101000000-000000000000 => github.com/kubevirt/client-go v0.25.0
)
