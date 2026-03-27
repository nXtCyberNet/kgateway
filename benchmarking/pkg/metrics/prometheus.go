// benchmarking/pkg/metrics/prometheus.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// PrometheusClient is a thin wrapper around the official Prometheus HTTP API client.
type PrometheusClient struct {
	api v1.API
}

// NewPrometheusClient creates a new Prometheus client pointing at the given address.
func NewPrometheusClient(address string) (*PrometheusClient, error) {
	pc, err := api.NewClient(api.Config{Address: address})
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %w", err)
	}
	return &PrometheusClient{api: v1.NewAPI(pc)}, nil
}

// ScrapeGatewayMetrics collects all core latency, error, resource, and GenAI-specific
// metrics for a completed scenario run. Queries are scoped by namespace and service to
// prevent cross-benchmark metric bleed in a shared Kind cluster. The data-plane flag
// switches between Envoy and agentgateway metric names automatically.
func (p *PrometheusClient) ScrapeGatewayMetrics(ctx context.Context, namespace, service, dataPlane string, scrapeDuration time.Duration) (*scenarios.Results, error) {
	res := &scenarios.Results{
		DataPlane: dataPlane,
		Timestamp: time.Now(),
	}

	dStr := scrapeDuration.String()

	// 1. Latency percentiles — template and arg order differ per data plane.
	latencyQueryTpl := p.latencyQueryTemplate(dataPlane)
	res.P50LatencyMs, _ = p.queryWithRetry(ctx, fmt.Sprintf(latencyQueryTpl, 0.50, namespace, service, dStr))
	res.P95LatencyMs, _ = p.queryWithRetry(ctx, fmt.Sprintf(latencyQueryTpl, 0.95, namespace, service, dStr))
	res.P99LatencyMs, _ = p.queryWithRetry(ctx, fmt.Sprintf(latencyQueryTpl, 0.99, namespace, service, dStr))

	// 2. Error rate.
	errRate, err := p.errorRateQuery(ctx, namespace, service, dataPlane, dStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query error rate: %w", err)
	}
	res.ErrorRate = errRate

	// 3. Gateway resource usage (CPU in millicores, Memory in MiB).
	res.GatewayCPUMillicores = p.queryInstant(ctx, fmt.Sprintf(
		`sum(rate(container_cpu_usage_seconds_total{namespace="%s", pod=~".*kgateway.*|.*gateway.*"}[%s])) * 1000`,
		namespace, dStr))
	res.GatewayMemoryMB = p.queryInstant(ctx, fmt.Sprintf(
		`sum(container_memory_working_set_bytes{namespace="%s", pod=~".*kgateway.*|.*gateway.*"}) / 1024 / 1024`,
		namespace))

	// 4. EPP decision latency — common to both data planes when inference routing is on.
	res.EPPDecisionLatencyMs = p.queryInstant(ctx, fmt.Sprintf(
		`histogram_quantile(0.99, sum by (le) (rate(epp_endpoint_selection_duration_seconds_bucket{namespace="%s"}[%s]))) * 1000`,
		namespace, dStr))

	// 5. GenAI streaming metrics — native labels only exist on agentgateway.
	// For Envoy, these are populated by the direct SSE client in streaming.go instead.
	if dataPlane == "agentgateway" {
		res.TTFTMeanMs = p.queryInstant(ctx, fmt.Sprintf(
			`avg(agentgateway_latency_ttft_seconds{namespace="%s"}) * 1000`, namespace))
		res.TTFTp99Ms = p.queryInstant(ctx, fmt.Sprintf(
			`histogram_quantile(0.99, sum by (le) (rate(agentgateway_latency_ttft_seconds_bucket{namespace="%s"}[%s]))) * 1000`,
			namespace, dStr))
		res.ITLMeanUs = p.queryInstant(ctx, fmt.Sprintf(
			`avg(agentgateway_latency_itl_seconds{namespace="%s"}) * 1000000`, namespace))
	}

	return res, nil
}

// ScrapeTierDistribution returns the percentage of requests handled by each backend tier.
// Used by the EPP fairness scenario (S5) to validate the 70/20/10 expected distribution.
func (p *PrometheusClient) ScrapeTierDistribution(ctx context.Context, namespace string, duration time.Duration) (map[string]float64, error) {
	dStr := duration.String()
	query := fmt.Sprintf(
		`sum by (tier) (rate(agentgateway_requests_total{namespace="%s"}[%s])) / ignoring(tier) group_left sum(rate(agentgateway_requests_total{namespace="%s"}[%s])) * 100`,
		namespace, dStr, namespace, dStr)

	val, _, err := p.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query tier distribution: %w", err)
	}

	dist := make(map[string]float64)
	if vector, ok := val.(model.Vector); ok {
		for _, sample := range vector {
			if tier := string(sample.Metric["tier"]); tier != "" {
				dist["tier-"+tier] = float64(sample.Value)
			}
		}
	}
	return dist, nil
}

// latencyQueryTemplate returns the PromQL template for the given data plane.
// Both templates accept args in order: (quantile float, namespace string, service string, duration string).
// The Envoy template matches the cluster name by service name because Envoy encodes
// the upstream service in envoy_cluster_name, not in a separate label.
func (p *PrometheusClient) latencyQueryTemplate(dataPlane string) string {
	if dataPlane == "envoy" {
		// arg order: %g=quantile, first %s=namespace, second %s=service (cluster name match), third %s=duration
		return `histogram_quantile(%g, sum by (le) (rate(envoy_cluster_upstream_rq_time_bucket{namespace="%s", envoy_cluster_name=~".*%s.*"}[%s])))`
	}
	// agentgateway uses standard prometheus labels
	// arg order: %g=quantile, first %s=namespace, second %s=service, third %s=duration
	return `histogram_quantile(%g, sum by (le) (rate(agentgateway_request_duration_seconds_bucket{namespace="%s", service="%s"}[%s])))`
}

// errorRateQuery builds and executes the error-rate query for the selected data plane.
func (p *PrometheusClient) errorRateQuery(ctx context.Context, namespace, service, dataPlane, duration string) (float64, error) {
	var q string
	if dataPlane == "envoy" {
		q = fmt.Sprintf(
			`sum(rate(envoy_cluster_upstream_rq_xx{namespace="%s", envoy_cluster_name=~".*%s.*", code=~"4..|5.."}[%s])) / sum(rate(envoy_cluster_upstream_rq_total{namespace="%s", envoy_cluster_name=~".*%s.*"}[%s]))`,
			namespace, service, duration, namespace, service, duration)
	} else {
		q = fmt.Sprintf(
			`sum(rate(agentgateway_requests_total{namespace="%s", service="%s", code=~"4..|5.."}[%s])) / sum(rate(agentgateway_requests_total{namespace="%s", service="%s"}[%s]))`,
			namespace, service, duration, namespace, service, duration)
	}
	return p.queryWithRetry(ctx, q)
}

// queryWithRetry executes a PromQL instant query with exponential-backoff retries.
// A zero result on the first attempt triggers a retry — this handles the common case
// where Prometheus hasn't yet scraped metrics at the start of a benchmark window.
func (p *PrometheusClient) queryWithRetry(ctx context.Context, query string) (float64, error) {
	const attempts = 3
	for i := 0; i < attempts; i++ {
		val := p.queryInstant(ctx, query)
		if val > 0 || i == attempts-1 {
			return val, nil
		}
		time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
	}
	return 0, nil
}

// queryInstant executes a PromQL instant query and returns the first scalar value.
// Returns 0 on any error so a transient scrape failure never aborts the benchmark.
func (p *PrometheusClient) queryInstant(ctx context.Context, query string) float64 {
	val, _, err := p.api.Query(ctx, query, time.Now())
	if err != nil || val == nil {
		return 0
	}
	if vector, ok := val.(model.Vector); ok && len(vector) > 0 {
		return float64(vector[0].Value)
	}
	return 0
}
