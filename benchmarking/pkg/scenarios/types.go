package scenarios

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Scenario represents the load test configuration and backend requirements
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
	BackendTiers           []BackendTier `yaml:"backendTiers"` // Tiers or pool config
}

// BackendTier defines the configuration for a simulated LLM backend pool
type BackendTier struct {
	Name            string            `yaml:"name"`
	CPULimit        string            `yaml:"cpuLimit"`
	MemoryLimit     string            `yaml:"memoryLimit"`
	ResponseDelayMs int               `yaml:"responseDelayMs"`
	Replicas        int               `yaml:"replicas"`
	Labels          map[string]string `yaml:"labels"`
}

// Results stores the output metrics of a specific benchmark run
type Results struct {
	ScenarioName         string  `json:"scenarioName"`
	DataPlane            string  `json:"dataPlane"`
	Timestamp            string  `json:"timestamp"`
	P50LatencyMs         float64 `json:"p50LatencyMs"`
	P95LatencyMs         float64 `json:"p95LatencyMs"`
	P99LatencyMs         float64 `json:"p99LatencyMs"`
	MeanLatencyMs        float64 `json:"meanLatencyMs"`
	TTFTMeanMs           float64 `json:"ttftMeanMs"`
	TTFTp99Ms            float64 `json:"ttftP99Ms"`
	ITLMeanUs            float64 `json:"itlMeanUs"`
	ThroughputRPS        float64 `json:"throughputRps"`
	ErrorRate            float64 `json:"errorRate"`
	GatewayCPUMillicores float64 `json:"gatewayCpuMillicores"`
	GatewayMemoryMB      float64 `json:"gatewayMemoryMb"`
	EPPDecisionLatencyMs float64 `json:"eppDecisionLatencyMs"`
	GatewayOverheadMs    float64 `json:"gatewayOverheadMs"`
}

// RegressionResult represents the outcome of comparing a run to a baseline
type RegressionResult struct {
	ScenarioName string  `json:"scenarioName"`
	BaselineP99  float64 `json:"baselineP99"`
	CurrentP99   float64 `json:"currentP99"`
	DeltaPct     float64 `json:"deltaPct"`
	Exceeded     bool    `json:"exceeded"`
	Threshold    float64 `json:"threshold"`
}

// LoadFromYAML attempts to parse a given yaml file into a Scenario
func LoadFromYAML(path string) (*Scenario, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario file: %w", err)
	}

	var sc Scenario
	if err := yaml.Unmarshal(bytes, &sc); err != nil {
		return nil, fmt.Errorf("unmarshal scenario file: %w", err)
	}

	return &sc, nil
}
