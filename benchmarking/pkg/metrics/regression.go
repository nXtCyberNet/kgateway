package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// CheckRegression compares a current benchmark result against a saved baseline
// and returns a RegressionResult. It fails on either a P99 latency regression
// OR a significant error-rate increase (silent failures).
func CheckRegression(current, baseline *scenarios.Results, thresholdPct float64) *scenarios.RegressionResult {
	if baseline == nil {
		return nil
	}

	reg := &scenarios.RegressionResult{
		ScenarioName: current.ScenarioName,
		BaselineP99:  baseline.P99LatencyMs,
		CurrentP99:   current.P99LatencyMs,
		Threshold:    thresholdPct,
	}

	if baseline.P99LatencyMs > 0 {
		reg.DeltaPct = ((current.P99LatencyMs - baseline.P99LatencyMs) / baseline.P99LatencyMs) * 100
	}

	// Latency regression
	if current.P99LatencyMs > (baseline.P99LatencyMs * (1 + thresholdPct/100)) {
		reg.Exceeded = true
	}

	// Error-rate regression (absolute increase > 1 percentage point)
	const errorRateThreshold = 0.01
	if (current.ErrorRate - baseline.ErrorRate) > errorRateThreshold {
		reg.Exceeded = true
	}

	return reg
}

// LoadBaseline reads the saved baseline.json file and returns the reference Results.
func LoadBaseline(path string) (*scenarios.Results, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read baseline %s: %w", path, err)
	}
	var res scenarios.Results
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal baseline %s: %w", path, err)
	}
	return &res, nil
}

// SaveResults writes the result to disk and returns the full path of the created file.
func SaveResults(res *scenarios.Results, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	fileName := fmt.Sprintf("%s_%s.json", res.ScenarioName, res.Timestamp.Format("20060102-150405"))
	fullPath := filepath.Join(outputDir, fileName)

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write results file: %w", err)
	}
	return fullPath, nil
}

// GenerateSummaryTable produces a clean ASCII table for stdout that includes
// the core "gateway tax" metric and all important columns.
func GenerateSummaryTable(results []*scenarios.Results, regressions []*scenarios.RegressionResult) string {
	header := fmt.Sprintf("%-20s | %-10s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s\n",
		"Scenario", "DataPlane", "P99(ms)", "Overhead", "TTFT(ms)", "ITL(µs)", "RPS", "CPU(m)", "Mem(MB)")
	divider := "----------------------------------------------------------------------------------------------------\n"

	table := header + divider
	for _, r := range results {
		overhead := "-"
		if r.GatewayOverheadMs > 0 {
			overhead = fmt.Sprintf("%.2f", r.GatewayOverheadMs)
		}
		table += fmt.Sprintf("%-20s | %-10s | %-8.2f | %-8s | %-8.2f | %-8.0f | %-8.1f | %-8.0f | %-8.0f\n",
			r.ScenarioName,
			r.DataPlane,
			r.P99LatencyMs,
			overhead,
			r.TTFTMeanMs,
			r.ITLMeanUs,
			r.ThroughputRPS,
			r.GatewayCPUMillicores,
			r.GatewayMemoryMB,
		)
	}
	return table
}
