package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/metrics"
	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// Config holds the configuration options set via command-line flags
type Config struct {
	ScenarioOpt   string
	Namespace     string
	Kubeconfig    string
	PrometheusURL string
	BaselinePath  string
	Threshold     float64
	OutputDir     string
	DataPlane     string
	ScenariosDir  string
}

func main() {
	config := parseFlags()

	log.Printf("Starting kgateway inference benchmarking runner")
	log.Printf("Data plane: %s, Namespace: %s, Threshold: %.1f%%", config.DataPlane, config.Namespace, config.Threshold)

	baseline := loadBaseline(config.BaselinePath)
	scenarioFiles := discoverScenarios(config.ScenarioOpt, config.ScenariosDir)

	ctx := context.Background()
	allResults, allRegressions := runAllScenarios(ctx, config, scenarioFiles, baseline)

	printSummary(allResults, allRegressions)
	checkRegressionsAndExit(allRegressions)
}

// parseFlags parses command line arguments and returns a Config struct
func parseFlags() *Config {
	cfg := &Config{
		ScenariosDir: "scenarios", // Default directory for finding runtime scenarios
	}

	flag.StringVar(&cfg.ScenarioOpt, "scenario", "all", "Scenario to run (name of yaml file without extension, or 'all')")
	flag.StringVar(&cfg.Namespace, "namespace", "default", "Kubernetes namespace")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	flag.StringVar(&cfg.PrometheusURL, "prometheus-url", "http://localhost:9090", "Prometheus URL")
	flag.StringVar(&cfg.BaselinePath, "baseline", "", "Path to baseline.json")
	flag.Float64Var(&cfg.Threshold, "threshold", 20.0, "Regression threshold percentage")
	flag.StringVar(&cfg.OutputDir, "output", "results/", "Output directory for benchmark results")
	flag.StringVar(&cfg.DataPlane, "data-plane", "envoy", "Data plane type (envoy or agentgateway)")
	flag.Parse()

	return cfg
}

// loadBaseline loads the baseline metrics if a path is provided
func loadBaseline(path string) *scenarios.Results {
	if path == "" {
		return nil
	}
	
	baseline, err := metrics.LoadBaseline(path)
	if err != nil {
		log.Fatalf("failed to load baseline: %v", err)
	}
	log.Printf("Loaded baseline from %s: P99=%.2fms", path, baseline.P99LatencyMs)
	return baseline
}

// discoverScenarios finds the valid scenario config paths based on the provided option
func discoverScenarios(scenarioOpt, scenariosDir string) []string {
	var filesToRun []string

	if scenarioOpt == "all" {
		files, err := os.ReadDir(scenariosDir)
		if err != nil {
			log.Fatalf("failed to read scenarios directory: %v", err)
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".yaml" {
				filesToRun = append(filesToRun, filepath.Join(scenariosDir, f.Name()))
			}
		}
	} else {
		filesToRun = append(filesToRun, filepath.Join(scenariosDir, scenarioOpt+".yaml"))
	}

	return filesToRun
}

// runAllScenarios iterates and runs scenarios, saving output records along the way
func runAllScenarios(ctx context.Context, cfg *Config, scenarioFiles []string, baseline *scenarios.Results) ([]*scenarios.Results, []*scenarios.RegressionResult) {
	var allResults []*scenarios.Results
	var allRegressions []*scenarios.RegressionResult

	for _, file := range scenarioFiles {
		scen, err := scenarios.LoadFromYAML(file)
		if err != nil {
			log.Fatalf("failed to load scenario %s: %v", file, err)
		}
		log.Printf("Running scenario: %s", scen.Name)

		// This calls into the orchestrator runner mapping
		res := executeScenarioMock(ctx, scen)
		res.DataPlane = cfg.DataPlane

		savedPath, err := metrics.SaveResults(res, cfg.OutputDir)
		if err != nil {
			log.Fatalf("failed to save results: %v", err)
		}
		log.Printf("Saved results to %s", savedPath)

		allResults = append(allResults, res)

		if baseline != nil {
			reg := metrics.CheckRegression(res, baseline, cfg.Threshold)
			if reg != nil {
				allRegressions = append(allRegressions, reg)
			}
		}
	}

	return allResults, allRegressions
}

// printSummary outputs the summary report table to the standard console
func printSummary(results []*scenarios.Results, regressions []*scenarios.RegressionResult) {
	summary := metrics.GenerateSummaryTable(results, regressions)
	fmt.Println("\nBenchmark Summary:")
	fmt.Println(summary)
}

// checkRegressionsAndExit checks if any run breached the regression threshold and fails with exit code 1
func checkRegressionsAndExit(regressions []*scenarios.RegressionResult) {
	for _, reg := range regressions {
		if reg.Exceeded {
			log.Printf("Performance regression detected in %s! Exceeded threshold of %.2f%% (Current: %.2fms vs Baseline: %.2fms)", 
				reg.ScenarioName, reg.Threshold, reg.CurrentP99, reg.BaselineP99)
			os.Exit(1)
		}
	}
}

// executeScenarioMock represents the core engine test execution (currently mocked data)
func executeScenarioMock(ctx context.Context, scen *scenarios.Scenario) *scenarios.Results {
	return &scenarios.Results{
		ScenarioName:         scen.Name,
		P99LatencyMs:         12.5,
		ThroughputRPS:        float64(scen.TargetRPS),
		ErrorRate:            0,
		GatewayOverheadMs:    3.2,
		GatewayCPUMillicores: 15.0,
		GatewayMemoryMB:      50.0,
	}
}
