// Package cert provides TLS certificate bootstrapping and validation for admission webhooks.
package cert

// Config holds options for certificate generation.
type Config struct {
	// CertDir is the directory where serving certificates are stored.
	CertDir string
	// ServiceName is the Kubernetes Service name for the webhook.
	ServiceName string
	// Namespace is the Kubernetes namespace where the webhook service resides.
	Namespace string
}
