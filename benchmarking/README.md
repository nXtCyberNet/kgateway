# Benchmarking Framework for kgateway Inference Routing

![Go Version](https://img.shields.io/badge/go-1.22-blue) ![License](https://img.shields.io/badge/license-Apache_2.0-blue)

This framework evaluates the "gateway tax" introduced by kgateway when routing LLM inference requests. It runs orchestrated tests against simulated backends to measure precise P99 latency, TTFT, ITL, and EPP overhead.

## Prerequisites
- Kind
- kubectl
- Helm
- Go 1.22+

## Quick Start
`ash
bash scripts/setup-kind.sh
go run ./cmd/runner --scenario all --output results/
open results/
``n
## Scenarios
| Scenario | Description | Expected Bottleneck |
|---|---|---|
| S1 (Baseline) | TCP direct to simulator. | Network IO |
| S2 (Header Route) | Basic envoy HTTP header routing. | Envoy parsing |
| S3 (Inference Route) | Full EPP & body metrics. | EPP processing |
| S4 (Streaming) | SSE measuring TTFT / ITL. | Network buffering |
| S5 (EPP Fairness) | GPU simulator ratio validation. | EPP queuing |

## Metrics Collected
- P50, P95, P99 Latency
- TTFT / ITL (Streaming)
- EPP Decision Latency
- Envoy CPU/Memory

## Regression Detection
Compares baseline.json P99s against new runs dynamically, failing CI natively if a threshold (e.g., >20%) is exceeded.

## CI/CD
Runs nightly via .github/workflows/benchmark.yaml.
