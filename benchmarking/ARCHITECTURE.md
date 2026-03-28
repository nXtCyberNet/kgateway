# Benchmarking Architecture

This document describes the architecture of the benchmarking framework under `benchmarking/`, its execution lifecycle, and the data/metric flows used for analysis and CI regression detection.

## 1. System Context

```mermaid
flowchart LR
	Dev[Developer or CI Runner]
	Runner[Go Runner\ncmd/runner/main.go]
	K8s[Kubernetes Kind Cluster]
	Prom[Prometheus]
	Artifacts[JSON Results + HTML Report]

	Dev --> Runner
	Runner --> K8s
	K8s --> Prom
	Runner --> Prom
	Runner --> Artifacts
```

## 2. Core Components

```mermaid
flowchart TB
	subgraph Runner[Go Orchestrator]
		Main[main.go]
		K8sPkg[pkg/k8s\nclient.go + manifests.go]
		MetricsPkg[pkg/metrics\nprometheus.go + regression.go]
		ScenarioPkg[pkg/scenarios\nYAML + streaming + fairness]
		ReportPkg[pkg/report\nhtml.go]
	end

	subgraph Cluster[Kubernetes Resources]
		Gw[Gateway + HTTPRoute]
		Sim[llm-d-inference-sim tiers]
		Job[inference-perf Job]
	end

	subgraph Obs[Observability]
		Prom[(Prometheus)]
	end

	Main --> K8sPkg
	Main --> MetricsPkg
	Main --> ScenarioPkg
	Main --> ReportPkg

	K8sPkg --> Gw
	K8sPkg --> Sim
	K8sPkg --> Job

	MetricsPkg --> Prom
	Job --> Gw
	Gw --> Sim
```

## 3. End-to-End Scenario Lifecycle

```mermaid
flowchart TD
	Start[Parse flags and scenario selection] --> Load[Load YAML scenario config]
	Load --> Baseline[Load baseline results]
	Baseline --> Apply[Apply manifests and deploy resources]
	Apply --> Wait[Wait for pods and preflight checks]
	Wait --> Warmup[Warmup period]
	Warmup --> Run[Run inference-perf load job]
	Run --> Scrape[Scrape Prometheus metrics]
	Scrape --> Special{Special scenario path?}
	Special -->|streaming| Stream[Measure TTFT and ITL]
	Special -->|fairness| Fair[Collect tier distribution]
	Special -->|none| Save[Save scenario results]
	Stream --> Save
	Fair --> Save
	Save --> Overhead[Compute gateway overhead]
	Overhead --> Regress[Check regression vs baseline]
	Regress --> Report[Generate HTML report]
	Report --> Done[Write artifacts and exit]
```

## 4. Kubernetes Resource Orchestration

```mermaid
sequenceDiagram
	participant R as Runner
	participant K as Kubernetes API
	participant H as Helm
	participant J as inference-perf Job

	R->>K: Apply simulator tier manifests
	R->>K: Apply gateway and httproute manifests
	R->>K: Apply inference CRDs (with fallback if needed)
	R->>K: Wait for pods readiness
	R->>H: helm install inference-perf
	H->>J: Create benchmark job
	R->>K: Wait for job completion
	R->>H: helm uninstall inference-perf
	R->>K: Cleanup resources
```

## 5. Metrics Collection Pipeline

```mermaid
flowchart LR
	Job[inference-perf traffic] --> Gw[Gateway dataplane]
	Gw --> Sim[Simulator tiers]
	Gw --> Prom[(Prometheus)]
	Sim --> Prom

	Runner[PrometheusClient] --> Query[Build PromQL by data plane]
	Query --> Retry[queryWithRetry with backoff]
	Retry --> Parse[Parse instant/range vectors]
	Parse --> Output[Scenario metrics\nP50/P95/P99, RPS, error rate, CPU, memory]
```

## 6. Streaming TTFT/ITL Measurement Path

```mermaid
sequenceDiagram
	participant R as Runner
	participant P as Port-Forward
	participant S as Simulator SSE Endpoint

	R->>P: Start local port-forward
	R->>S: POST stream request (Accept: text/event-stream)
	S-->>R: data chunk #1
	Note over R: TTFT = t(first chunk) - t(request start)
	S-->>R: data chunk #2..N
	Note over R: ITL = mean(delta between consecutive chunks)
	S-->>R: [DONE]
	R->>R: Save TTFT and ITL metrics
```

## 7. Regression and CI Decision Flow

```mermaid
flowchart TD
	Current[Current scenario result] --> Compare[Compare with baseline]
	Baseline[Baseline result] --> Compare
	Compare --> P99{P99 delta > threshold?}
	Compare --> Err{Error rate increase > limit?}
	P99 -->|yes| Fail[Mark regression exceeded]
	Err -->|yes| Fail
	P99 -->|no| PassPath[Continue]
	Err -->|no| PassPath
	Fail --> Exit1[Exit code 1 in CI]
	PassPath --> Exit0[Exit code 0]
```

## 8. Data-Plane Query Abstraction

```mermaid
flowchart TB
	Input[--data-plane flag] --> Choice{Selected plane}
	Choice -->|envoy| EnvoyQ[Use envoy metric names\nand envoy_cluster_name labels]
	Choice -->|agentgateway| AgentQ[Use agentgateway metric names\nand namespace/service labels]
	EnvoyQ --> Metrics[Normalized results model]
	AgentQ --> Metrics
```

## 9. Failure Diagnostics and Hardening Loop

```mermaid
flowchart LR
	Detect[Detect invalid or zero-value run] --> Gather[Collect diagnostics\nqueries, logs, statuses]
	Gather --> Classify[Classify root cause\nlabels, scrape timing, routing]
	Classify --> Patch[Apply fix\nquery, manifest, preflight]
	Patch --> ReRun[Re-run scenario]
	ReRun --> Detect
```

## Repository Mapping

- `cmd/runner/main.go`: Orchestration entrypoint and scenario lifecycle.
- `pkg/k8s/client.go`: Cluster waits, port-forward helpers, diagnostics.
- `pkg/k8s/manifests.go`: Manifest apply/delete and fallback handling.
- `pkg/metrics/prometheus.go`: PromQL building, retries, metric scraping.
- `pkg/metrics/regression.go`: Baseline I/O and regression checks.
- `pkg/scenarios/*.go`: Scenario definitions, validation, streaming/fairness logic.
- `pkg/report/html.go`: HTML report generation.
- `.github/workflows/benchmark.yaml`: PR and nightly CI workflow.

## Design Principles

- Keep orchestration, measurement, and reporting separated.
- Treat metric validity as a first-class concern (retries, guardrails, diagnostics).
- Support both Envoy and agentgateway through a shared abstraction.
- Keep scenario configuration YAML-driven for reproducibility.
- Optimize for CI reliability and actionable artifacts.