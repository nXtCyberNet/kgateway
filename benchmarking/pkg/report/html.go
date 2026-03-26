package report

import (
	"fmt"
	"os"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// GenerateHTMLReport generates a single-file performance report.
func GenerateHTMLReport(results []*scenarios.Results, regressions []*scenarios.RegressionResult, outputPath string) error {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>kgateway Benchmarking Report</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; line-height: 1.6; color: #333; max-width: 1000px; margin: 0 auto; padding: 20px; }
        h1, h2 { color: #2c3e50; }
        table { width: 100%; border-collapse: collapse; margin-bottom: 20px; }
        th, td { padding: 12px; text-align: left; border-bottom: 1px solid #eee; }
        th { background-color: #f8f9fa; }
        .green { background-color: #d4edda; color: #155724; }
        .yellow { background-color: #fff3cd; color: #856404; }
        .red { background-color: #f8d7da; color: #721c24; font-weight: bold; }
        .methodology { background-color: #f8f9fa; padding: 15px; border-left: 5px solid #007bff; font-size: 0.9em; }
    </style>
</head>
<body>
    <h1>kgateway Inference Routing Performance</h1>
    <p>Generated at: ` + time.Now().Format(time.RFC1123) + `</p>

    <table>
        <thead>
            <tr>
                <th>Scenario</th>
                <th>P99 (ms)</th>
                <th>Overhead (ms)</th>
                <th>Throughput (RPS)</th>
                <th>Gateway CPU</th>
                <th>EPP Latency</th>
            </tr>
        </thead>
        <tbody>`

	for _, r := range results {
		overheadClass := "green"
		if r.GatewayOverheadMs > 15 {
			overheadClass = "red"
		} else if r.GatewayOverheadMs > 5 {
			overheadClass = "yellow"
		}

		html += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>%.2f</td>
                <td class="%s">%.2f</td>
                <td>%.1f</td>
                <td>%.0fm</td>
                <td>%.2fms</td>
            </tr>`, r.ScenarioName, r.P99LatencyMs, overheadClass, r.GatewayOverheadMs, r.ThroughputRPS, r.GatewayCPUMillicores, r.EPPDecisionLatencyMs)
	}

	html += `
        </tbody>
    </table>

    <div class="methodology">
        <strong>Methodology Note:</strong> Latency overhead is calculated as the difference between the current scenario's P99 and the S1-TCP-Baseline P99. Gateway CPU reflects millicores consumed per 100 RPS.
    </div>
</body>
</html>`

	return os.WriteFile(outputPath, []byte(html), 0644)
}
