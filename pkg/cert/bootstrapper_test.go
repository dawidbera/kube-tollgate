package cert

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestBootstrapper_EnsureCertificates verifies generation and persistence of root CA and server certificates.
func TestBootstrapper_EnsureCertificates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tollgate-cert-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	b := NewBootstrapper(tempDir, "tollgate-svc", "tollgate-ns", nil, logr.Discard())

	// First execution: generates certificates
	caPEM, err := b.EnsureCertificates()
	require.NoError(t, err)
	assert.NotEmpty(t, caPEM)

	certPath := filepath.Join(tempDir, "tls.crt")
	keyPath := filepath.Join(tempDir, "tls.key")
	caPath := filepath.Join(tempDir, "ca.crt")

	assert.FileExists(t, certPath)
	assert.FileExists(t, keyPath)
	assert.FileExists(t, caPath)

	// Validate parsed certificate SANs
	certBytes, err := os.ReadFile(certPath)
	require.NoError(t, err)
	block, _ := pem.Decode(certBytes)
	require.NotNil(t, block)
	parsedCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Contains(t, parsedCert.DNSNames, "tollgate-svc")
	assert.Contains(t, parsedCert.DNSNames, "tollgate-svc.tollgate-ns")
	assert.Contains(t, parsedCert.DNSNames, "tollgate-svc.tollgate-ns.svc")
	assert.Contains(t, parsedCert.DNSNames, "localhost")

	// Second execution: reuses existing certificates without error
	caPEM2, err := b.EnsureCertificates()
	require.NoError(t, err)
	assert.Equal(t, caPEM, caPEM2)
}

// TestBootstrapper_PatchWebhookConfigurations verifies injection of caBundle into mutating and validating webhook configs.
func TestBootstrapper_PatchWebhookConfigurations(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, admissionregistrationv1.AddToScheme(scheme))

	mwc := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kube-tollgate-mutating-webhook-configuration",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "mutate.finops.tollgate.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-bundle"),
				},
			},
		},
	}

	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kube-tollgate-validating-webhook-configuration",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "validate.finops.tollgate.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-bundle"),
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(mwc, vwc).
		Build()

	b := NewBootstrapper("/tmp", "tollgate-svc", "tollgate-ns", fakeClient, logr.Discard())

	newCABundle := []byte("new-ca-bundle-pem-content")
	err := b.PatchWebhookConfigurations(context.Background(), newCABundle)
	require.NoError(t, err)

	// Verify MWC
	var updatedMWC admissionregistrationv1.MutatingWebhookConfiguration
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: mwc.Name}, &updatedMWC)
	require.NoError(t, err)
	assert.Equal(t, newCABundle, updatedMWC.Webhooks[0].ClientConfig.CABundle)

	// Verify VWC
	var updatedVWC admissionregistrationv1.ValidatingWebhookConfiguration
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: vwc.Name}, &updatedVWC)
	require.NoError(t, err)
	assert.Equal(t, newCABundle, updatedVWC.Webhooks[0].ClientConfig.CABundle)
}
