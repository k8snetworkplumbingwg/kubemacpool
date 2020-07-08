package certificate

import (
	"crypto/x509"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/qinqon/kube-admission-webhook/pkg/certificate/triple"
)

// nextElapsedForCleanup return a substraction between nextCleanupDeadline and
// `now`
func (m *Manager) nextElapsedForCleanup() (time.Duration, error) {
	deadline, err := m.nextCleanupDeadline()
	if err != nil {
		return time.Duration(0), errors.Wrap(err, "failed calculating cleanup deadline")
	}
	now := m.now()
	elapsedForCleanup := deadline.Sub(now)
	m.log.Info(fmt.Sprintf("nextElapsedForCleanup {now: %s, deadline: %s, elapsedForCleanup: %s}", now, deadline, elapsedForCleanup))
	return elapsedForCleanup, nil
}

// nextCleanupDeadline get all the certificates at CABundle and return the
// deadline calculated by nextCleanupDeadlineForCerts
func (m *Manager) nextCleanupDeadline() (time.Time, error) {
	cas, err := m.getCACertsFromCABundle()
	if err != nil {
		return m.now(), errors.Wrap(err, "failed getting CA certificates from CA bundle")
	}

	return m.nextCleanupDeadlineForCerts(cas), nil
}

// nextCleanupDeadlineForCACerts will inspect CA certificates
// select the deadline based on certificate that is going to expire
// sooner so cleanup is triggered then
func (m *Manager) nextCleanupDeadlineForCerts(certificates []*x509.Certificate) time.Time {

	var selectedCertificate *x509.Certificate

	for _, certificate := range certificates {
		if selectedCertificate == nil || certificate.NotAfter.Before(selectedCertificate.NotAfter) {
			selectedCertificate = certificate
		}
	}
	if selectedCertificate == nil {
		return m.now()
	}

	return selectedCertificate.NotAfter
}

func (m *Manager) cleanUpCABundle() error {
	logger := m.log.WithName("cleanUpCABundle")
	logger.Info("Cleaning up expired certificates at CA bundle")
	_, err := m.updateWebhookCABundleWithFunc(func([]byte) ([]byte, error) {
		cas, err := m.getCACertsFromCABundle()
		if err != nil {
			return nil, errors.Wrap(err, "failed getting ca certs to start cleanup")
		}
		cleanedCAs := m.cleanUpExpiredCertificates(cas)
		pem := triple.EncodeCertsPEM(cleanedCAs)
		return pem, nil
	})

	if err != nil {
		return errors.Wrap(err, "failed updating webhook config after ca certificates cleanup")
	}
	return nil
}

func (m *Manager) cleanUpExpiredCertificates(certificates []*x509.Certificate) []*x509.Certificate {
	now := m.now()
	// create a zero-length slice with the same underlying array
	cleanedUpCertificates := certificates[:0]
	for _, certificate := range certificates {
		if certificate.NotAfter != m.now() && certificate.NotAfter.After(now) {
			cleanedUpCertificates = append(cleanedUpCertificates, certificate)
		}
	}
	return cleanedUpCertificates
}
