package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	networkingv1 "k8s.io/api/networking/v1"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	zap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme              = runtime.NewScheme()
	flagAnnotationKey   = flag.String("annotation-key", "external-dns.alpha.kubernetes.io/target", "Annotation key to update on the Ingress")
	flagIngressClassAnn = flag.String("ingress-class-annotation-key", "kubernetes.io/ingress.class", "Annotation key that stores ingress class (e.g. kubernetes.io/ingress.class)")
	flagIngressClass    = flag.String("ingress-class", "public-nginx", "Ingress class value to target (e.g. public-nginx)")
	flagIPs             = flag.String("ips", "", "Comma-separated list of IPs to probe (e.g. 1.1.1.1,8.8.8.8)")
	flagHTTPPath        = flag.String("http-path", "/", "HTTP path to GET on each IP")
	flagScheme          = flag.String("http-scheme", "http", "http or https")
	flagInterval        = flag.Duration("interval", 30*time.Second, "Probe interval")
	flagTimeout         = flag.Duration("timeout", 2*time.Second, "HTTP request timeout per IP")
	flagSkipTLSVerify   = flag.Bool("insecure-skip-verify", false, "Skip TLS verification when scheme=https")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(networkingv1.AddToScheme(scheme))
}

type Runner struct {
	k8s                       client.Client
	ingressClassAnnotationKey string
	ingressClass              string
	annotationKey             string
	ips                       []string
	httpClient                *http.Client
	urlScheme                 string
	httpPath                  string
	interval                  time.Duration
}

func (r *Runner) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("runner started")

	t := time.NewTicker(r.interval)
	defer t.Stop()

	// run immediately at startup
	r.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) HealthyIPs(ctx context.Context) ([]string, error) {
	healthy := make([]string, 0, len(r.ips))
	for _, ip := range r.ips {
		u := fmt.Sprintf("%s://%s%s", r.urlScheme, net.JoinHostPort(ip, portForScheme(r.urlScheme)), r.httpPath)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := r.httpClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			healthy = append(healthy, ip)
		}
	}
	if len(healthy) == 0 {
		return nil, fmt.Errorf("no healthy IP found")
	}
	return healthy, nil
}

func portForScheme(s string) string {
	if s == "https" {
		return "443"
	}
	return "80"
}

func (r *Runner) tick(ctx context.Context) {
	logger := log.FromContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, *flagTimeout*time.Duration(max(1, len(r.ips))))

	defer cancel()

	healthyIPs, err := r.HealthyIPs(ctx)
	if err != nil {
		logger.Info("no healthy IP; leaving annotations unchanged", "error", err.Error())
		return
	}

	desired := strings.Join(healthyIPs, ",")

	list := &networkingv1.IngressList{}
	if err := r.k8s.List(ctx, list); err != nil {
		logger.Error(err, "failed to list Ingresses")
		return
	}

	for i := range list.Items {
		ing := &list.Items[i]

		if ing.Annotations == nil {
			continue
		}
		if cls, ok := ing.Annotations[r.ingressClassAnnotationKey]; !ok || cls != r.ingressClass {
			continue
		}

		if ing.Annotations == nil {
			ing.Annotations = map[string]string{}
		}
		current := ing.Annotations[r.annotationKey]
		if current == desired {
			continue
		}

		patch := client.MergeFrom(ing.DeepCopy())
		ing.Annotations[r.annotationKey] = desired

		if err := r.k8s.Patch(ctx, ing, patch); err != nil {
			logger.Error(err, "failed to patch Ingress annotation", "ingress", types.NamespacedName{Namespace: ing.Namespace, Name: ing.Name}.String(), "key", r.annotationKey, "value", desired)
			continue
		}

		logger.Info("updated annotation", "ingress", types.NamespacedName{Namespace: ing.Namespace, Name: ing.Name}.String(), "key", r.annotationKey, "value", desired)
	}
}

func parseEnvOrFlag(name string, fallback *string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return *fallback
}

func main() {
	// Allow config via env OR flags
	flag.Parse()
	// Initialize logger before deriving any named loggers
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := ctrl.SetupSignalHandler()
	logger := ctrl.Log.WithName("ingress-target-prober")
	ctx = log.IntoContext(ctx, logger)

	cfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: ":8081",
		LeaderElection:         false, // set true for HA
	})
	if err != nil {
		logger.Error(err, "unable to start manager")
		os.Exit(1)
	}

	annotationKey := getStr("ANNOTATION_KEY", *flagAnnotationKey)
	ingressClassAnnKey := getStr("INGRESS_CLASS_ANNOTATION_KEY", *flagIngressClassAnn)
	ingressClass := getStr("INGRESS_CLASS", *flagIngressClass)
	ipCSV := getStr("IPS", *flagIPs)
	httpPath := getStr("HTTP_PATH", *flagHTTPPath)
	httpScheme := getStr("HTTP_SCHEME", *flagScheme)

	if ipCSV == "" {
		logger.Error(fmt.Errorf("missing required config"),
			"set IPS (comma-separated)")
		os.Exit(2)
	}

	ips := splitAndTrim(ipCSV)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: getBool("INSECURE_SKIP_VERIFY", *flagSkipTLSVerify)},
	}
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   getDuration("TIMEOUT", *flagTimeout),
	}

	r := &Runner{
		k8s:                       mgr.GetClient(),
		ingressClassAnnotationKey: ingressClassAnnKey,
		ingressClass:              ingressClass,
		annotationKey:             annotationKey,
		ips:                       ips,
		httpClient:                httpClient,
		urlScheme:                 httpScheme,
		httpPath:                  httpPath,
		interval:                  getDuration("INTERVAL", *flagInterval),
	}

	if err := mgr.Add(r); err != nil {
		logger.Error(err, "unable to add runner")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	logger.Info("starting manager",
		"ingress_class_annotation_key", ingressClassAnnKey,
		"ingress_class", ingressClass,
		"annotation", r.annotationKey,
		"ips", strings.Join(ips, ","),
		"path", httpPath,
		"interval", r.interval.String(),
		"scheme", httpScheme,
	)
	if err := mgr.Start(ctx); err != nil {
		logger.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getStr(env string, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}
func getDuration(env string, fallback time.Duration) time.Duration {
	if v := os.Getenv(env); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}
func getBool(env string, fallback bool) bool {
	if v := os.Getenv(env); v != "" {
		l := strings.ToLower(v)
		return l == "1" || l == "true" || l == "yes"
	}
	return fallback
}
func splitAndTrim(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
