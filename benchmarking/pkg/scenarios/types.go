package scenarios

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario defines a single benchmarking scenario loaded from YAML files in benchmarking/scenarios/.
type Scenario struct {
	Name                   string        `yaml:"name"`
	Description            string        `yaml:"description"`
	GatewayClass           string        `yaml:"gatewayClass"`
	EnableInferenceRouting bool          `yaml:"enableInferenceRouting"`
	EnableBodyParsing      bool          `yaml:"enableBodyParsing"`
	TargetRPS              int           `yaml:"targetRPS"`
	DurationSeconds        int           `yaml:"durationSeconds"`
	ConcurrentUsers        int           `yaml:"concurrentUsers"`
	WarmupSeconds          int           `yaml:"warmupSeconds"`
	BackendTiers           []BackendTier `yaml:"backendTiers"`
}

// BackendTier represents one class of simulator backend pods (large/medium/small).
type BackendTier struct {
	Name        string `yaml:"name"`
	CPULimit    string `yaml:"cpuLimit"`
	MemoryLimit string `yaml:"memoryLimit"`
	// ResponseDelayMs models compute capability analogous to MIG slice sizes:
	// low delay = large slice (3g.20gb), high delay = constrained slice (1g.10gb).
	ResponseDelayMs int               `yaml:"responseDelayMs"`
	Replicas        int               `yaml:"replicas"`
	Labels          map[string]string `yaml:"labels"`
	// SimulatedKVCachePercent (0-100) is the fake KV cache utilization the simulator
	// reports to Prometheus. Per-tier so a single run can have e.g. tier-large at 30%
	// and tier-small at 80%, creating the pressure skew the EPP needs to route meaningfully.
	SimulatedKVCachePercent int `yaml:"simulatedKVCachePercent,omitempty"`
}

// Results holds all collected metrics for one completed scenario run.
type Results struct {
	ScenarioName string    `json:"scenarioName"`
	DataPlane    string    `json:"dataPlane"` // "envoy" or "agentgateway"
	Timestamp    time.Time `json:"timestamp"`

	P50LatencyMs  float64 `json:"p50LatencyMs"`
	P95LatencyMs  float64 `json:"p95LatencyMs"`
	P99LatencyMs  float64 `json:"p99LatencyMs"`
	MeanLatencyMs float64 `json:"meanLatencyMs"`

	// Streaming metrics — only populated for the "streaming" scenario.
	TTFTMeanMs float64 `json:"ttftMeanMs"`
	TTFTp99Ms  float64 `json:"ttftP99Ms"`
	ITLMeanUs  float64 `json:"itlMeanUs"`

	ThroughputRPS        float64 `json:"throughputRPS"`
	ErrorRate            float64 `json:"errorRate"`
	GatewayCPUMillicores float64 `json:"gatewayCPUMillicores"`
	GatewayMemoryMB      float64 `json:"gatewayMemoryMB"`
	EPPDecisionLatencyMs float64 `json:"eppDecisionLatencyMs"`

	// GatewayOverheadMs is the "gateway tax": P99 of this scenario minus S1 baseline P99.
	// Calculated by the runner after all scenarios complete, not scraped from Prometheus.
	GatewayOverheadMs float64 `json:"gatewayOverheadMs"`

	// BaselineFile records which baseline JSON was used for regression comparison.
	BaselineFile string `json:"baselineFile,omitempty"`
}

// RegressionResult captures the outcome of comparing a current run against a saved baseline.
type RegressionResult struct {
	ScenarioName string  `json:"scenarioName"`
	BaselineP99  float64 `json:"baselineP99"`
	CurrentP99   float64 `json:"currentP99"`
	DeltaPct     float64 `json:"deltaPct"`
	Exceeded     bool    `json:"exceeded"`
	Threshold    float64 `json:"threshold"`
}

// LoadFromYAML reads and validates a scenario definition from the given YAML file.
func LoadFromYAML(ctx context.Context, path string) (*Scenario, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("load cancelled before reading %s: %w", path, ctx.Err())
	default:
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenario file %s: %w", path, err)
	}

	var s Scenario
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML from %s: %w", path, err)
	}

	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("invalid scenario %s: %w", path, err)
	}

	return &s, nil
}

func (s *Scenario) validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if s.TargetRPS <= 0 {
		return fmt.Errorf("targetRPS must be > 0")
	}
	if s.DurationSeconds <= 0 {
		return fmt.Errorf("durationSeconds must be > 0")
	}
	if len(s.BackendTiers) == 0 {
		return fmt.Errorf("at least one backend tier is required")
	}
	for i, tier := range s.BackendTiers {
		if tier.Name == "" {
			return fmt.Errorf("backendTiers[%d].name is required", i)
		}
		if tier.ResponseDelayMs < 0 {
			return fmt.Errorf("backendTiers[%d].responseDelayMs cannot be negative", i)
		}
		if tier.SimulatedKVCachePercent < 0 || tier.SimulatedKVCachePercent > 100 {
			return fmt.Errorf("backendTiers[%d].simulatedKVCachePercent must be 0-100", i)
		}
	}
	return nil
}
