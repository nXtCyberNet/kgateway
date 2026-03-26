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
	PrometheusURL string
	BaselinePath  string
	Threshold     float64
	OutputDir     string
	DataPlane     string
	ScenariosDir  string
	GatewaySvc    string
}

func main() {
	cfg := parseFlags()
	ctx := context.Background()

	baseline := loadBaseline(cfg.BaselinePath)
	scenarioFiles := discoverScenarios(cfg.ScenarioOpt, cfg.ScenariosDir)

	results, regressions := runAllScenarios(ctx, cfg, scenarioFiles, baseline)
	fmt.Println(metrics.GenerateSummaryTable(results, regressions))

	reportPath := filepath.Join(cfg.OutputDir, "report.html")
	if err := report.GenerateHTMLReport(results, regressions, reportPath); err != nil {
		log.Printf("failed to generate HTML report: %v", err)
	}

	for _, reg := range regressions {
		if reg != nil && reg.Exceeded {
			os.Exit(1)
		}
	}
}

func parseFlags() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.ScenarioOpt, "scenario", "all", "Scenario yaml name to run, or all")
	flag.StringVar(&cfg.Namespace, "namespace", "default", "Kubernetes namespace")
	flag.StringVar(&cfg.PrometheusURL, "prometheus-url", "http://localhost:9090", "Prometheus URL")
	flag.StringVar(&cfg.BaselinePath, "baseline", "", "Path to baseline json")
	flag.Float64Var(&cfg.Threshold, "threshold", 20.0, "Latency regression threshold percentage")
	flag.StringVar(&cfg.OutputDir, "output", "results", "Results output directory")
	flag.StringVar(&cfg.DataPlane, "data-plane", "envoy", "Data plane type (envoy or agentgateway)")
	flag.StringVar(&cfg.ScenariosDir, "scenarios-dir", "scenarios", "Directory containing scenario yaml files")
	flag.StringVar(&cfg.GatewaySvc, "gateway-service", "gateway", "Gateway service/pod identifier used in Prometheus queries")
	flag.Parse()
	return cfg
}

func loadBaseline(path string) *scenarios.Results {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	baseline, err := metrics.LoadBaseline(path)
	if err != nil {
		log.Fatalf("load baseline: %v", err)
	}
	return baseline
}

func discoverScenarios(scenarioOpt, scenariosDir string) []string {
	if scenarioOpt != "all" {
		return []string{filepath.Join(scenariosDir, scenarioOpt+".yaml")}
	}

	entries, err := os.ReadDir(scenariosDir)
	if err != nil {
		log.Fatalf("read scenarios dir %s: %v", scenariosDir, err)
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml") {
			out = append(out, filepath.Join(scenariosDir, e.Name()))
		}
	}
	return out
}

func runAllScenarios(ctx context.Context, cfg *Config, scenarioFiles []string, baseline *scenarios.Results) ([]*scenarios.Results, []*scenarios.RegressionResult) {
	client, err := k8s.NewK8sClient()
	if err != nil {
		log.Fatalf("init kubernetes client: %v", err)
	}

	prom, err := metrics.NewPrometheusClient(cfg.PrometheusURL)
	if err != nil {
		log.Fatalf("init prometheus client: %v", err)
	}

	results := make([]*scenarios.Results, 0, len(scenarioFiles))
	regs := make([]*scenarios.RegressionResult, 0, len(scenarioFiles))

	for _, file := range scenarioFiles {
		s, err := scenarios.LoadFromYAML(file)
		if err != nil {
			log.Fatalf("load scenario %s: %v", file, err)
		}

		res := executeScenario(ctx, cfg, client, prom, s)
		savedPath, err := metrics.SaveResults(res, cfg.OutputDir)
		if err != nil {
			log.Fatalf("save results: %v", err)
		}
		log.Printf("saved result: %s", savedPath)

		results = append(results, res)
		if baseline != nil {
			regs = append(regs, metrics.CheckRegression(res, baseline, cfg.Threshold))
		}
	}

	return results, regs
}

func executeScenario(ctx context.Context, cfg *Config, client *k8s.K8sClient, prom *metrics.PrometheusClient, scen *scenarios.Scenario) *scenarios.Results {
	manifestDir := scen.Gateway.ManifestsDir
	if manifestDir == "" {
		manifestDir = filepath.Join("manifests", cfg.DataPlane)
	}

	if err := client.ApplyManifestDir(ctx, manifestDir); err != nil {
		log.Printf("manifest apply warning: %v", err)
	}

	duration, err := time.ParseDuration(scen.LoadConfig.Duration)
	if err != nil {
		duration = time.Minute
	}

	helmValues := map[string]interface{}{
		"targetRPS": scen.LoadConfig.TargetRps,
		"duration":  scen.LoadConfig.Duration,
	}
	if err := client.HelmInstall("perf-run", filepath.Join("helm", "inference-perf"), cfg.Namespace, helmValues); err != nil {
		log.Printf("helm install warning: %v", err)
	}

	_ = client.WaitForJobComplete(ctx, cfg.Namespace, "perf-run", duration+3*time.Minute)

	metricsSnap, _ := prom.ScrapeGatewayMetrics(ctx, cfg.Namespace, cfg.GatewaySvc, cfg.DataPlane, duration)
	if metricsSnap == nil {
		metricsSnap = &metrics.GatewayMetrics{}
	}

	streaming := metricsSnap.Streaming
	if strings.Contains(strings.ToLower(scen.Name), "stream") {
		podName, err := client.GetRunningPodName(ctx, cfg.Namespace, "app=gateway")
		if err == nil {
			stop, err := client.PortForward(cfg.Namespace, podName, 18080, 8080)
			if err == nil {
				ttft, itl, serr := scenarios.MeasureStreamingMetrics(ctx, "http://127.0.0.1:18080/v1/chat/completions", scen.Metadata.ModelName)
				if serr == nil {
					streaming.TTFT = ttft
					streaming.ITL = itl
				}
				stop()
			}
		}
	}

	if err := client.HelmUninstall("perf-run", cfg.Namespace); err != nil {
		log.Printf("helm uninstall warning: %v", err)
	}
	if err := client.DeleteManifestDir(ctx, manifestDir); err != nil {
		log.Printf("manifest delete warning: %v", err)
	}

	return &scenarios.Results{
		ScenarioName:  scen.Name,
		DataPlane:     cfg.DataPlane,
		Timestamp:     time.Now().UTC(),
		Latency:       metricsSnap.Latency,
		Streaming:     streaming,
		ErrorRate:     metricsSnap.ErrorRate,
		LoadShedCount: metricsSnap.LoadShedCount,
	}
}
