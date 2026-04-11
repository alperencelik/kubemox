package framework

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GetMetrics fetches the raw Prometheus metrics text from the operator's /metrics endpoint.
// It connects via the Kubernetes service using port-forward or direct service URL.
func (f *Framework) GetMetrics(ctx context.Context) (string, error) {
	// When the operator is deployed in-cluster, the metrics endpoint is available
	// via the service. We use a direct HTTP call to the service ClusterIP.
	svcHost := fmt.Sprintf("%s.%s.svc.cluster.local",
		f.Config.Operator.DeploymentName,
		f.Config.Operator.Namespace,
	)
	url := fmt.Sprintf("http://%s:8080/metrics", svcHost)

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // e2e test only
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating metrics request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading metrics response: %w", err)
	}

	return string(body), nil
}

// AssertMetricExists checks that a metric with the given name appears in the metrics output.
func AssertMetricExists(metricsOutput, metricName string) bool {
	for _, line := range strings.Split(metricsOutput, "\n") {
		if strings.HasPrefix(line, metricName+" ") || strings.HasPrefix(line, metricName+"{") {
			return true
		}
	}
	return false
}
