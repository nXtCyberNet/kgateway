// benchmarking/cmd/runner/main.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/k8s"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/metrics"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/report"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

func main() {
	scenarioFlag := flag.String("scenario", "all", "Scenario to run: baseline, header-routing, inference-routing, streaming, epp-fairness, or 'all'")
	kubeconfig := flag.String("kubeconfig", "", "Absolute path to the kubeconfig file (defaults to ~/.kube/config)")
	prometheusURL := flag.String("prometheus-url", "http://localhost:9090", "Prometheus API address")
	scenariosDir := flag.String("scenarios-dir", "scenarios", "Directory containing scenario YAML files")
	outputDir := flag.String("output", "results/", "Directory to save JSON results and HTML report")
	baselinePath := flag.String("baseline", "results/baseline.json", "Path to baseline.json for regression checks")
	threshold := flag.Float64("threshold", 20.0, "P99 regression threshold percentage")
	dataPlane := flag.String("data-plane", "envoy", "Data plane under test: envoy or agentgateway")
	namespace := flag.String("namespace", "default", "Kubernetes namespace")
	// --stub skips real Kubernetes/Helm calls and returns synthetic metric values.
	// Use this to validate the reporting pipeline without a live cluster.
	stub := flag.Bool("stub", false, "Run in stub mode (no real K8s/load calls, synthetic metrics)")

	flag.Parse()

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	// Resolve scenarios/ relative to this source file so the binary works
	// regardless of invocation directory.
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "Failed to determine source directory")
		os.Exit(1)
	}
	if *scenariosDir == "scenarios" {
		*scenariosDir = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filename))), "scenarios")
	}

	ctx := context.Background()

	// In stub mode skip all cluster / Prometheus connectivity — only the
	// reporting pipeline is exercised, so no real clients are needed.
	var k8sClient *k8s.K8sClient
	var promClient *metrics.PrometheusClient
	if !*stub {
		var err error
		k8sClient, err = k8s.NewK8sClient(*kubeconfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create Kubernetes client: %v\n", err)
			os.Exit(1)
		}

		promClient, err = metrics.NewPrometheusClient(*prometheusURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create Prometheus client: %v\n", err)
			os.Exit(1)
		}
	}

	activeScenarios, err := loadScenarios(ctx, *scenariosDir, *scenarioFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load scenarios: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🚀 kgateway Inference Routing Benchmark Runner\n")
	fmt.Printf("   Data Plane : %s\n", *dataPlane)
	fmt.Printf("   Namespace  : %s\n", *namespace)
	fmt.Printf("   Threshold  : %.0f%%\n", *threshold)
	if *stub {
		fmt.Println("   ⚠️  STUB MODE — results are synthetic, no real cluster calls are made")
	}
	fmt.Println()

	// Load baseline — surface the error explicitly so users know regression
	// checks are skipped rather than silently passing.
	baseline, baselineErr := metrics.LoadBaseline(*baselinePath)
	if baselineErr != nil {
		fmt.Printf("   ⚠️  Baseline not loaded (%v) — regression checks will be skipped\n\n", baselineErr)
	}

	var allResults []*scenarios.Results
	var regressions []*scenarios.RegressionResult
	failedCount := 0

	for _, s := range activeScenarios {
		fmt.Printf("=== Running scenario: %s ===\n", s.Name)

		result, err := executeScenario(ctx, k8sClient, promClient, s, *namespace, *dataPlane, *stub)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Scenario %s failed: %v\n", s.Name, err)
			failedCount++
			continue
		}

		// Annotate which baseline file was compared so JSON is self-describing.
		if baseline != nil {
			result.BaselineFile = *baselinePath
		}

		allResults = append(allResults, result)

		if savedPath, err := metrics.SaveResults(result, *outputDir); err == nil {
			fmt.Printf("   Saved: %s\n", filepath.Base(savedPath))
		} else {
			fmt.Printf("   Warning: failed to save results: %v\n", err)
		}

		if result.ScenarioName == "baseline" {
			if err := saveBaselineJSON(*baselinePath, result); err != nil {
				fmt.Printf("   Warning: failed to update baseline file %s: %v\n", *baselinePath, err)
			} else {
				fmt.Printf("   Baseline updated: %s\n", *baselinePath)
				baseline = result
			}
		}
	}

	// Guard: if every scenario failed there is nothing useful to report.
	if len(allResults) == 0 {
		fmt.Fprintln(os.Stderr, "\n❌ All scenarios failed — no results to report.")
		os.Exit(1)
	}

	// Calculate "gateway tax": each non-baseline scenario's P99 minus baseline P99.
	computeGatewayOverhead(allResults)

	// Regression checks against saved baseline.
	if baseline != nil {
		for _, res := range allResults {
			if reg := metrics.CheckRegression(res, baseline, *threshold); reg != nil {
				regressions = append(regressions, reg)
			}
		}
	}

	fmt.Println("\n" + metrics.GenerateSummaryTable(allResults, regressions))

	reportPath := filepath.Join(*outputDir, "report.html")
	if err := report.GenerateHTMLReport(allResults, regressions, reportPath); err != nil {
		fmt.Printf("Warning: failed to generate HTML report: %v\n", err)
	} else {
		fmt.Printf("✅ HTML report: %s\n", reportPath)
	}

	// Exit non-zero if any regressions were detected OR any scenario failed.
	if hasRegression(regressions) {
		fmt.Println("\n❌ Regression(s) detected — see report for details.")
		os.Exit(1)
	}
	if failedCount > 0 {
		fmt.Fprintf(os.Stderr, "\n❌ %d scenario(s) failed.\n", failedCount)
		os.Exit(1)
	}

	fmt.Println("\n✅ Benchmark run completed successfully.")
}

// loadScenarios loads the requested scenario(s) from YAML files in dir.
// Baseline is sorted first so computeGatewayOverhead always has a reference point.
func loadScenarios(ctx context.Context, dir, scenarioFlag string) ([]*scenarios.Scenario, error) {
	var names []string
	if scenarioFlag == "all" {
		names = []string{"baseline", "header-routing", "inference-routing", "streaming", "epp-fairness"}
	} else {
		names = []string{scenarioFlag}
	}

	var list []*scenarios.Scenario
	for _, name := range names {
		path := filepath.Join(dir, name+".yaml")
		s, err := scenarios.LoadFromYAML(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", name, err)
		}
		list = append(list, s)
	}

	// Robust total ordering: baseline always first, rest alphabetical.
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name == "baseline" {
			return true
		}
		if list[j].Name == "baseline" {
			return false
		}
		return list[i].Name < list[j].Name
	})

	return list, nil
}

// executeScenario runs one complete benchmark scenario: setup → warmup → load → scrape → teardown.
// When stub is true the Kubernetes/Helm calls are skipped and synthetic metric values are returned
// so the reporting pipeline can be validated without a live cluster.
func executeScenario(
	ctx context.Context,
	k8sClient *k8s.K8sClient,
	promClient *metrics.PrometheusClient,
	scenario *scenarios.Scenario,
	namespace, dataPlane string,
	stub bool,
) (*scenarios.Results, error) {

	if stub {
		return syntheticResults(scenario, dataPlane), nil
	}

	// ── Setup ────────────────────────────────────────────────────────────────
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("failed to determine runner source path")
	}
	benchmarkingRoot := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
	manifestsRoot := filepath.Join(benchmarkingRoot, "manifests")

	simulatorDir := filepath.Join(manifestsRoot, "simulator")
	appliedFiles := make([]string, 0, 3)

	fmt.Printf("   Applying manifests...\n")
	if err := k8sClient.ApplyManifestDir(ctx, simulatorDir); err != nil {
		return nil, fmt.Errorf("apply simulator manifests: %w", err)
	}

	gatewayFile := filepath.Join(manifestsRoot, "gateway.yaml")
	dataPlaneGatewayFile := filepath.Join(manifestsRoot, dataPlane, "gateway.yaml")
	if _, err := os.Stat(dataPlaneGatewayFile); err == nil {
		gatewayFile = dataPlaneGatewayFile
	}
	if err := k8sClient.ApplyManifestFile(ctx, gatewayFile); err != nil {
		return nil, fmt.Errorf("apply gateway manifest %s: %w", gatewayFile, err)
	}
	appliedFiles = append(appliedFiles, gatewayFile)

	// Apply HTTPRoute so the gateway has routing rules. Without this the gateway
	// listener returns 404 for every request and no upstream metrics are emitted.
	httprouteFile := filepath.Join(manifestsRoot, "httproute.yaml")
	if err := k8sClient.ApplyManifestFile(ctx, httprouteFile); err != nil {
		return nil, fmt.Errorf("apply httproute manifest %s: %w", httprouteFile, err)
	}
	appliedFiles = append(appliedFiles, httprouteFile)

	if scenario.EnableInferenceRouting {
		inferencePoolFile := filepath.Join(manifestsRoot, "inference-pool.yaml")
		appliedPoolFile, err := applyInferenceManifestWithFallback(ctx, k8sClient, manifestsRoot, inferencePoolFile)
		if err != nil {
			return nil, fmt.Errorf("apply inference-pool manifest: %w", err)
		}
		appliedFiles = append(appliedFiles, appliedPoolFile)

		inferenceModelFile := filepath.Join(manifestsRoot, "inference-model.yaml")
		appliedModelFile, err := applyInferenceManifestWithFallback(ctx, k8sClient, manifestsRoot, inferenceModelFile)
		if err != nil {
			return nil, fmt.Errorf("apply inference-model manifest: %w", err)
		}
		appliedFiles = append(appliedFiles, appliedModelFile)
	}

	defer func() {
		if err := k8sClient.HelmUninstall(ctx, "inference-perf", namespace); err != nil {
			fmt.Printf("   Warning: helm uninstall failed: %v\n", err)
		}
		for i := len(appliedFiles) - 1; i >= 0; i-- {
			if err := k8sClient.DeleteManifestFile(ctx, appliedFiles[i]); err != nil {
				fmt.Printf("   Warning: delete manifest file failed (%s): %v\n", appliedFiles[i], err)
			}
		}
		if err := k8sClient.DeleteManifestDir(ctx, simulatorDir); err != nil {
			fmt.Printf("   Warning: delete simulator manifests failed: %v\n", err)
		}
	}()

	// Count total expected replicas across all tiers for the ready check.
	totalReplicas := 0
	for _, t := range scenario.BackendTiers {
		totalReplicas += t.Replicas
	}
	if totalReplicas == 0 {
		totalReplicas = 1
	}

	fmt.Printf("   Waiting for %d pod(s)...\n", totalReplicas)
	if err := k8sClient.WaitForPodsReady(ctx, namespace, "app=llm-d-sim", totalReplicas, 5*time.Minute); err != nil {
		return nil, fmt.Errorf("wait for simulator pods ready: %w", err)
	}

	// ── Warmup ───────────────────────────────────────────────────────────────
	fmt.Printf("   Warmup (%ds)...\n", scenario.WarmupSeconds)
	if scenario.WarmupSeconds > 0 {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled during warmup: %w", ctx.Err())
		case <-time.After(time.Duration(scenario.WarmupSeconds) * time.Second):
		}
	}

	// ── Load generation ──────────────────────────────────────────────────────
	fmt.Printf("   Generating load (%d RPS for %ds)...\n", scenario.TargetRPS, scenario.DurationSeconds)
	serviceName := serviceNameForScenario(scenario)
	chartPath := filepath.Join(benchmarkingRoot, "helm", "inference-perf")
	// Baseline control scenarios may bypass the gateway and hit simulator directly.
	// All other scenarios route through the gateway Service on port 8080.
	var targetURL string
	if scenario.DirectToSimulator {
		targetURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:8000", serviceName, namespace)
		fmt.Printf("   Route mode: direct-to-simulator\n")
	} else {
		gatewaySvcName := gatewayServiceName(dataPlane)
		targetURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gatewaySvcName, namespace)
		fmt.Printf("   Route mode: via-gateway\n")
	}
	fmt.Printf("   Chart path: %s\n", chartPath)
	fmt.Printf("   Target Base URL: %s\n", targetURL)
	// CI image pulls can be slow; include larger startup buffer so benchmark jobs
	// do not time out before workload execution begins.
	jobTimeout := time.Duration(scenario.DurationSeconds+300) * time.Second
	if jobTimeout < 8*time.Minute {
		jobTimeout = 8 * time.Minute
	}
	activeDeadlineSeconds := int(math.Ceil(jobTimeout.Seconds()))
	helmParams := map[string]interface{}{
		"scenarioUrl":           targetURL,
		"targetRps":             scenario.TargetRPS,
		"durationSeconds":       scenario.DurationSeconds,
		"concurrentUsers":       scenario.ConcurrentUsers,
		"warmupSeconds":         scenario.WarmupSeconds,
		"activeDeadlineSeconds": activeDeadlineSeconds,
	}
	if scenario.DataType != "" {
		helmParams["dataType"] = scenario.DataType
	}

	if err := k8sClient.HelmInstall(ctx, "inference-perf", chartPath, namespace, helmParams); err != nil {
		return nil, fmt.Errorf("helm install inference-perf: %w", err)
	}

	fmt.Printf("   Waiting for inference-perf job completion (timeout=%s)...\n", jobTimeout)
	if err := k8sClient.WaitForJobComplete(ctx, namespace, "inference-perf", jobTimeout); err != nil {
		logs, logErr := k8sClient.GetJobLogs(ctx, namespace, "inference-perf")
		if logErr == nil && strings.TrimSpace(logs) != "" {
			return nil, fmt.Errorf("wait for inference-perf job completion: %w; job logs: %s", err, strings.TrimSpace(logs))
		}
		return nil, fmt.Errorf("wait for inference-perf job completion: %w", err)
	}

	// ── Metrics scrape ───────────────────────────────────────────────────────
	// Derive the service name from the first backend tier label so the Prometheus
	// queries are scoped to the actual services deployed for this scenario,
	// not a hardcoded string that may not exist.
	duration := time.Duration(scenario.DurationSeconds) * time.Second

	result, err := promClient.ScrapeGatewayMetrics(ctx, namespace, serviceName, dataPlane, duration)
	if err != nil {
		return nil, fmt.Errorf("metrics scrape failed: %w", err)
	}

	result.ScenarioName = scenario.Name
	result.DataPlane = dataPlane

	// ── Streaming-specific: direct SSE measurement ───────────────────────────
	// Pass only the base URL — createStreamingRequest appends the path internally.
	if scenario.Name == "streaming" {
		fmt.Printf("   Measuring TTFT/ITL via SSE client...\n")
		podName, podErr := k8sClient.GetPodName(ctx, namespace, fmt.Sprintf("app=%s", serviceName))
		if podErr != nil {
			fmt.Printf("   Warning: could not find pod for streaming port-forward: %v\n", podErr)
		} else {
			stopPortForward, pfErr := k8sClient.PortForward(ctx, namespace, podName, 18080, 8080)
			if pfErr != nil {
				fmt.Printf("   Warning: streaming port-forward failed: %v\n", pfErr)
			} else {
				defer stopPortForward()
			}
		}

		streamResult, streamErr := scenarios.MeasureStreamingMetrics(
			ctx,
			"http://127.0.0.1:18080", // base URL only, path appended in streaming.go
			"meta-llama/Llama-3-8b",
		)
		if streamErr != nil {
			fmt.Printf("   Warning: streaming measurement failed: %v\n", streamErr)
		} else {
			result.TTFTMeanMs = streamResult.TTFTMs
			result.ITLMeanUs = streamResult.ITLMeanUs
		}
	}

	// ── EPP fairness validation ───────────────────────────────────────────────
	if scenario.Name == "epp-fairness" {
		dist, distErr := promClient.ScrapeTierDistribution(ctx, namespace, duration)
		if distErr != nil {
			fmt.Printf("   Warning: tier distribution scrape failed: %v\n", distErr)
		} else if fairErr := scenarios.CheckFairness(dist, scenarios.ExpectedFairnessDistribution, scenarios.FairnessTolerancePct); fairErr != nil {
			fmt.Printf("   ⚠️  EPP fairness violation: %v\n", fairErr)
		} else {
			fmt.Printf("   ✅ EPP fairness check passed\n")
		}
	}

	fmt.Printf("   ✅ %s done (P99=%.2fms)\n", scenario.Name, result.P99LatencyMs)
	return result, nil
}

// gatewayServiceName returns the Kubernetes Service name that kgateway creates for
// the Gateway object. The name must match the `metadata.name` of the applied Gateway
// manifest so that the in-cluster URL resolves correctly.
func gatewayServiceName(dataPlane string) string {
	if dataPlane == "envoy" {
		return "envoy-gateway" // matches manifests/envoy/gateway.yaml
	}
	return "inference-gateway" // matches manifests/gateway.yaml
}

// serviceNameForScenario derives a concrete simulator Service name for the first tier.
// Simulator manifests expose tiered Services like llm-d-sim-large/medium/small, so a
// fallback to llm-d-sim would not resolve in-cluster and causes load jobs to stall.
func serviceNameForScenario(scenario *scenarios.Scenario) string {
	if len(scenario.BackendTiers) > 0 {
		name := strings.TrimSpace(scenario.BackendTiers[0].Name)
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "tier-") {
			suffix := strings.TrimPrefix(name, "tier-")
			if suffix != "" {
				return "llm-d-sim-" + suffix
			}
		}
		if n, err := strconv.Atoi(name); err == nil && n >= 0 {
			return fmt.Sprintf("llm-d-sim-%d", n)
		}

		if app, ok := scenario.BackendTiers[0].Labels["app"]; ok && app != "" {
			// app=llm-d-sim is a pod label, not a concrete Service name.
			if app == "llm-d-sim" {
				if tier, ok := scenario.BackendTiers[0].Labels["tier"]; ok && strings.TrimSpace(tier) != "" {
					return "llm-d-sim-" + strings.ToLower(strings.TrimSpace(tier))
				}
				return "llm-d-sim-large"
			}

			if strings.HasPrefix(app, "llm-d-sim-") {
				return app
			}
		}
	}
	return "llm-d-sim-large"
}

// syntheticResults returns plausible but clearly fake metrics for stub mode.
// Values are based on the realistic baseline.json numbers so reports look meaningful.
func syntheticResults(scenario *scenarios.Scenario, dataPlane string) *scenarios.Results {
	fmt.Printf("   [STUB] returning synthetic metrics for %s\n", scenario.Name)
	return &scenarios.Results{
		ScenarioName:         scenario.Name,
		DataPlane:            dataPlane,
		Timestamp:            time.Now(),
		P50LatencyMs:         3.2,
		P95LatencyMs:         8.1,
		P99LatencyMs:         12.4,
		MeanLatencyMs:        4.5,
		ThroughputRPS:        float64(scenario.TargetRPS) * 0.92,
		ErrorRate:            0.002,
		GatewayCPUMillicores: 120,
		GatewayMemoryMB:      256,
		EPPDecisionLatencyMs: 0.8,
	}
}

// computeGatewayOverhead calculates GatewayOverheadMs for every non-baseline scenario.
// This is the "gateway tax": how much latency the inference routing extensions add.
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

func saveBaselineJSON(path string, result *scenarios.Results) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create baseline directory: %w", err)
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline result: %w", err)
	}

	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("write baseline file: %w", err)
	}

	return nil
}

// applyInferenceManifestWithFallback applies manifestPath and falls back to a
// sibling *-v1.yaml manifest when the cluster does not serve v1alpha2 CRDs.
func applyInferenceManifestWithFallback(ctx context.Context, c *k8s.K8sClient, manifestsRoot, manifestPath string) (string, error) {
	err := c.ApplyManifestFile(ctx, manifestPath)
	if err == nil {
		return manifestPath, nil
	}

	if !strings.Contains(err.Error(), "no matches for kind") || !strings.Contains(err.Error(), "v1alpha2") {
		return "", err
	}

	base := filepath.Base(manifestPath)
	var fallback string
	switch base {
	case "inference-pool.yaml":
		fallback = filepath.Join(manifestsRoot, "inference-pool-v1.yaml")
	case "inference-model.yaml":
		fallback = filepath.Join(manifestsRoot, "inference-model-v1.yaml")
	default:
		return "", err
	}

	if _, statErr := os.Stat(fallback); statErr != nil {
		return "", fmt.Errorf("%w; fallback manifest missing: %s", err, fallback)
	}

	if fbErr := c.ApplyManifestFile(ctx, fallback); fbErr != nil {
		return "", fmt.Errorf("%w; fallback apply failed: %w", err, fbErr)
	}

	fmt.Printf("   Applied fallback inference manifest: %s\n", filepath.Base(fallback))
	return fallback, nil
}
