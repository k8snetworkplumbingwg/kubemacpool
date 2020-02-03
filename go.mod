module github.com/k8snetworkplumbingwg/kubemacpool

go 1.12

require (
	github.com/NYTimes/gziphandler v1.0.1 // indirect
	github.com/containernetworking/cni v0.6.0 // indirect
	github.com/go-logr/logr v0.1.0
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/intel/multus-cni v0.0.0-20190127194101-cd6f9880ac19
	github.com/markbates/inflect v1.0.4 // indirect
	github.com/mattbaird/jsonpatch v0.0.0-20171005235357-81af80346b1a // indirect
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/prometheus/procfs v0.0.8 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4 // indirect
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/code-generator v0.17.2
	kubevirt.io/client-go v0.20.4
	kubevirt.io/kubevirt v0.20.4
	sigs.k8s.io/controller-runtime v0.1.9
	sigs.k8s.io/controller-tools v0.1.7
	sigs.k8s.io/testing_frameworks v0.1.2 // indirect
)

replace (
	k8s.io/api => k8s.io/api v0.0.0-20190222213804-5cb15d344471
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190221213512-86fb29eff628
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190228174230-b40b2a5939e4
	kubevirt.io/client-go v0.0.0-00010101000000-000000000000 => kubevirt.io/client-go v0.20.4
)
