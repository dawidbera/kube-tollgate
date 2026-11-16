// Package main is the entrypoint for the kube-tollgate FinOps admission controller daemon.
package main

import (
	"flag"
	"os"
	"strconv"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
	"github.com/kube-tollgate/kube-tollgate/pkg/metrics"
	"github.com/kube-tollgate/kube-tollgate/pkg/policy"
	tollgatewebhook "github.com/kube-tollgate/kube-tollgate/pkg/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tollgatev1alpha1.AddToScheme(scheme))
}

// getEnvOrDefault returns an environment variable's value or a fallback default if unset.
func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// getEnvIntOrDefault returns an environment variable as integer or a fallback default if unset/invalid.
func getEnvIntOrDefault(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return fallback
}

// getEnvBoolOrDefault returns an environment variable as boolean or a fallback default if unset/invalid.
func getEnvBoolOrDefault(key string, fallback bool) bool {
	if val := os.Getenv(key); val != "" {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		}
	}
	return fallback
}

func main() {
	var (
		webhookPort    int
		metricsPort    int
		certDir        string
		enableAutoCert bool
		defaultMode    string
		logLevel       string
	)

	flag.IntVar(&webhookPort, "webhook-port", getEnvIntOrDefault("TOLLGATE_WEBHOOK_PORT", 9443), "HTTPS port for admission webhook server.")
	flag.IntVar(&metricsPort, "metrics-port", getEnvIntOrDefault("TOLLGATE_METRICS_PORT", 8080), "HTTP port for Prometheus metrics and health checks.")
	flag.StringVar(&certDir, "cert-dir", getEnvOrDefault("TOLLGATE_CERT_DIR", "/tmp/k8s-webhook-server/serving-certs"), "Directory containing tls.crt and tls.key.")
	flag.BoolVar(&enableAutoCert, "enable-auto-cert", getEnvBoolOrDefault("TOLLGATE_ENABLE_AUTO_CERT", true), "Generate self-signed CA and server cert if certs are missing.")
	flag.StringVar(&defaultMode, "default-mode", getEnvOrDefault("TOLLGATE_DEFAULT_MODE", "Enforce"), "Global default mode when not specified in policy (Enforce / Audit).")
	flag.StringVar(&logLevel, "log-level", getEnvOrDefault("TOLLGATE_LOG_LEVEL", "info"), "Log level (debug, info, warn, error).")

	opts := zap.Options{
		Development: logLevel == "debug",
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog.Info("starting kube-tollgate admission controller",
		"webhookPort", webhookPort,
		"metricsPort", metricsPort,
		"certDir", certDir,
		"enableAutoCert", enableAutoCert,
		"defaultMode", defaultMode,
	)

	// Register Prometheus metrics
	metrics.RegisterMetrics()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: ":" + strconv.Itoa(metricsPort),
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			CertDir: certDir,
		}),
		HealthProbeBindAddress: ":" + strconv.Itoa(metricsPort),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	policyCache := policy.NewMemoryCache()
	if err := (&policy.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Cache:  policyCache,
		Log:    ctrl.Log.WithName("controllers").WithName("TollgatePolicy"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TollgatePolicy")
		os.Exit(1)
	}

	// Register admission webhooks
	engine := policy.NewEngine(policyCache)
	mutatingHandler := tollgatewebhook.NewMutatingHandler(
		mgr.GetClient(),
		engine,
		mgr.GetScheme(),
		ctrl.Log.WithName("webhook").WithName("mutating"),
	)
	mgr.GetWebhookServer().Register("/mutate", &admission.Webhook{
		Handler: mutatingHandler,
	})

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
