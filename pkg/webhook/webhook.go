// Package webhook provides Mutating and Validating Admission Webhook handlers.
package webhook

// ServerConfig defines the runtime configuration for the admission webhook server.
type ServerConfig struct {
	// Port is the port number for the HTTPS admission review server.
	Port int
	// CertDir is the directory containing TLS certificate and key files.
	CertDir string
}
