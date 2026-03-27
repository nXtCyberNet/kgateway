# kgateway Benchmarking Reference

This directory contains the inference-routing benchmarking framework for kgateway.
It measures end-to-end request performance and computes gateway overhead ("gateway tax") for different routing and policy setups.

## What This Project Is

kgateway is a Kubernetes Gateway API control plane for Envoy. The benchmarking module focuses on AI/inference traffic scenarios and answers practical questions such as:

- How much latency does routing through kgateway add versus direct simulator access?
- How does inference routing behavior affect P99 latency?
- Are streaming metrics (TTFT/ITL) stable under load?
- Are fairness goals met for tiered backends?

## Key Features

- Scenario-driven benchmark runner (`cmd/runner`)
- Multiple scenarios: `baseline`, `header-routing`, `inference-routing`, `streaming`, `epp-fairness`
- Automatic baseline handling for regression checks
- Threshold-based regression detection (`--threshold`, default `20`)
- Data-plane selection (`--data-plane envoy|agentgateway`)
- Metrics collection from Prometheus (latency, resource, fairness-related data)
- HTML report generation plus per-scenario JSON output
- Stub mode (`--stub`) for validating report pipeline without a live cluster
- Helm-based load generation (`helm/inference-perf`)
- Fallback handling for inference manifests (`v1alpha2` -> `v1` when needed)

## Repository Layout (Benchmarking)

- `cmd/runner`: CLI runner and orchestration logic
- `pkg/k8s`: Kubernetes and Helm interactions
- `pkg/metrics`: scraping, regression logic, summaries
- `pkg/scenarios`: scenario definitions and validators
- `manifests`: gateway, simulator, inference manifests
- `scenarios`: YAML scenario configs
- `helm/inference-perf`: load job chart
- `results`: generated benchmark artifacts
- `scripts/setup-kind.sh`: local benchmark cluster bootstrap

## Prerequisites

- Kind
- kubectl
- Helm
- Go (benchmarking module currently uses `go 1.25.0` in `benchmarking/go.mod`)
- Prometheus reachable by the runner (`--prometheus-url`)

## Quick Start

From the `benchmarking` directory:

```bash
bash scripts/setup-kind.sh
go run ./cmd/runner \
	--scenario all \
	--namespace default \
	--data-plane envoy \
	--prometheus-url http://127.0.0.1:9090 \
	--baseline results/baseline.json \
	--threshold 20 \
	--output results/real-all
```

Output artifacts:

- JSON files in the output folder
- HTML report at `<output>/report.html`

## Runner Flags (Most Used)

- `--scenario`: one of `baseline|header-routing|inference-routing|streaming|epp-fairness|all`
- `--namespace`: Kubernetes namespace (default `default`)
- `--data-plane`: `envoy` or `agentgateway`
- `--prometheus-url`: Prometheus API endpoint
- `--baseline`: baseline JSON path for regression checks
- `--threshold`: regression threshold percent for P99 checks
- `--output`: output directory
- `--stub`: synthetic run without cluster calls

## Current Problems Seen So Far and Solutions

### 1) Missing Inference CRDs

Symptom:

- `no matches for kind "InferencePool" in version "inference.networking.x-k8s.io/..."`

Cause:

- Standard Gateway API experimental CRDs do not include Gateway API Inference Extension CRDs.

Solution:

- Ensure inference extension CRDs are applied before running inference scenarios.
- `benchmarking/scripts/setup-kind.sh` has been updated to apply:
	- `inference.networking.x-k8s.io_inferencepools.yaml`
	- `inference.networking.x-k8s.io_inferencemodelrewrites.yaml`
	- `inference.networking.x-k8s.io_inferenceobjectives.yaml`
- Note: `inference.networking.x-k8s.io_inferencemodels.yaml` currently returns 404 upstream.

### 1.1) InferenceModel Kind Compatibility Risk

Symptom:

- Inference scenarios may fail while applying `InferenceModel` even after CRDs are installed.

Cause:

- Current upstream CRD set in `config/crd/bases` may not include an `InferenceModel` CRD file, while benchmark manifests still reference `InferenceModel` resources.

Solution:

- Check served resources in your cluster (`kubectl api-resources | grep -i inference`).
- If `InferenceModel` is not served, update benchmark manifests/scenarios to the currently supported inference API objects.

### 2) Baseline File Missing (Regression Checks Skipped)

Symptom:

- Runner warns baseline was not loaded and skips regression checks.

Cause:

- Baseline path does not exist yet.

Solution:

- Run the benchmark at least once so baseline can be saved.
- Keep `--baseline` pointing to the same file for future comparisons.

### 3) `inference-perf` Job Timeout

Symptom:

- Timeout waiting for job completion.

Likely causes:

- Upstream service not reachable
- Pods not ready
- Helm job failing internally
- Cluster resource pressure

Solution checklist:

- Inspect job logs: `kubectl -n <ns> logs job/inference-perf`
- Inspect job/pod status: `kubectl -n <ns> get jobs,pods`
- Confirm simulator pods are ready and service DNS target exists
- Re-run single scenario first (for example `baseline`) to narrow scope

### 4) Incorrect `kubectl logs` Resource Type

Symptom:

- `deployments.apps "<pod-name>" not found`

Cause:

- Pod name used with `deploy/` prefix.

Solution:

- Use `pod/<pod-name>` or query deployment logs by deployment name.

## Troubleshooting Commands

```bash
kubectl -n default get pods,jobs
kubectl -n default logs job/inference-perf
kubectl -n default describe job inference-perf
kubectl -n default get events --sort-by=.lastTimestamp | tail -n 50
```

## Practical Workflow

1. Run `scripts/setup-kind.sh`.
2. Run `baseline` scenario and verify completion.
3. Confirm `results/baseline.json` exists.
4. Run `all` scenarios with regression threshold.
5. Review `<output>/report.html` and scenario JSON files.

## Notes

- Inference routing scenarios require both core Gateway API CRDs and Inference Extension CRDs.
- If cluster setup changes, re-run setup to avoid stale CRD/API mismatches.
- When debugging, run a single scenario first to reduce noise and speed iteration.
