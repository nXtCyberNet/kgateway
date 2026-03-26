package scenarios

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// BackendTier defines the resource constraints and behavior for a group of simulator pods.
type BackendTier struct {
	Name            string            `yaml:"name"`
	CPULimit        string            `yaml:"cpuLimit"`
	MemoryLimit     string            `yaml:"memoryLimit"`
	ResponseDelayMs int               `yaml:"responseDelayMs"`
	Replicas        int               `yaml:"replicas"`
	Labels          map[string]string `yaml:"labels"`
}

// Scenario represents a single benchmark configuration.
type Scenario struct {
	Name                   string        `yaml:"name"`
	Description            string        `yaml:"description"`
	GatewayClass           string        `yaml:"gatewayClass"`
	EnableInferenceRouting bool          `yaml:"enableInferenceRouting"`
	EnableBodyParsing      bool          `yaml:"enableBodyParsing"`
	TargetRPS              int           `yaml:"targetRps"`
	DurationSeconds        int           `yaml:"durationSeconds"`
	ConcurrentUsers        int           `yaml:"concurrentUsers"`
	WarmupSeconds          int           `yaml:"warmupSeconds"`
	BackendTiers           []BackendTier `yaml:"backendTiers"`
}

// Results holds the telemetry captured during a scenario run.
type Results struct {
	ScenarioName         string    `json:"scenario_name"`
	DataPlane            string    `json:"data_plane"` // "envoy" or "agentgateway"
	Timestamp            time.Time `json:"timestamp"`
	P50LatencyMs         float64   `json:"p50_latency_ms"`
	P95LatencyMs         float64   `json:"p95_latency_ms"`
	P99LatencyMs         float64   `json:"p99_latency_ms"`
	MeanLatencyMs        float64   `json:"mean_latency_ms"`
	TTFTMeanMs           float64   `json:"ttft_mean_ms"`
	TTFTp99Ms            float64   `json:"ttft_p99_ms"`
	ITLMeanUs            float64   `json:"itl_mean_us"`
	ThroughputRPS        float64   `json:"throughput_rps"`
	ErrorRate            float64   `json:"error_rate"`
	GatewayCPUMillicores float64   `json:"gateway_cpu_millicores"`
	GatewayMemoryMB      float64   `json:"gateway_memory_mb"`
	EPPDecisionLatencyMs float64   `json:"epp_decision_latency_ms"`
	GatewayOverheadMs    float64   `json:"gateway_overhead_ms"`
}

// RegressionResult represents the delta between current and baseline performance.
type RegressionResult struct {
	ScenarioName string  `json:"scenario_name"`
	BaselineP99  float64 `json:"baseline_p99"`
	CurrentP99   float64 `json:"current_p99"`
	DeltaPct     float64 `json:"delta_pct"`
	Exceeded     bool    `json:"exceeded"`
	Threshold    float64 `json:"threshold"`
}

// LoadFromYAML reads a scenario definition from a file.
func LoadFromYAML(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenario file %s: %w", path, err)
	}

	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("failed to unmarshal scenario yaml: %w", err)
	}

	return &s, nil
}
