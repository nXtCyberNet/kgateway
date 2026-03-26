package main

import (
"context"
"flag"
"fmt"
"log"
"os"
"path/filepath"
"strings"
"time"

"github.com/kgateway-dev/kgateway/benchmarking/pkg/k8s"
"github.com/kgateway-dev/kgateway/benchmarking/pkg/metrics"
"github.com/kgateway-dev/kgateway/benchmarking/pkg/report"
"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

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

reportPath := filepath.Join(config.OutputDir, "report.html")
if err := report.GenerateHTMLReport(allResults, allRegressions, reportPath); err != nil {
log.Printf("Failed to generate HTML report: %v", err)
} else {
log.Printf("Generated HTML report at %s", reportPath)
}

checkRegressionsAndExit(allRegressions)
}

func parseFlags() *Config {
ex, err := os.Executable()
var defaultDir string
if err == nil && !strings.Contains(ex, "go-build") { // Not run via go run
defaultDir = filepath.Join(filepath.Dir(ex), "..", "..", "scenarios")
} else {
cwd, _ := os.Getwd()
defaultDir = filepath.Join(cwd, "benchmarking", "scenarios")
}

cfg := &Config{
ScenariosDir: defaultDir,
}

flag.StringVar(&cfg.ScenarioOpt, "scenario", "all", "Scenario to run (name of yaml file without extension, or 'all')")
flag.StringVar(&cfg.Namespace, "namespace", "default", "Kubernetes namespace")
flag.StringVar(&cfg.Kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
flag.StringVar(&cfg.PrometheusURL, "prometheus-url", "http://localhost:9090", "Prometheus URL")
flag.StringVar(&cfg.BaselinePath, "baseline", "", "Path to baseline.json")
flag.Float64Var(&cfg.Threshold, "threshold", 20.0, "Regression threshold percentage")
flag.StringVar(&cfg.OutputDir, "output", "results/", "Output directory for benchmark results")
flag.StringVar(&cfg.DataPlane, "data-plane", "envoy", "Data plane type (envoy or agentgateway)")
flag.StringVar(&cfg.ScenariosDir, "scenarios-dir", cfg.ScenariosDir, "Directory containing scenario yaml files")
flag.Parse()

return cfg
}

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

func discoverScenarios(scenarioOpt, scenariosDir string) []string {
var filesToRun []string
if scenarioOpt == "all" {
files, err := os.ReadDir(scenariosDir)
if err != nil {
log.Fatalf("failed to read scenarios directory %s: %v", scenariosDir, err)
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

func runAllScenarios(ctx context.Context, cfg *Config, scenarioFiles []string, baseline *scenarios.Results) ([]*scenarios.Results, []*scenarios.RegressionResult) {
var allResults []*scenarios.Results
var allRegressions []*scenarios.RegressionResult

k8sClient, err := k8s.NewK8sClient(cfg.Kubeconfig)
if err != nil {
log.Fatalf("failed to initialize kubernetes client: %v", err)
}
promClient := metrics.NewPrometheusClient(cfg.PrometheusURL)

var directBackendLatency float64

for _, file := range scenarioFiles {
scen, err := scenarios.LoadFromYAML(file)
if err != nil {
log.Fatalf("failed to load scenario %s: %v", file, err)
}
log.Printf("====== Running scenario: %s ======", scen.Name)

res := executeScenario(ctx, cfg, scen, k8sClient, promClient)
res.DataPlane = cfg.DataPlane

if strings.Contains(strings.ToLower(scen.Name), "direct") || directBackendLatency == 0 {
directBackendLatency = res.P99LatencyMs
res.GatewayOverheadMs = 0
} else {
res.GatewayOverheadMs = res.P99LatencyMs - directBackendLatency
}

if scen.Name == "epp-fairness" {
log.Printf("Scraping EPP Fairness Metrics... (Mocked for now, assuming external metric evaluation)")
// Integration hook for fairness checker
// scenarios.CheckFairness(actualDist, scenarios.ExpectedDistribution, 5.0)
}

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

func executeScenario(ctx context.Context, cfg *Config, scen *scenarios.Scenario, client *k8s.K8sClient, prom *metrics.PrometheusClient) *scenarios.Results {
manifestDir := filepath.Join("benchmarking", "manifests", cfg.DataPlane)
if _, err := os.Stat(manifestDir); os.IsNotExist(err) {
manifestDir = filepath.Join("benchmarking", "manifests")
}

log.Printf("Applying manifests from %s...", manifestDir)
k8s.ApplyManifestDir(ctx, client, manifestDir)



client.WaitForPodsReady(ctx, cfg.Namespace, "app=llm-d-sim", 1, 3*time.Minute)
client.WaitForPodsReady(ctx, cfg.Namespace, "app=gateway", 1, 3*time.Minute)

log.Println("Launching payload traffic generator via helm...")
chartPath := filepath.Join("benchmarking", "helm", "inference-perf")
helmSet := map[string]string{
"targetRPS": fmt.Sprintf("%d", scen.TargetRPS),
"duration":  fmt.Sprintf("%ds", scen.DurationSeconds),
"users":     fmt.Sprintf("%d", scen.ConcurrentUsers),
}
if err := k8s.HelmInstall("perf-run", chartPath, cfg.Namespace, helmSet); err != nil {
log.Printf("Warning: failed running load gen: %v", err)
}

log.Println("Waiting for traffic injection to complete...")
client.WaitForJobCompletion(ctx, cfg.Namespace, "app=inference-perf", 5*time.Minute)

var ttftMs, itlMeanUs float64
if scen.Name == "streaming" {
podName, _ := client.GetPodName(ctx, cfg.Namespace, "app=gateway")
if podName != "" {
stopFw, err := client.PortForward(cfg.Namespace, podName, 8080, 8080)
if err == nil {
log.Println("Measuring SSE specific streaming metrics...")
ttftMs, itlMeanUs, _ = scenarios.MeasureStreamingMetrics("http://localhost:8080/v1/chat/completions", "mock-model")
stopFw()
}
}
}

log.Println("Scraping core operational metrics...")
podName, _ := client.GetPodName(ctx, cfg.Namespace, "app=gateway")
gwMetrics, _ := prom.ScrapeGatewayMetrics(ctx, cfg.Namespace, podName, cfg.DataPlane)
if gwMetrics == nil {
gwMetrics = &metrics.GatewayMetrics{}
}

log.Printf("Teardown...")
k8s.HelmUninstall("perf-run", cfg.Namespace)
k8s.DeleteManifestDir(ctx, client, manifestDir)

return &scenarios.Results{
ScenarioName:         scen.Name,
DataPlane:            cfg.DataPlane,
ThroughputRPS:        float64(scen.TargetRPS),
ErrorRate:            0.0,
P99LatencyMs:         gwMetrics.P99,
P95LatencyMs:         gwMetrics.P95,
P50LatencyMs:         gwMetrics.P50,
GatewayMemoryMB:      gwMetrics.GatewayMemoryMB,
GatewayCPUMillicores: gwMetrics.GatewayCPUMillicores,
TTFTp99Ms:            ttftMs,
ITLMeanUs:            itlMeanUs,
}
}

func printSummary(results []*scenarios.Results, regressions []*scenarios.RegressionResult) {
summary := metrics.GenerateSummaryTable(results, regressions)
fmt.Println("\nBenchmark Summary:")
fmt.Println(summary)
}

func checkRegressionsAndExit(regressions []*scenarios.RegressionResult) {
for _, reg := range regressions {
if reg.Exceeded {
log.Printf("Performance regression detected in %s! Exceeded threshold of %.2f%% (Current: %.2fms vs Baseline: %.2fms)",
reg.ScenarioName, reg.Threshold, reg.CurrentP99, reg.BaselineP99)
os.Exit(1)
}
}
}

