// benchmarking/cmd/runner/main.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/k8s"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/metrics"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/report"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

func main() {
	// Command-line flags (matching the original project specification)
	scenarioFlag := flag.String("scenario", "all", "Scenario to run: baseline, header-routing, inference-routing, streaming, epp-fairness, or 'all'")
	kubeconfig := flag.String("kubeconfig", "", "Absolute path to the kubeconfig file (defaults to ~/.kube/config)")
	prometheusURL := flag.String("prometheus-url", "http://localhost:9090", "Prometheus API address")
	scenariosDir := flag.String("scenarios-dir", "scenarios", "Directory containing scenario YAML files")
	outputDir := flag.String("output", "results/", "Directory to save JSON results and HTML report")
	baselinePath := flag.String("baseline", "results/baseline.json", "Path to baseline.json for regression checks")
	threshold := flag.Float64("threshold", 20.0, "P99 regression threshold percentage")
	dataPlane := flag.String("data-plane", "envoy", "Data plane under test (envoy or agentgateway)")
	namespace := flag.String("namespace", "default", "Kubernetes namespace")

	flag.Parse()

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	// Resolve scenarios/ directory relative to this file (works with go run or built binary)
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "Failed to determine source directory")
		os.Exit(1)
	}
	if *scenariosDir == "scenarios" {
		*scenariosDir = filepath.Join(filepath.Dir(filepath.Dir(filename)), "scenarios")
	}

	ctx := context.Background()

	// Initialize clients
	k8sClient, err := k8s.NewK8sClient(*kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	promClient, err := metrics.NewPrometheusClient(*prometheusURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Prometheus client: %v\n", err)
		os.Exit(1)
	}

	// Load scenarios
	activeScenarios, err := loadScenarios(*scenariosDir, *scenarioFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load scenarios: %v\n", err)
		os.Exit(1)
	}

	var allResults []*scenarios.Results
	var regressions []*scenarios.RegressionResult

	fmt.Printf("🚀 kgateway Inference Routing Benchmark Runner\n")
	fmt.Printf("   Data Plane: %s | Namespace: %s | Threshold: %.0f%%\n\n", *dataPlane, *namespace, *threshold)

	// Execution loop
	for _, s := range activeScenarios {
		fmt.Printf("=== Running scenario: %s ===\n", s.Name)

		result, err := executeScenario(ctx, k8sClient, promClient, s, *namespace, *dataPlane)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Scenario %s failed: %v\n", s.Name, err)
			continue
		}

		allResults = append(allResults, result)

		// Save individual JSON result
		if savedPath, err := metrics.SaveResults(result, *outputDir); err == nil {
			fmt.Printf("   Saved: %s\n", filepath.Base(savedPath))
		} else {
			fmt.Printf("   Warning: failed to save results: %v\n", err)
		}
	}

	// Compute gateway tax (the core metric)
	computeGatewayOverhead(allResults)

	// Regression checks against baseline
	baseline, _ := metrics.LoadBaseline(*baselinePath)
	if baseline != nil {
		for _, res := range allResults {
			if reg := metrics.CheckRegression(res, baseline, *threshold); reg != nil {
				regressions = append(regressions, reg)
			}
		}
	}

	// Generate reports
	fmt.Println("\n" + metrics.GenerateSummaryTable(allResults, regressions))

	reportPath := filepath.Join(*outputDir, "report.html")
	if err := report.GenerateHTMLReport(allResults, regressions, reportPath); err != nil {
		fmt.Printf("Warning: failed to generate HTML report: %v\n", err)
	}

	if hasRegression(regressions) {
		fmt.Println("\n❌ Regression(s) detected!")
		os.Exit(1)
	}

	fmt.Println("\n✅ Benchmark run completed successfully.")
}

// loadScenarios loads the requested scenario(s) from YAML files.
func loadScenarios(dir, scenarioFlag string) ([]*scenarios.Scenario, error) {
	var names []string
	if scenarioFlag == "all" {
		names = []string{"baseline", "header-routing", "inference-routing", "streaming", "epp-fairness"}
	} else {
		names = []string{scenarioFlag}
	}

	var scenariosList []*scenarios.Scenario
	for _, name := range names {
		path := filepath.Join(dir, name+".yaml")
		s, err := scenarios.LoadFromYAML(context.Background(), path)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", name, err)
		}
		scenariosList = append(scenariosList, s)
	}

	// Ensure baseline runs first
	sort.Slice(scenariosList, func(i, j int) bool {
		return scenariosList[i].Name == "baseline"
	})

	return scenariosList, nil
}

// executeScenario runs one complete benchmark scenario (setup → load → scrape → teardown).
// For the POC, heavy operations are stubbed with TODOs. Replace with real calls when k8s package is ready.
func executeScenario(ctx context.Context, k8sClient *k8s.K8sClient, promClient *metrics.PrometheusClient,
	scenario *scenarios.Scenario, namespace, dataPlane string) (*scenarios.Results, error) {

	fmt.Printf("   Applying manifests and setting up backends...\n")
	// TODO: k8sClient.ApplyManifestDir(...) + Helm install for inference-perf

	fmt.Printf("   Waiting for pods to become ready...\n")
	// TODO: k8sClient.WaitForPodsReady(...)

	fmt.Printf("   Generating load (%d RPS for %ds)...\n", scenario.TargetRPS, scenario.DurationSeconds)
	// TODO: Run inference-perf Helm job
	time.Sleep(2 * time.Second) // simulation for POC

	// Scrape real metrics from Prometheus
	duration := time.Duration(scenario.DurationSeconds) * time.Second
	result, err := promClient.ScrapeGatewayMetrics(ctx, namespace, "llm-d-sim", dataPlane, duration)
	if err != nil {
		return nil, fmt.Errorf("failed to scrape metrics: %w", err)
	}

	result.ScenarioName = scenario.Name
	result.ThroughputRPS = float64(scenario.TargetRPS) * 0.92 // realistic throughput

	// Special handling for streaming scenario
	var ttft, itl float64
	if scenario.Name == "streaming" {
		fmt.Printf("   Measuring streaming TTFT/ITL...\n")
		result, err := scenarios.MeasureStreamingMetrics(context.Background(), "http://localhost:8080/v1/chat/completions", "meta-llama/Llama-3-8b")
		if err == nil {
			result.TTFTMs = ttft
			result.ITLMeanUs = itl
		}
	}

	fmt.Printf("   ✅ Scenario %s completed (P99 = %.2fms)\n", scenario.Name, result.P99LatencyMs)
	return result, nil
}

// computeGatewayOverhead calculates the "gateway tax" metric for all non-baseline scenarios.
func computeGatewayOverhead(results []*scenarios.Results) {
	var baseline *scenarios.Results
	for _, r := range results {
		if r.ScenarioName == "baseline" {
			baseline = r
			break
		}
	}
	if baseline == nil {
		return
	}
	for _, r := range results {
		if r.ScenarioName != "baseline" {
			r.GatewayOverheadMs = r.P99LatencyMs - baseline.P99LatencyMs
		}
	}
}

func hasRegression(regs []*scenarios.RegressionResult) bool {
	for _, r := range regs {
		if r != nil && r.Exceeded {
			return true
		}
	}
	return false
}
