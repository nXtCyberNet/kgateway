package report

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)


type ReportData struct {
	GeneratedAt time.Time
	Results     []*scenarios.Results
	Regressions map[string]*scenarios.RegressionResult
}


func GenerateHTMLReport(results []*scenarios.Results, regressions []*scenarios.RegressionResult, outputPath string) error {
	if len(results) == 0 {
		return fmt.Errorf("no results to generate report")
	}

	// Convert regression slice to map for fast lookup in template
	regMap := make(map[string]*scenarios.RegressionResult, len(regressions))
	for _, r := range regressions {
		if r != nil {
			regMap[r.ScenarioName] = r
		}
	}

	data := ReportData{
		GeneratedAt: time.Now(),
		Results:     results,
		Regressions: regMap,
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %w", err)
	}

	tmpl, err := template.New("benchmark-report").Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse HTML template: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create report file %s: %w", outputPath, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("failed to execute HTML template: %w", err)
	}

	fmt.Printf("✅ HTML report generated: %s\n", outputPath)
	return nil
}

// htmlTemplate is a complete, self-contained HTML + Tailwind-like CSS report.
const htmlTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>kgateway Inference Routing Benchmark Report</title>
    <style>
        :root {
            --bg: #ffffff;
            --text: #1a202c;
            --header: #f7fafc;
            --border: #e2e8f0;
            --success: #38a169;
            --warning: #dd6b20;
            --danger: #e53e3e;
            --accent: #3182ce;
        }
        @media (prefers-color-scheme: dark) {
            :root {
                --bg: #1a202c;
                --text: #f7fafc;
                --header: #2d3748;
                --border: #4a5568;
            }
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: var(--bg);
            color: var(--text);
            line-height: 1.5;
            padding: 2rem;
            margin: 0;
        }
        .container { max-width: 1280px; margin: 0 auto; }
        h1 { border-bottom: 3px solid var(--accent); padding-bottom: 0.5rem; margin-bottom: 1.5rem; }
        .meta { color: #718096; font-size: 0.95rem; margin-bottom: 2rem; }
        table {
            width: 100%;
            border-collapse: collapse;
            margin-bottom: 3rem;
            box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1);
        }
        th, td {
            padding: 1rem;
            text-align: left;
            border: 1px solid var(--border);
        }
        th {
            background: var(--header);
            font-weight: 600;
            text-transform: uppercase;
            font-size: 0.8rem;
            letter-spacing: 0.05em;
        }
        tr:nth-child(even) { background: rgba(0,0,0,0.02); }
        .badge {
            display: inline-block;
            padding: 0.25rem 0.75rem;
            border-radius: 9999px;
            font-size: 0.75rem;
            font-weight: 600;
        }
        .badge-envoy { background: #ebf8ff; color: #2b6cb0; }
        .badge-agent { background: #faf5ff; color: #6b46c1; }
        .status-pass { color: var(--success); font-weight: bold; }
        .status-fail { color: var(--danger); font-weight: bold; }
        .metric-unit { font-size: 0.8rem; color: #718096; margin-left: 4px; }
        .card {
            background: var(--header);
            border: 1px solid var(--border);
            border-radius: 0.75rem;
            padding: 1.5rem;
            margin-top: 2rem;
        }
        .overhead-positive { color: var(--danger); }
    </style>
</head>
<body>
    <div class="container">
        <h1>kgateway Inference Routing Benchmark Report</h1>
        <p class="meta">Generated at: {{.GeneratedAt.Format "Jan 02, 2006 15:04:05 MST"}} • Framework v0.1</p>

        <table>
            <thead>
                <tr>
                    <th>Scenario</th>
                    <th>Data Plane</th>
                    <th>P99 Latency</th>
                    <th>Gateway Tax</th>
                    <th>TTFT (mean)</th>
                    <th>ITL (mean)</th>
                    <th>Throughput</th>
                    <th>Error Rate</th>
                    <th>Status</th>
                </tr>
            </thead>
            <tbody>
                {{range .Results}}
                {{$reg := index $.Regressions .ScenarioName}}
                <tr>
                    <td><strong>{{.ScenarioName}}</strong></td>
                    <td>
                        <span class="badge {{if eq .DataPlane "envoy"}}badge-envoy{{else}}badge-agent{{end}}">
                            {{.DataPlane}}
                        </span>
                    </td>
                    <td>{{printf "%.2f" .P99LatencyMs}}<span class="metric-unit">ms</span></td>
                    <td>
                        {{if gt .GatewayOverheadMs 0.0}}
                            <span class="overhead-positive">+{{printf "%.2f" .GatewayOverheadMs}} ms</span>
                        {{else}}-{{end}}
                    </td>
                    <td>{{if gt .TTFTMeanMs 0.0}}{{printf "%.2f" .TTFTMeanMs}}<span class="metric-unit">ms</span>{{else}}-{{end}}</td>
                    <td>{{if gt .ITLMeanUs 0.0}}{{printf "%.0f" .ITLMeanUs}}<span class="metric-unit">µs</span>{{else}}-{{end}}</td>
                    <td>{{printf "%.1f" .ThroughputRPS}}<span class="metric-unit">RPS</span></td>
                    <td>{{printf "%.4f" .ErrorRate}}</td>
                    <td>
                        {{if $reg}}
                            {{if $reg.Exceeded}}
                                <span class="status-fail">🔴 REGRESSION</span>
                            {{else}}
                                <span class="status-pass">🟢 PASS</span>
                            {{end}}
                        {{else}}
                            <span class="status-pass">🟢 NEW</span>
                        {{end}}
                    </td>
                </tr>
                {{end}}
            </tbody>
        </table>

        <div class="card">
            <h3>Infrastructure Utilization Summary</h3>
            <ul style="margin:0; padding-left:1.5rem;">
                {{range .Results}}
                <li><strong>{{.ScenarioName}} ({{.DataPlane}})</strong>: 
                    {{printf "%.0f" .GatewayCPUMillicores}}m CPU &nbsp;•&nbsp; 
                    {{printf "%.1f" .GatewayMemoryMB}} MB RAM
                </li>
                {{end}}
            </ul>
        </div>

        <p style="margin-top:3rem; font-size:0.85rem; color:#718096; text-align:center;">
            Methodology: Baseline (S1) vs Inference Routing (S3) measures the true "gateway tax".<br>
            All metrics are scraped from Prometheus after a warm-up period. Streaming metrics (TTFT/ITL) are measured via direct SSE client.
        </p>
    </div>
</body>
</html>
`
