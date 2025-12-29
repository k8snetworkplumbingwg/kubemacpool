package tests

import (
	"context"
	"fmt"
)

const (
	nadPostURL         = "/apis/k8s.cni.cncf.io/v1/namespaces/%s/network-attachment-definitions/%s"
	linuxBridgeConfCRD = `{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition",` +
		`"metadata":{"name":"%s","namespace":"%s"},` +
		`"spec":{"config":"{ \"cniVersion\": \"0.3.1\", \"type\": \"bridge\", \"bridge\": \"br1\"}"}}`
)

func createNetworkAttachmentDefinition(namespace, name string) error {
	result := testClient.K8sClient.ExtensionsV1beta1().RESTClient().
		Post().
		RequestURI(fmt.Sprintf(nadPostURL, namespace, name)).
		Body([]byte(fmt.Sprintf(linuxBridgeConfCRD, name, namespace))).
		Do(context.TODO())
	return result.Error()
}

func deleteNetworkAttachmentDefinition(namespace, name string) error {
	result := testClient.K8sClient.ExtensionsV1beta1().RESTClient().
		Delete().
		RequestURI(fmt.Sprintf(nadPostURL, namespace, name)).
		Do(context.TODO())
	return result.Error()
}
