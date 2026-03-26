package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// PrometheusClient interacts with the Prometheus HTTP API
type PrometheusClient struct {
	baseURL string
}

// GatewayMetrics holds the specific scraped metrics for the gateway
type GatewayMetrics struct {
	P99                  float64
	P95                  float64
	P50                  float64
	GatewayCPUMillicores float64
	GatewayMemoryMB      float64
	EPPDecisionLatency   float64
}

// prometheusAPIResponse maps the standard JSON output of /api/v1/query
type prometheusAPIResponse struct {
	Data struct {
		Result []struct {
			Value []interface{} `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// NewPrometheusClient creates a new PrometheusClient
func NewPrometheusClient(baseURL string) *PrometheusClient {
	return &PrometheusClient{baseURL: baseURL}
}

// QueryInstant executes a direct single instant query against Prometheus HTTP API
func (p *PrometheusClient) QueryInstant(ctx context.Context, query string) (float64, error) {
	reqURL := fmt.Sprintf("%s/api/v1/query?query=%s", p.baseURL, url.QueryEscape(query))
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create query request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("execute prometheus query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus returned status: %s", resp.Status)
	}

	return parsePrometheusResponse(resp)
}

// parsePrometheusResponse deserializes the struct to extract the single float magnitude
func parsePrometheusResponse(resp *http.Response) (float64, error) {
	var result prometheusAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode prometheus response: %w", err)
	}

	if len(result.Data.Result) == 0 || len(result.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("no data returned for prometheus query inside response payload")
	}

	valStr, ok := result.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("unexpected value type in prometheus response")
	}

	var val float64
	if _, err := fmt.Sscanf(valStr, "%f", &val); err != nil {
		return 0, fmt.Errorf("parse prometheus value: %w", err)
	}

	return val, nil
}

// ScrapeGatewayMetrics gathers full proxy container stats mapped for the report payload
func (p *PrometheusClient) ScrapeGatewayMetrics(ctx context.Context, namespace, podName string) (*GatewayMetrics, error) {
	metrics := &GatewayMetrics{}
	queries := buildGatewayQueries(namespace, podName, metrics)

	for query, target := range queries {
		val, err := p.QueryInstant(ctx, query)
		if err != nil {
			// On single query parse failures, record 0 but continue moving to not ruin entire run context
			*target = 0
		} else {
			*target = val
		}
	}

	return metrics, nil
}

// buildGatewayQueries generates the map referencing query string values via reference map pointers
func buildGatewayQueries(namespace, podName string, m *GatewayMetrics) map[string]*float64 {
	return map[string]*float64{
		`histogram_quantile(0.99, rate(envoy_cluster_upstream_rq_time_bucket[1m]))`: &m.P99,
		`histogram_quantile(0.95, rate(envoy_cluster_upstream_rq_time_bucket[1m]))`: &m.P95,
		`histogram_quantile(0.50, rate(envoy_cluster_upstream_rq_time_bucket[1m]))`: &m.P50,
		fmt.Sprintf(`rate(container_cpu_usage_seconds_total{pod="%s",namespace="%s"}[1m]) * 1000`, podName, namespace): &m.GatewayCPUMillicores,
		fmt.Sprintf(`container_memory_working_set_bytes{pod="%s",namespace="%s"} / 1024 / 1024`, podName, namespace): &m.GatewayMemoryMB,
		`histogram_quantile(0.99, rate(epp_endpoint_selection_duration_seconds_bucket[1m])) * 1000`: &m.EPPDecisionLatency,
	}
}
