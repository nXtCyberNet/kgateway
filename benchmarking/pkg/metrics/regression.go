package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// CheckRegression compares current results against baseline data to detect slowdowns
func CheckRegression(current, baseline *scenarios.Results, thresholdPct float64) *scenarios.RegressionResult {
	if baseline == nil {
		return nil
	}
	deltaPct := ((current.P99LatencyMs - baseline.P99LatencyMs) / baseline.P99LatencyMs) * 100.0
	exceeded := deltaPct > thresholdPct
	return &scenarios.RegressionResult{
		ScenarioName: current.ScenarioName,
		BaselineP99:  baseline.P99LatencyMs,
		CurrentP99:   current.P99LatencyMs,
		DeltaPct:     deltaPct,
		Exceeded:     exceeded,
		Threshold:    thresholdPct,
	}
}

// LoadBaseline loads previously saved JSON benchmark baseline
func LoadBaseline(path string) (*scenarios.Results, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read baseline file %s: %w", path, err)
	}
	var res scenarios.Results
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("could not decode baseline data: %w", err)
	}
	return &res, nil
}

// SaveResults dumps result JSON to outputDir, returning resulting path
func SaveResults(results *scenarios.Results, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("create output directory failed: %w", err)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("could not marshal performance results: %w", err)
	}
	timestamp := time.Now().Format("20060102150405")
	filename := fmt.Sprintf("%s_%s.json", timestamp, results.ScenarioName)
	path := filepath.Join(outputDir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write results to disk failed: %w", err)
	}
	return path, nil
}

// GenerateSummaryTable formats metrics into a simple console matrix
func GenerateSummaryTable(results []*scenarios.Results, regressions []*scenarios.RegressionResult) string {
	res := "Scenario | P99 (ms) | Throughput (RPS) | Error Rate | OH (ms) | CPU (m) | Mem (MB)\n"
	res += "-----------------------------------------------------------------------------------\n"
	for _, r := range results {
		res += fmt.Sprintf("%-8s | %-8.2f | %-16.2f | %-10.2f | %-7.2f | %-7.0f | %-8.0f\n",
			r.ScenarioName, r.P99LatencyMs, r.ThroughputRPS, r.ErrorRate, r.GatewayOverheadMs, r.GatewayCPUMillicores, r.GatewayMemoryMB)
	}

	// Add regression blocks at bottom if provided
	if len(regressions) > 0 {
		res += "\nRegressions Detected:\n"
		for _, reg := range regressions {
			if reg.Exceeded {
				res += fmt.Sprintf(" - %s: base %.1fms, current %.1fms (%.1f%% worse)\n", reg.ScenarioName, reg.BaselineP99, reg.CurrentP99, reg.DeltaPct)
			}
		}
	}
	return res
}
