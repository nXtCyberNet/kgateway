package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// PrometheusClient provides an interface to query Prometheus metrics.
type PrometheusClient struct {
	BaseURL string
}

// GatewayMetrics holds raw performance and resource usage data for the gateway.
type GatewayMetrics struct {
	P99Latency float64
	P95Latency float64
	P50Latency float64
	CPUUsage   float64
	MemoryMB   float64
	EPPLatency float64
}

type prometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value []interface{} `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// QueryInstant executes a Prometheus instant query and returns the first float result.
func (p *PrometheusClient) QueryInstant(ctx context.Context, query string) (float64, error) {
	u, err := url.Parse(fmt.Sprintf("%s/api/v1/query", p.BaseURL))
	if err != nil {
		return 0, err
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("prometheus request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var pr prometheusResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return 0, fmt.Errorf("failed to unmarshal prometheus response: %w", err)
	}

	if len(pr.Data.Result) == 0 {
		return 0, nil
	}

	valStr := pr.Data.Result[0].Value[1].(string)
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse result value %s: %w", valStr, err)
	}

	return val, nil
}

// ScrapeGatewayMetrics gathers performance metrics for a specific kgateway instance.
func (p *PrometheusClient) ScrapeGatewayMetrics(ctx context.Context, namespace, podName string) (*GatewayMetrics, error) {
	m := &GatewayMetrics{}
	var err error

	m.P99Latency, err = p.QueryInstant(ctx, `histogram_quantile(0.99, sum by (le) (rate(envoy_cluster_upstream_rq_time_bucket[1m])))`)
	if err != nil {
		return nil, err
	}

	m.P95Latency, err = p.QueryInstant(ctx, `histogram_quantile(0.95, sum by (le) (rate(envoy_cluster_upstream_rq_time_bucket[1m])))`)
	if err != nil {
		return nil, err
	}

	m.P50Latency, err = p.QueryInstant(ctx, `histogram_quantile(0.50, sum by (le) (rate(envoy_cluster_upstream_rq_time_bucket[1m])))`)
	if err != nil {
		return nil, err
	}

	m.CPUUsage, err = p.QueryInstant(ctx, fmt.Sprintf(`rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"kgateway.*"}[1m]) * 1000`, namespace))
	if err != nil {
		return nil, err
	}

	m.MemoryMB, err = p.QueryInstant(ctx, fmt.Sprintf(`container_memory_working_set_bytes{namespace="%s",pod=~"kgateway.*"} / 1024 / 1024`, namespace))
	if err != nil {
		return nil, err
	}

	m.EPPLatency, err = p.QueryInstant(ctx, `histogram_quantile(0.99, sum by (le) (rate(epp_endpoint_selection_duration_seconds_bucket[1m]))) * 1000`)
	if err != nil {
		return nil, err
	}

	return m, nil
}
