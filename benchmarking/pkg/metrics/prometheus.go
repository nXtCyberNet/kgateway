// benchmarking/pkg/metrics/prometheus.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"errors"
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

// ErrNoSample indicates Prometheus query succeeded but returned no vector samples.
var ErrNoSample = errors.New("prometheus query returned no samples")

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
	latencyP50 := p.latencyQuery(dataPlane, 0.50, namespace, service, dStr)
	latencyP95 := p.latencyQuery(dataPlane, 0.95, namespace, service, dStr)
	latencyP99 := p.latencyQuery(dataPlane, 0.99, namespace, service, dStr)
	res.P50LatencyMs, _ = p.queryWithRetry(ctx, latencyP50)
	res.P95LatencyMs, _ = p.queryWithRetry(ctx, latencyP95)
	res.P99LatencyMs, _ = p.queryWithRetry(ctx, latencyP99)

	meanLatency, err := p.meanLatencyQuery(ctx, namespace, service, dataPlane, dStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query mean latency: %w", err)
	}
	res.MeanLatencyMs = meanLatency

	// 2. Error rate.
	errRate, err := p.errorRateQuery(ctx, namespace, service, dataPlane, dStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query error rate: %w", err)
	}
	res.ErrorRate = errRate

	rps, err := p.throughputQuery(ctx, namespace, service, dataPlane, dStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query throughput: %w", err)
	}
	res.ThroughputRPS = rps

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
		// Envoy metrics generally do not expose a namespace label in this deployment,
		// so scope by cluster name only.
		return `histogram_quantile(%g, sum by (le) (rate(envoy_cluster_upstream_rq_time_bucket{envoy_cluster_name=~".*%s.*"}[%s])))`
	}
	// agentgateway uses standard prometheus labels
	// arg order: %g=quantile, first %s=namespace, second %s=service, third %s=duration
	return `histogram_quantile(%g, sum by (le) (rate(agentgateway_request_duration_seconds_bucket{namespace="%s", service="%s"}[%s])))`
}

func (p *PrometheusClient) latencyQuery(dataPlane string, quantile float64, namespace, service, duration string) string {
	tpl := p.latencyQueryTemplate(dataPlane)
	if dataPlane == "envoy" {
		return fmt.Sprintf(tpl, quantile, service, duration)
	}
	return fmt.Sprintf(tpl, quantile, namespace, service, duration)
}

// errorRateQuery builds and executes the error-rate query for the selected data plane.
func (p *PrometheusClient) errorRateQuery(ctx context.Context, namespace, service, dataPlane, duration string) (float64, error) {
	var q string
	if dataPlane == "envoy" {
		q = fmt.Sprintf(
			`sum(rate(envoy_cluster_upstream_rq_xx{envoy_cluster_name=~".*%s.*", code=~"4..|5.."}[%s])) / sum(rate(envoy_cluster_upstream_rq_total{envoy_cluster_name=~".*%s.*"}[%s]))`,
			service, duration, service, duration)
	} else {
		q = fmt.Sprintf(
			`sum(rate(agentgateway_requests_total{namespace="%s", service="%s", code=~"4..|5.."}[%s])) / sum(rate(agentgateway_requests_total{namespace="%s", service="%s"}[%s]))`,
			namespace, service, duration, namespace, service, duration)
	}
	return p.queryWithRetry(ctx, q)
}

func (p *PrometheusClient) throughputQuery(ctx context.Context, namespace, service, dataPlane, duration string) (float64, error) {
	var q string
	if dataPlane == "envoy" {
		q = fmt.Sprintf(
			`sum(rate(envoy_cluster_upstream_rq_total{envoy_cluster_name=~".*%s.*"}[%s]))`,
			service, duration)
	} else {
		q = fmt.Sprintf(
			`sum(rate(agentgateway_requests_total{namespace="%s", service="%s"}[%s]))`,
			namespace, service, duration)
	}
	return p.queryWithRetry(ctx, q)
}

func (p *PrometheusClient) meanLatencyQuery(ctx context.Context, namespace, service, dataPlane, duration string) (float64, error) {
	var q string
	if dataPlane == "envoy" {
		// envoy_cluster_upstream_rq_time is reported in milliseconds already;
		// no unit conversion needed (do NOT multiply by 1000).
		q = fmt.Sprintf(
			`sum(rate(envoy_cluster_upstream_rq_time_sum{envoy_cluster_name=~".*%s.*"}[%s])) / sum(rate(envoy_cluster_upstream_rq_time_count{envoy_cluster_name=~".*%s.*"}[%s]))`,
			service, duration, service, duration)
	} else {
		q = fmt.Sprintf(
			`(sum(rate(agentgateway_request_duration_seconds_sum{namespace="%s", service="%s"}[%s])) / sum(rate(agentgateway_request_duration_seconds_count{namespace="%s", service="%s"}[%s]))) * 1000`,
			namespace, service, duration, namespace, service, duration)
	}
	return p.queryWithRetry(ctx, q)
}

// queryWithRetry executes a PromQL instant query with exponential-backoff retries.
// Retries occur on query errors or when no sample is returned yet. A sample value of
// zero is considered valid (for example, 0% error rate) and is returned immediately.
func (p *PrometheusClient) queryWithRetry(ctx context.Context, query string) (float64, error) {
	const attempts = 3
	var lastErr error
	missingSample := false
	for i := 0; i < attempts; i++ {
		val, hasSample, err := p.queryInstantWithStatus(ctx, query)
		if err == nil && hasSample {
			return val, nil
		}
		if err != nil {
			lastErr = err
		} else {
			missingSample = true
		}

		if i < attempts-1 {
			time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
		}
	}

	if lastErr != nil {
		return 0, lastErr
	}

	if missingSample {
		return 0, ErrNoSample
	}

	return 0, fmt.Errorf("prometheus query retry exhausted without result")
}

// queryInstantWithStatus executes a PromQL instant query and returns:
// - value: the first sample value
// - hasSample: whether at least one sample was returned
// - err: API query errors
func (p *PrometheusClient) queryInstantWithStatus(ctx context.Context, query string) (float64, bool, error) {
	val, _, err := p.api.Query(ctx, query, time.Now())
	if err != nil {
		return 0, false, fmt.Errorf("prometheus query failed: %w", err)
	}
	if val == nil {
		return 0, false, nil
	}
	if vector, ok := val.(model.Vector); ok && len(vector) > 0 {
		return float64(vector[0].Value), true, nil
	}
	return 0, false, nil
}

// queryInstant executes a PromQL instant query and returns the first scalar value.
// Returns 0 on any error or when no sample is available.
func (p *PrometheusClient) queryInstant(ctx context.Context, query string) float64 {
	val, hasSample, err := p.queryInstantWithStatus(ctx, query)
	if err != nil || !hasSample {
		return 0
	}

	return val
}
