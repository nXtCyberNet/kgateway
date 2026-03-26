package report

import (
	"fmt"
	"os"
	"strings"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// GenerateHTMLReport writes a self-contained inline-CSS HTML report containing benchmark results
func GenerateHTMLReport(results []*scenarios.Results, regressions []*scenarios.RegressionResult, outputPath string) error {
	var sb strings.Builder

	writeHTMLHeader(&sb)
	writeTableContent(&sb, results)
	writeRegressionContent(&sb, regressions)
	writeHTMLFooter(&sb)

	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write html report to %s: %w", outputPath, err)
	}

	return nil
}

// writeHTMLHeader sets up the page layout and inline styles
func writeHTMLHeader(sb *strings.Builder) {
	sb.WriteString(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<title>kgateway Benchmark Report</title>
	<style>
		body { font-family: -apple-system, sans-serif; margin: 40px; color: #333; }
		h1, h2 { color: #111; }
		table { border-collapse: collapse; width: 100%; margin-top: 20px; }
		th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
		th { background-color: #f5f5f5; }
		.good { background-color: #e6ffed; }
		.warn { background-color: #fffbdd; }
		.bad { background-color: #ffeef0; }
		.note { font-size: 0.9em; color: #666; margin-top: 40px; }
	</style>
</head>
<body>
	<h1>kgateway Inference Routing - Performance Benchmark</h1>
	<p>Generated benchmark results for Envoy vs Agentgateway dataplanes.</p>

	<h2>Results Summary</h2>
	<table>
		<tr>
			<th>Scenario</th>
			<th>P99 Latency (ms)</th>
			<th>Throughput (RPS)</th>
			<th>Overhead (ms)</th>
			<th>CPU (m)</th>
			<th>Memory (MB)</th>
		</tr>
`)
}

// writeTableContent formats the run results into the main data table
func writeTableContent(sb *strings.Builder, results []*scenarios.Results) {
	for _, r := range results {
		overheadClass := "bad"
		if r.GatewayOverheadMs < 5.0 {
			overheadClass = "good"
		} else if r.GatewayOverheadMs <= 15.0 {
			overheadClass = "warn"
		}

		sb.WriteString(fmt.Sprintf(`
		<tr>
			<td>%s</td>
			<td>%.2f</td>
			<td>%.2f</td>
			<td class="%s">%.2f</td>
			<td>%.0f</td>
			<td>%.0f</td>
		</tr>`, r.ScenarioName, r.P99LatencyMs, r.ThroughputRPS, overheadClass, r.GatewayOverheadMs, r.GatewayCPUMillicores, r.GatewayMemoryMB))
	}
	sb.WriteString(`
	</table>
`)
}

// writeRegressionContent displays dynamic threshold breach warnings if encountered during validation
func writeRegressionContent(sb *strings.Builder, regressions []*scenarios.RegressionResult) {
	if len(regressions) == 0 {
		sb.WriteString(`<p>No regressions detected against baseline.</p>`)
		return
	}

	sb.WriteString(`<h2>Regressions Detected</h2><ul>`)
	for _, reg := range regressions {
		if reg.Exceeded {
			sb.WriteString(fmt.Sprintf(`<li><strong>%s</strong>: P99 regression from %.2fms to %.2fms (%.2f%% increase, threshold %.2f%%)</li>`,
				reg.ScenarioName, reg.BaselineP99, reg.CurrentP99, reg.DeltaPct, reg.Threshold))
		}
	}
	sb.WriteString(`</ul>`)
}

// writeHTMLFooter appends methodology context and closes tags
func writeHTMLFooter(sb *strings.Builder) {
	sb.WriteString(`
	<div class="note">
		<h3>Methodology</h3>
		<p>Benchmark measures full round-trip time vs theoretical bare-metal simulator time to compute the "gateway tax". 
		Green: &lt;5ms. Yellow: 5-15ms. Red: &gt;15ms latency overhead.</p>
	</div>
</body>
</html>`)
}
