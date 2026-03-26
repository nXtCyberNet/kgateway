package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// CheckRegression compares current results against a baseline.
func CheckRegression(current, baseline *scenarios.Results, thresholdPct float64) *scenarios.RegressionResult {
	delta := ((current.P99LatencyMs - baseline.P99LatencyMs) / baseline.P99LatencyMs) * 100
	return &scenarios.RegressionResult{
		ScenarioName: current.ScenarioName,
		BaselineP99:  baseline.P99LatencyMs,
		CurrentP99:   current.P99LatencyMs,
		DeltaPct:     delta,
		Exceeded:     delta > thresholdPct,
		Threshold:    thresholdPct,
	}
}

// LoadBaseline reads the baseline results from a JSON file.
func LoadBaseline(path string) (*scenarios.Results, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read baseline file: %w", err)
	}
	var res scenarios.Results
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal baseline: %w", err)
	}
	return &res, nil
}

// SaveResults persists scenario results to the specified output directory.
func SaveResults(results *scenarios.Results, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	fileName := fmt.Sprintf("%d_%s.json", results.Timestamp.Unix(), results.ScenarioName)
	path := filepath.Join(outputDir, fileName)

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write results file: %w", err)
	}

	return path, nil
}

// GenerateSummaryTable produces a human-readable ASCII table of the results.
func GenerateSummaryTable(results []*scenarios.Results, regressions []*scenarios.RegressionResult) string {
	table := "SCENARIO               | P99 (ms) | OVERHEAD (ms) | RPS   | REGRESSION\n"
	table += "-----------------------|----------|---------------|-------|-----------\n"
	for i, r := range results {
		regText := "PASS"
		if i < len(regressions) && regressions[i].Exceeded {
			regText = fmt.Sprintf("FAIL (+%.1f%%)", regressions[i].DeltaPct)
		}
		table += fmt.Sprintf("%-22s | %8.2f | %13.2f | %5.1f | %s\n",
			r.ScenarioName, r.P99LatencyMs, r.GatewayOverheadMs, r.ThroughputRPS, regText)
	}
	return table
}
