package tests

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("kube-admission library", func() {
	Context("Check objects recovery", func() {
		var (
			err         error
			oldSecret   *v1.Secret
			oldCABundle []byte
		)
		BeforeEach(func() {
			By("getting the secret prior to any certification change ")
			oldSecret, err = GetCurrentSecret(names.WEBHOOK_SERVICE)
			Expect(err).ToNot(HaveOccurred(), "Should successfully get pre test secret")

			By("getting the caBundle prior to any certification change ")
			oldCABundle = GetCurrentCABundle()
		})

		It("should be able to recover from service secret deletion", func() {
			By("deleting the webhook service secret")
			deleteServiceSecret()

			checkCertLibraryRecovery(oldCABundle, oldSecret)
		})

		It("should be able to recover from mutatingWebhookConfiguration caBundle deletion", func() {

			By("deleting the mutatingWebhookConfiguration caBundle")
			deleteServiceCaBundle()

			checkCertLibraryRecovery(oldCABundle, oldSecret)
		})
	})
})

func deleteServiceSecret() {
	var err error
	secret, err := testClient.VirtClient.CoreV1().Secrets(managerNamespace).Get(context.TODO(), names.WEBHOOK_SERVICE, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should successfully get kubemacpool secret")

	err = testClient.VirtClient.CoreV1().Secrets(managerNamespace).Delete(context.TODO(), secret.Name, metav1.DeleteOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should successfully delete the new secret")
}

func deleteServiceCaBundle() {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mutatingWebhook, err := testClient.VirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), names.MUTATE_WEBHOOK_CONFIG, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

		for i := range mutatingWebhook.Webhooks {
			mutatingWebhook.Webhooks[i].ClientConfig.CABundle = make([]byte, 0)
		}

		_, err = testClient.VirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(), mutatingWebhook, metav1.UpdateOptions{})
		return err
	})

	Expect(err).ToNot(HaveOccurred(), "Should successfully remove caBundle from MutatingWebhookConfiguration")
}

func checkCertLibraryRecovery(oldCABundle []byte, oldSecret *v1.Secret) {
	By("checking that the secret is regenerated")
	checkSecretRecovery(oldSecret)

	By("checking that the caBundle is regenerated")
	checkCaBundleRecovery(oldCABundle)
}

func GetCurrentSecret(secretName string) (*v1.Secret, error) {
	return testClient.VirtClient.CoreV1().Secrets(managerNamespace).Get(context.TODO(), secretName, metav1.GetOptions{})
}

func GetCurrentCABundle() (caBundle []byte) {
	mutatingWebhook, err := testClient.VirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), names.MUTATE_WEBHOOK_CONFIG, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

	//get the first one
	return mutatingWebhook.Webhooks[0].ClientConfig.CABundle
}

func checkSecretRecovery(oldSecret *v1.Secret) {
	Eventually(func() (map[string][]byte, error) {
		By("Getting the new secret if exists")
		secret, err := GetCurrentSecret(names.WEBHOOK_SERVICE)
		if err != nil {
			return nil, err
		}
		return secret.Data, nil

	}, timeout, pollingInterval).ShouldNot(Equal(oldSecret.Data), "should successfully renew secret")
}

func checkCaBundleRecovery(oldCABundle []byte) {
	Eventually(func() ([][]byte, error) {
		By("Getting the MutatingWebhookConfiguration")
		mutatingWebhook, err := testClient.VirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), names.MUTATE_WEBHOOK_CONFIG, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		caBundles := [][]byte{}
		for _, webhook := range mutatingWebhook.Webhooks {
			caBundles = append(caBundles, webhook.ClientConfig.CABundle)
		}
		return caBundles, nil
	}, timeout, pollingInterval).ShouldNot(ContainElement(oldCABundle), "should successfully renew all webhook's caBundles")
}
