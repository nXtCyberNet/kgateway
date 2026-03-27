# kgateway Benchmarking Reference

This module benchmarks inference-routing behavior in kgateway end-to-end.
It deploys simulator backends, configures Gateway resources, runs synthetic traffic, scrapes Prometheus metrics, computes regressions, and publishes JSON plus HTML reports.

## Table Of Contents

1. Overview
2. Goals And Non-Goals
3. Architecture
4. End-To-End Execution Flow
5. Scenario Catalog
6. Repository Map
7. Environment And Prerequisites
8. Local Setup And Runbook
9. Runner CLI And Behavior
10. Metrics, Regression, And Reporting
11. CI Workflow Details
12. Known Issues And Failure Modes
13. Error Playbooks
14. Current Technical Debt And Next Fixes
15. Command Reference

## Overview

The benchmarking stack answers these questions with reproducible runs:

- What is the gateway tax (P99 delta vs baseline)?
- How much latency is added by inference routing and policy layers?
- Is request distribution fair across backend tiers under EPP constraints?
- Are streaming metrics (TTFT, ITL) stable under load?
- Are changes regressing latency or error rate compared to baseline?

The benchmark is designed for both local iteration and CI automation.

## Goals And Non-Goals

### Goals

- Measure scenario-level P50, P95, P99, throughput, error rate, CPU, memory.
- Capture streaming metrics (TTFT, ITL) when applicable.
- Compute gateway tax using baseline P99 as control.
- Detect regressions automatically and fail CI when thresholds are exceeded.
- Keep scenario configuration human-editable via YAML.

### Non-Goals

- Absolute production capacity validation.
- Full conformance testing of all Gateway API features.
- Multi-cluster orchestration or production-grade load generation reliability.

## Architecture

The module has five major components:

1. Orchestrator
The Go runner (`cmd/runner/main.go`) drives setup, load, scraping, cleanup, and reporting.

2. Kubernetes Runtime Layer
`pkg/k8s` provides typed orchestration helpers for:
- Helm install/uninstall for load jobs
- Manifest apply/delete (directory and file)
- Pod readiness wait
- Job completion wait
- Job log collection
- Pod port-forward for streaming probes

3. Workload Under Test
Simulator deployments/services in `manifests/simulator` emulate heterogeneous inference tiers (large, medium, small).

4. Control Plane And Routing
Gateway resources and inference extension manifests are applied from `manifests`.
Per-data-plane gateways are available under `manifests/envoy` and `manifests/agentgateway`.

5. Observability And Output
Prometheus queries in `pkg/metrics/prometheus.go` produce scenario metrics.
Regression and summary logic in `pkg/metrics/regression.go`.
HTML dashboard generation in `pkg/report/html.go`.

## End-To-End Execution Flow

For each scenario, the runner does this sequential pipeline:

1. Load scenario YAML from `scenarios/*.yaml`.
2. Apply simulator manifests (`manifests/simulator`).
3. Apply gateway manifest (`manifests/gateway.yaml` or data-plane-specific override).
4. If inference routing is enabled:
- apply `inference-pool.yaml`
- apply `inference-model.yaml`
- if cluster lacks v1alpha2 support, fallback to v1 manifest variants
5. Wait for expected simulator pod count based on scenario replicas.
6. Warmup sleep (`warmupSeconds`).
7. Install Helm load job chart (`helm/inference-perf`) with runtime values:
- scenario URL
- target RPS
- duration
- concurrent users
- warmup seconds
- active deadline seconds
8. Wait for job completion with timeout buffer.
9. Scrape metrics from Prometheus.
10. Run scenario-specific checks:
- streaming: direct SSE TTFT/ITL measurement
- fairness: expected tier distribution validation
11. Save per-scenario JSON output.
12. Update `baseline.json` when scenario is `baseline`.
13. Compute gateway tax and regression results.
14. Generate summary table and HTML report.
15. Cleanup:
- uninstall helm job
- delete manifests in reverse-safe order

## Scenario Catalog

Current source of truth is YAML in `benchmarking/scenarios`.

### baseline

- Purpose: control sample for gateway tax
- Inference routing: disabled
- Body parsing: disabled
- Current config:
	- targetRPS: 100
	- durationSeconds: 120
	- concurrentUsers: 10
	- warmupSeconds: 60
	- tiers: large only, replicas 2

### header-routing

- Purpose: route-level overhead without inference extension
- Inference routing: disabled
- Body parsing: disabled
- Current config:
	- targetRPS: 100
	- durationSeconds: 120
	- concurrentUsers: 10
	- warmupSeconds: 60
	- tiers: large only, replicas 2

### inference-routing

- Purpose: full inference routing overhead under heterogeneous tiers
- Inference routing: enabled
- Body parsing: enabled
- Current config:
	- targetRPS: 100
	- durationSeconds: 120
	- concurrentUsers: 20
	- warmupSeconds: 15
	- tiers: large, medium, small

### streaming

- Purpose: SSE behavior, TTFT and ITL quality
- Inference routing: enabled
- Body parsing: enabled
- Current config:
	- targetRPS: 20
	- durationSeconds: 60
	- concurrentUsers: 5
	- warmupSeconds: 10
	- tiers: large, small

### epp-fairness

- Purpose: distribution fairness validation by tier
- Inference routing: enabled
- Body parsing: enabled
- Current config:
	- targetRPS: 50
	- durationSeconds: 120
	- concurrentUsers: 15
	- warmupSeconds: 15
	- tiers: large, medium, small

Fairness target in code is 70/20/10 with tolerance 5 percent (`pkg/scenarios/epp_fairness.go`).

## Repository Map

- `cmd/runner`: CLI entrypoint and orchestration pipeline
- `pkg/k8s`: kubectl/helm equivalents in code
- `pkg/metrics`: Prometheus queries, regression logic, summary table
- `pkg/report`: HTML report generation
- `pkg/scenarios`: typed scenario model and helper logic
- `manifests`: all Kubernetes resources used by scenarios
- `helm/inference-perf`: load-generator job chart
- `scenarios`: editable scenario profiles
- `scripts/setup-kind.sh`: one-command local cluster setup
- `results`: output JSONs and report artifacts

## Environment And Prerequisites

- Kind
- kubectl
- Helm
- Go matching `benchmarking/go.mod` (currently Go 1.25.0)
- Docker/runtime network that can pull from quay.io and ghcr.io
- Prometheus accessible to runner via `--prometheus-url`

## Local Setup And Runbook

### Cluster bootstrap

Run from `benchmarking`:

```bash
bash scripts/setup-kind.sh
```

What the script does:

1. Creates kind cluster `kgateway-bench`.
2. Waits for API server and nodes readiness.
3. Applies Gateway API CRDs from experimental bundle.
4. Applies inference extension CRDs:
- inferencepools
- inferencemodelrewrites
- inferenceobjectives
5. Installs kgateway charts with AI extension enabled.
6. Installs Prometheus chart.
7. Waits for core monitoring workloads with retries.

### Benchmark run

```bash
go run ./cmd/runner \
	--scenario all \
	--namespace default \
	--data-plane envoy \
	--prometheus-url http://127.0.0.1:9090 \
	--baseline results/baseline.json \
	--threshold 20 \
	--output results/real-all
```

### Output

- Per-scenario JSON: `<output>/<scenario>_<timestamp>.json`
- Baseline file: `results/baseline.json` (updated by baseline scenario)
- HTML report: `<output>/report.html`

## Runner CLI And Behavior

Main flags:

- `--scenario`: baseline, header-routing, inference-routing, streaming, epp-fairness, all
- `--kubeconfig`: optional kubeconfig path
- `--prometheus-url`: Prometheus HTTP API URL
- `--scenarios-dir`: YAML scenario directory
- `--output`: output directory
- `--baseline`: baseline JSON path
- `--threshold`: regression threshold percentage
- `--data-plane`: envoy or agentgateway
- `--namespace`: namespace for all benchmark resources
- `--stub`: synthetic mode, skips K8s and Prometheus calls

Important runtime behavior:

- Baseline load failure is non-fatal for run start, but regressions are skipped without baseline.
- Job timeout includes CI startup buffer and has an 8 minute minimum.
- Runner prints chart path and resolved target URL before Helm install.
- Service resolution now maps `app: llm-d-sim` to tier Services like `llm-d-sim-large` to avoid routing baseline traffic to a non-existent Service.
- If job wait fails, runner attempts to include job logs in error output.

## Metrics, Regression, And Reporting

### Metrics collection

Collected in `pkg/metrics/prometheus.go`:

- Latency percentiles: P50, P95, P99
- Error rate
- Gateway CPU and memory
- EPP decision latency
- Agentgateway TTFT/ITL (for agentgateway data plane)

Streaming scenario additionally captures TTFT and ITL via direct SSE measurement from `pkg/scenarios/streaming.go`.

### Gateway tax

Computed as:

- `gatewayOverheadMs = scenarioP99 - baselineP99`

### Regression checks

`pkg/metrics/regression.go` marks regression when either:

- P99 exceeds baseline by threshold percent, or
- error rate increases by more than 0.01 absolute

### HTML report

`pkg/report/html.go` renders a single standalone file with:

- scenario table
- status badges
- regression state
- infrastructure utilization summary

## CI Workflow Details

Primary workflow is repository-level `.github/workflows/benchmark.yaml`.

Triggers:

- push on `benchmarking/**` and workflow file
- pull_request on `benchmarking/**`
- nightly schedule (`0 2 * * *`)
- manual `workflow_dispatch` with inputs

Manual inputs:

- mode (`poc` or `full`)
- scenario
- data_plane
- threshold
- kgateway_version

Pipeline summary:

1. `benchmark-poc` job (default for push/PR):
- checkout + setup Go
- install kind, kubectl, helm
- run `benchmarking/scripts/setup-kind.sh`
- port-forward Prometheus
- run real `baseline` scenario
- upload POC artifact (`benchmark-poc-results`)

2. `benchmark-full` job (nightly or manual `mode=full`):
- checkout + setup Go
- install kind, kubectl, helm
- run `benchmarking/scripts/setup-kind.sh`
- port-forward Prometheus
- run selected/full benchmark scenarios against real cluster
- upload full artifacts and PR comment summary when applicable

Why this split exists:

- `benchmark-poc` gives a real baseline benchmark result for PoC/demo workflows.
- `benchmark-full` preserves full-fidelity integration benchmarking when explicitly requested.

## Known Issues And Failure Modes

### Private image pull failures for load generator

Problem:

- previous load image `ghcr.io/llm-d/inference-perf:latest` can return `403 Forbidden` in GitHub Actions and other unauthenticated environments.

Current handling:

- `helm/inference-perf` now uses the public pinned image `quay.io/inference-perf/inference-perf:v0.4.0`.
- the benchmark job runs the real `inference-perf` CLI with a generated config file mounted via ConfigMap.
- the tool's `server.model_name` is set to a public model ID for tokenizer initialization reliability; request routing target remains the in-cluster simulator endpoint.
- this removes dependency on private GHCR package access for baseline/full runs.

Impact:

- load generation and request lifecycle reporting are produced by the actual `inference-perf` tool.

### Inference CRD compatibility drift

Problem:

- Upstream CRD layouts change, causing missing-kind errors.

Current handling:

- setup script installs specific inference CRD files
- runner has fallback from v1alpha2 manifests to v1 variants

Open risk:

- `InferenceModel` availability can differ by extension version; manifests may require future migration to newer resource types.

### SSA typed patch schema error on InferencePool

Problem:

- some cluster/schema combinations fail SSA on `spec.selector.matchLabels`

Current handling:

- apply path now attempts SSA first, then falls back to create/update for inference resources on known schema error signatures

### DNS/image pull instability in local and CI environments

Problem:

- quay.io or ghcr.io pull latency/timeouts block Prometheus or job startup

Current handling:

- retry logic in setup-kind readiness waits
- larger runner job timeout startup buffer
- early non-progress detection in job wait path
- POC workflow now runs a real baseline path (not synthetic), so DNS/image pull stability still matters

### Scenario drift between YAML and Go constructors

Problem:

- runner loads YAML, not constructor defaults

Impact:

- changing Go scenario constructors does not affect runtime unless YAML is updated

Recommendation:

- treat YAML as source of truth and keep constructors aligned or deprecate constructors for non-test use

## Error Playbooks

### Job timeout waiting for inference-perf

Run:

```bash
kubectl -n default get jobs,pods
kubectl -n default describe job inference-perf
kubectl -n default logs job/inference-perf
kubectl -n default get events --sort-by=.lastTimestamp | tail -n 80
```

Check for:

- image pull failures
- unresolved service DNS
- crash loops in perf container
- deadline exceeded before workload starts

### InferencePool apply error with matchLabels expected string

Meaning:

- SSA schema typing issue surfaced before object apply

Current status:

- fallback path is implemented in manifests apply logic

### Prometheus rollout stuck in setup-kind

Typical root cause:

- transient image pull DNS failures

Run:

```bash
kubectl -n monitoring get pods -o wide
kubectl -n monitoring describe pod -l app.kubernetes.io/name=prometheus
kubectl -n monitoring get events --sort-by=.lastTimestamp | tail -n 120
```

## Current Technical Debt And Next Fixes

1. Inference API evolution alignment
- Revisit manifests to match latest upstream inference extension kinds and versions.

2. Unified scenario source
- Decide whether YAML or Go constructors are canonical and enforce consistency checks.

3. More actionable failure logs
- On job wait failure, append pod describe/events automatically in runner output.

4. CI resiliency
- Add optional mirror/pull-through cache strategy for external images if network flakiness persists.

## Command Reference

Run baseline only:

```bash
go run ./cmd/runner --scenario baseline --namespace default --prometheus-url http://127.0.0.1:9090 --output results/baseline-run
```

Run baseline in stub mode (fast PoC output):

```bash
go run ./cmd/runner --scenario baseline --stub --output results/poc
```

Note: stub output is synthetic and intended only for local pipeline sanity checks.
CI `benchmark-poc` runs a real baseline benchmark.

Run all scenarios with explicit baseline and threshold:

```bash
go run ./cmd/runner --scenario all --namespace default --data-plane envoy --prometheus-url http://127.0.0.1:9090 --baseline results/baseline.json --threshold 20 --output results/full-run
```

Stub mode (no cluster calls):

```bash
go run ./cmd/runner --scenario all --stub --output results/stub
```

Run tests for benchmarking module:

```bash
go test ./...
```
