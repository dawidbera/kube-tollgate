// Package cert provides TLS certificate bootstrapping and validation for admission webhooks.
package cert

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Bootstrapper handles TLS certificate generation and webhook configuration patching.
type Bootstrapper struct {
	CertDir                     string
	ServiceName                 string
	Namespace                   string
	MutatingWebhookConfigName   string
	ValidatingWebhookConfigName string
	Client                      client.Client
	Log                         logr.Logger
}

// NewBootstrapper creates a Bootstrapper configured with default naming conventions.
func NewBootstrapper(certDir, serviceName, namespace string, c client.Client, log logr.Logger) *Bootstrapper {
	if serviceName == "" {
		serviceName = "tollgate-webhook-service"
	}
	if namespace == "" {
		namespace = "tollgate-system"
	}
	return &Bootstrapper{
		CertDir:                     certDir,
		ServiceName:                 serviceName,
		Namespace:                   namespace,
		MutatingWebhookConfigName:   "kube-tollgate-mutating-webhook-configuration",
		ValidatingWebhookConfigName: "kube-tollgate-validating-webhook-configuration",
		Client:                      c,
		Log:                         log,
	}
}

// EnsureCertificates checks for existing certificates in CertDir and generates self-signed certificates if missing.
// It returns the PEM-encoded root CA certificate.
func (b *Bootstrapper) EnsureCertificates() ([]byte, error) {
	certPath := filepath.Join(b.CertDir, "tls.crt")
	keyPath := filepath.Join(b.CertDir, "tls.key")
	caPath := filepath.Join(b.CertDir, "ca.crt")

	if fileExists(certPath) && fileExists(keyPath) {
		b.Log.Info("serving certificates already exist in cert dir", "certDir", b.CertDir)
		if fileExists(caPath) {
			return os.ReadFile(caPath)
		}
		return os.ReadFile(certPath)
	}

	b.Log.Info("generating self-signed TLS certificates for webhook", "certDir", b.CertDir)
	if err := os.MkdirAll(b.CertDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cert directory %s: %w", b.CertDir, err)
	}

	// 1. Generate Root CA
	caCertPEM, caKey, caCert, err := generateCA()
	if err != nil {
		return nil, fmt.Errorf("failed to generate root CA: %w", err)
	}

	// 2. Generate Serving Cert signed by Root CA
	serverCertPEM, serverKeyPEM, err := generateServerCertificate(b.ServiceName, b.Namespace, caCert, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server certificate: %w", err)
	}

	// 3. Write files to disk
	if err := os.WriteFile(certPath, serverCertPEM, 0644); err != nil {
		return nil, fmt.Errorf("failed to write tls.crt: %w", err)
	}
	if err := os.WriteFile(keyPath, serverKeyPEM, 0600); err != nil {
		return nil, fmt.Errorf("failed to write tls.key: %w", err)
	}
	if err := os.WriteFile(caPath, caCertPEM, 0644); err != nil {
		return nil, fmt.Errorf("failed to write ca.crt: %w", err)
	}

	b.Log.Info("self-signed TLS certificates successfully generated and written to disk", "certDir", b.CertDir)
	return caCertPEM, nil
}

// PatchWebhookConfigurations patches the Mutating and Validating WebhookConfigurations with the CA bundle.
func (b *Bootstrapper) PatchWebhookConfigurations(ctx context.Context, caBundle []byte) error {
	if b.Client == nil || len(caBundle) == 0 {
		return nil
	}

	// Patch MutatingWebhookConfiguration
	if err := b.patchMutatingWebhook(ctx, caBundle); err != nil {
		b.Log.Error(err, "failed to patch mutating webhook configuration", "name", b.MutatingWebhookConfigName)
	}

	// Patch ValidatingWebhookConfiguration
	if err := b.patchValidatingWebhook(ctx, caBundle); err != nil {
		b.Log.Error(err, "failed to patch validating webhook configuration", "name", b.ValidatingWebhookConfigName)
	}

	return nil
}

// patchMutatingWebhook patches the CABundle in MutatingWebhookConfiguration if it exists.
func (b *Bootstrapper) patchMutatingWebhook(ctx context.Context, caBundle []byte) error {
	var mwc admissionregistrationv1.MutatingWebhookConfiguration
	err := b.Client.Get(ctx, types.NamespacedName{Name: b.MutatingWebhookConfigName}, &mwc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			b.Log.V(1).Info("mutating webhook configuration not found, skipping patch", "name", b.MutatingWebhookConfigName)
			return nil
		}
		return err
	}

	modified := false
	for i := range mwc.Webhooks {
		if !bytes.Equal(mwc.Webhooks[i].ClientConfig.CABundle, caBundle) {
			mwc.Webhooks[i].ClientConfig.CABundle = caBundle
			modified = true
		}
	}

	if modified {
		b.Log.Info("updating caBundle on MutatingWebhookConfiguration", "name", b.MutatingWebhookConfigName)
		return b.Client.Update(ctx, &mwc)
	}
	return nil
}

// patchValidatingWebhook patches the CABundle in ValidatingWebhookConfiguration if it exists.
func (b *Bootstrapper) patchValidatingWebhook(ctx context.Context, caBundle []byte) error {
	var vwc admissionregistrationv1.ValidatingWebhookConfiguration
	err := b.Client.Get(ctx, types.NamespacedName{Name: b.ValidatingWebhookConfigName}, &vwc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			b.Log.V(1).Info("validating webhook configuration not found, skipping patch", "name", b.ValidatingWebhookConfigName)
			return nil
		}
		return err
	}

	modified := false
	for i := range vwc.Webhooks {
		if !bytes.Equal(vwc.Webhooks[i].ClientConfig.CABundle, caBundle) {
			vwc.Webhooks[i].ClientConfig.CABundle = caBundle
			modified = true
		}
	}

	if modified {
		b.Log.Info("updating caBundle on ValidatingWebhookConfiguration", "name", b.ValidatingWebhookConfigName)
		return b.Client.Update(ctx, &vwc)
	}
	return nil
}

// fileExists checks whether a path exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// generateCA creates a self-signed root CA certificate and private key.
func generateCA() ([]byte, *rsa.PrivateKey, *x509.Certificate, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}

	caTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "kube-tollgate-ca",
			Organization: []string{"kube-tollgate"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	})

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, err
	}

	return caCertPEM, caKey, caCert, nil
}

// generateServerCertificate creates a TLS serving certificate signed by the root CA.
func generateServerCertificate(serviceName, namespace string, caCert *x509.Certificate, caKey *rsa.PrivateKey) ([]byte, []byte, error) {
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}

	commonName := fmt.Sprintf("%s.%s.svc", serviceName, namespace)
	dnsNames := []string{
		serviceName,
		fmt.Sprintf("%s.%s", serviceName, namespace),
		commonName,
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
		"localhost",
	}

	serverTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"kube-tollgate"},
		},
		DNSNames:    dnsNames,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, &serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	serverCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverDER,
	})

	serverKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	})

	return serverCertPEM, serverKeyPEM, nil
}
