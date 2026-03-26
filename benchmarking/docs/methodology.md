# Benchmark Methodology

## Goals

- Measure latency and throughput across data plane variants.
- Track regressions against a stored baseline.
- Capture streaming metrics including TTFT and ITL.

## Scenarios

- Baseline request latency.
- Inference routing behavior.
- Streaming token timing.
- EPP fairness under mixed backend delays.

## Reporting

Results are stored in JSON and summarized in static HTML for CI publishing.
