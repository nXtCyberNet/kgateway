# kgateway Inference Routing — Benchmarking Framework Documentation

> **Purpose**: This document is the authoritative reference for the design, features, tradeoffs,
> known limitations, and operational guidance of the kgateway benchmarking framework.
> It is intended for GSoC contributors, kgateway maintainers, and platform engineers
> who want to understand not just *how* the framework works but *why* each decision was made.

---

## Table of Contents

1. [What This Framework Measures](#1-what-this-framework-measures)
2. [Architecture Overview](#2-architecture-overview)
3. [Feature Reference](#3-feature-reference)
4. [Design Tradeoffs](#4-design-tradeoffs)
5. [Benchmark Scenarios — Deep Dive](#5-benchmark-scenarios--deep-dive)
6. [Metrics Reference](#6-metrics-reference)
7. [The EPP — How It Works and Why It's Hard to Benchmark](#7-the-epp--how-it-works-and-why-its-hard-to-benchmark)
8. [MIG Simulation Strategy](#8-mig-simulation-strategy)
9. [Regression Detection](#9-regression-detection)
10. [Stub Mode](#10-stub-mode)
11. [Known Limitations](#11-known-limitations)
12. [Operational Runbook](#12-operational-runbook)
13. [Future Work](#13-future-work)

---

## 1. What This Framework Measures

kgateway adds routing intelligence on top of standard HTTP proxying. When inference routing
is enabled, every request travels through an additional path:

```
Client → Envoy listener → ext_proc filter (gRPC) → EPP → backend pod
```

The `ext_proc` call parses the request body to extract the `model` field, calls the
Endpoint Picker (EPP) over gRPC, waits for the EPP to score all available pods using
live Prometheus metrics, and injects a routing header before forwarding.

**The core question this framework answers:**

> *How many milliseconds does inference routing add compared to standard HTTP routing,
> and is that overhead justified by improved backend utilization?*

This delta is called the **gateway tax** throughout the codebase and documentation.
It is computed as:

```
GatewayOverheadMs = P99(inference-routing) − P99(baseline)
```

Where `baseline` is a direct TCP connection to the simulator with no gateway at all.

---

## 2. Architecture Overview

The framework has four layers:

```
┌─────────────────────────────────────────────────────────────────┐
│ Layer A — Go Orchestrator  (cmd/runner/main.go)                 │
│  parse flags → load scenarios → apply K8s → warmup → run job   │
│  → scrape Prometheus → regression check → write report         │
├──────────────────────┬──────────────────────────────────────────┤
│ Layer B — Load Gen   │  Layer B — System Under Test            │
│  inference-perf Job  │  kgateway (Envoy or agentgateway)       │
│  (Helm, K8s Job)     │  + EPP + llm-d-inference-sim backends   │
├──────────────────────┴──────────────────────────────────────────┤
│ Layer C — Metrics Collection                                    │
│  Prometheus scrape: latency histograms, CPU, memory, EPP time  │
│  Direct SSE client: TTFT and ITL for streaming scenario        │
├─────────────────────────────────────────────────────────────────┤
│ Layer D — Reporting                                             │
│  results/*.json per run, report.html, regression exit code     │
└─────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Lives in |
|---|---|---|
| `cmd/runner/main.go` | Orchestrates the full lifecycle | `cmd/runner/` |
| `pkg/scenarios/` | Scenario config structs, streaming client, fairness check | `pkg/scenarios/` |
| `pkg/metrics/` | Prometheus client, regression logic, summary table | `pkg/metrics/` |
| `pkg/k8s/` | Kubernetes client wrapper, manifest apply/delete, Helm wrapper, PortForward | `pkg/k8s/` |
| `pkg/report/` | HTML report generator | `pkg/report/` |
| `manifests/` | Kubernetes YAML for simulator, Gateway, InferencePool, InferenceModel | `manifests/` |
| `helm/inference-perf/` | Helm chart for the inference-perf load generator Job | `helm/` |
| `scenarios/*.yaml` | Runtime scenario config (RPS, duration, tiers) | `scenarios/` |

---

## 3. Feature Reference

### 3.1 Five Benchmark Scenarios

Each scenario is driven by a YAML file under `scenarios/` and a corresponding Go
constructor in `pkg/scenarios/`. The YAML is the source of truth at runtime; the
Go constructors are used for programmatic access and testing.

| Scenario | File | Gateway On | Body Parse | EPP | Primary Metric |
|---|---|---|---|---|---|
| S1 baseline | `baseline.yaml` | No | No | No | Floor P99 latency |
| S2 header-routing | `header-routing.yaml` | Yes | No | No | L7 proxy overhead |
| S3 inference-routing | `inference-routing.yaml` | Yes | Yes | Yes | EPP overhead |
| S4 streaming | `streaming.yaml` | Yes | Yes | Yes | TTFT, ITL |
| S5 epp-fairness | `epp-fairness.yaml` | Yes | Yes | Yes | Traffic distribution |

### 3.2 Dual Data Plane Support

The `--data-plane` flag switches between two data plane implementations:

- **`envoy`** (default): Envoy C++ proxy with a Go `ext_proc` sidecar. The EPP is a
  separate process. Body parsing requires a context switch from Envoy into Go.
- **`agentgateway`**: Rust async proxy. Body parsing is in-process, no context switch.
  EPP uses the same gRPC protocol but from within the same process boundary.

The flag affects three things:
1. Which `manifests/<dataplane>/gateway.yaml` is applied.
2. Which Prometheus metric names are queried (Envoy vs agentgateway emit different names).
3. The `DataPlane` field stamped on every `Results` JSON for side-by-side comparison.

### 3.3 Stub Mode

`--stub` skips all Kubernetes and Helm calls and returns synthetic results. Designed
for validating the reporting pipeline without a live cluster. See §10 for full details.

### 3.4 Gateway Tax Computation

After all scenarios complete, `computeGatewayOverhead` calculates:

```go
r.GatewayOverheadMs = r.P99LatencyMs - baseline.P99LatencyMs
```

This is the headline number for the project: how much latency the gateway adds.
The color coding in the HTML report uses this field directly:
- Green: overhead < 5 ms
- Yellow: overhead 5–15 ms
- Red: overhead > 15 ms

### 3.5 Regression Detection

Compares `P99LatencyMs` and `ErrorRate` of the current run against a stored
`baseline.json`. Exits with code 1 if any threshold is exceeded so CI fails.
Threshold is configurable via `--threshold` (default 20%). See §9.

### 3.6 SSE Streaming Measurement

The streaming scenario uses a Go HTTP client that sets `Accept: text/event-stream`
and times the arrival of each SSE `data:` chunk to compute:
- **TTFT** (Time to First Token): wall-clock ms from request send to first chunk arrival.
- **ITL** (Inter-Token Latency): mean µs between consecutive chunk arrivals.

These are streaming-native metrics that standard request/response benchmarks cannot
capture at all. They are written into `Results.TTFTMeanMs` and `Results.ITLMeanUs`.

### 3.7 EPP Fairness Validation

After the `epp-fairness` scenario, the framework queries per-tier request counts from
Prometheus and calls `CheckFairness` with the expected distribution (70/20/10 for
large/medium/small tiers). A violation is printed as a warning but does not fail the
run — it is an observability signal, not a hard regression gate.

### 3.8 HTML Report

A self-contained single-file HTML report is written to `results/report.html` after
every run. It includes:
- A results summary table with all scenarios and both data planes if run twice
- Color-coded gateway overhead per scenario
- Regression warnings with baseline vs current P99
- Streaming metrics (TTFT, ITL) for the streaming scenario
- Methodology note explaining what each delta means

### 3.9 Inference Manifest Fallback

`applyInferenceManifestWithFallback` detects whether the cluster serves
`inference.networking.x-k8s.io/v1alpha2` CRDs. If not, it falls back to a `-v1.yaml`
sibling manifest. This lets the framework run on clusters that have older Gateway API
CRD versions installed without requiring manual intervention.

---

## 4. Design Tradeoffs

This section documents every significant architectural decision, the alternative that
was considered, and why the chosen approach was selected.

---

### 4.1 Go Orchestrator vs Shell Scripts

**Decision**: Write the orchestrator in Go using `client-go`.

**Alternative**: Shell scripts wrapping `kubectl` and `helm` (the approach in
robin-vidal's PR #1170 for agentgateway).

**Why Go:**
- Shell scripts are not testable in isolation. The Go orchestrator can be unit-tested
  with stub clients.
- `client-go` gives typed access to pod phase, job completion conditions, and
  port-forward streams. Shell scripts poll with `kubectl wait` which has race conditions
  on job completion.
- Error handling in shell is error-prone (`set -euo pipefail` helps but still misses
  subshell failures). Go's `fmt.Errorf("context: %w", err)` gives full stack traces.
- The stub mode (`--stub`) is trivial to implement in Go but would require significant
  mocking infrastructure in shell.

**Tradeoff accepted**: Go requires more upfront code than shell. A contributor familiar
only with bash faces a steeper entry curve. Mitigated by keeping `cmd/runner/main.go`
as the single entry point with clearly named functions.

---

### 4.2 inference-perf as Load Generator vs k6 vs Fortio

**Decision**: Use `inference-perf` (the GIE project's own load generator) deployed as
a Kubernetes Job via Helm.

**Alternative A**: k6 — excellent for HTTP load testing, has SSE support, rich scripting.

**Alternative B**: Fortio — simple, fast, good histogram output, used widely in service
mesh benchmarking.

**Why inference-perf:**
- It is built specifically for LLM inference workloads. It understands OpenAI-compatible
  request/response format natively.
- It emits `llm_requests_total` and streaming timing metrics as Prometheus counters,
  which can be scraped by the same Prometheus instance that monitors the gateway.
- Alignment with the GIE (Gateway API Inference Extension) project means kgateway
  maintainers can compare results directly with upstream benchmarks.
- Running it as a Kubernetes Job means it executes inside the cluster, eliminating
  network latency between the load generator and the gateway.

**Tradeoff accepted**: inference-perf is less mature than k6 or Fortio. Its Helm chart
is not yet published to a public registry (hence the local `helm/inference-perf/` copy).
If inference-perf changes its API, the Helm values map in `HelmInstall` breaks silently.
Mitigated by pinning `appVersion` in `Chart.yaml`.

---

### 4.3 Prometheus Scrape vs inference-perf Output Parsing

**Decision**: Scrape Prometheus for P99/P95/P50 latency histograms rather than parsing
inference-perf's JSON output file.

**Alternative**: Parse `summary_lifecycle_metrics.json` that inference-perf writes to
its output directory (as robin-vidal's PR does).

**Why Prometheus:**
- Prometheus histograms give accurate percentiles. JSON summary output typically gives
  mean and a coarse percentile computed by the load generator itself, which may differ
  from server-observed latency.
- The gateway's CPU, memory, and EPP decision latency come from Prometheus regardless.
  Using a single source for all metrics simplifies the data pipeline.
- Prometheus data can be visualised in Grafana without changes to the benchmarking code.
- Regression checks on Prometheus data are stable across inference-perf version changes.

**Tradeoff accepted**: Prometheus scraping requires port-forwarding the Prometheus pod
or exposing it via a Service. In CI on a fresh Kind cluster this adds setup complexity.
Also, Prometheus metric names differ between Envoy and agentgateway, requiring a
`dataPlane` branch in `buildGatewayQueries`. Mitigated by the `DataPlane` parameter on
`ScrapeGatewayMetrics` and careful documentation of metric names.

---

### 4.4 Server-Side Apply vs Get/Create/Update for Manifests

**Decision**: Use `Get → Create/Update` (the classic client-go pattern) for applying
Kubernetes manifests, not server-side apply (SSA).

**Alternative**: Server-side apply via `Patch` with `types.ApplyPatchType`, which is
the `kubectl apply` equivalent and handles field ownership correctly.

**Why Get/Create/Update:**
- The `InferencePool` and `InferenceModel` CRDs have a known schema bug where
  `spec.selector.matchLabels` is declared as `string` instead of `map[string]string`
  in the OpenAPI schema. SSA validates the patch object against the schema locally before
  sending it, causing the error:
  ```
  matchLabels: expected string, got map
  ```
  This is a bug in the upstream CRD definition, not in the framework.
- Get/Create/Update bypasses the local schema validation and sends the object directly
  to the API server, which accepts it correctly.

**Tradeoff accepted**: Get/Create/Update is a read-modify-write pattern with a race
window. If two concurrent applies happen (not currently possible since the orchestrator
is sequential), a 409 Conflict can occur. For a sequential benchmarking tool this is
not a real risk. If parallelism is added in future, migrate to SSA once the upstream
CRD schema is fixed.

---

### 4.5 Port-Forward for Prometheus vs In-Cluster Service URL

**Decision**: Port-forward the Prometheus pod to `localhost:9090` and query it locally.

**Alternative**: Use the in-cluster DNS name
`http://prometheus-server.monitoring.svc.cluster.local:80` directly from the runner.

**Why port-forward:**
- The Go runner executes outside the cluster (on the developer's machine or CI agent).
  In-cluster DNS names are not resolvable outside the cluster without additional network
  configuration.
- Port-forwarding is a standard Kubernetes pattern and requires no changes to the cluster
  network setup.

**Tradeoff accepted**: Port-forwarding adds latency to metric queries (each `QueryInstant`
call traverses the port-forward tunnel). This is fine because metric queries happen after
the load test completes — they do not affect the measurements themselves. Also, if the
port-forward drops during scraping, one or more metrics record as 0. Mitigated by the
retry wrapper in `QueryInstant` and a warning log when scraping returns zero.

---

### 4.6 Sequential Scenario Execution vs Parallel

**Decision**: Run all five scenarios sequentially, one at a time.

**Alternative**: Run scenarios in parallel to reduce total wall-clock time.

**Why sequential:**
- The scenarios share Kubernetes resources (namespace, simulator pods, gateway). Running
  them in parallel would mean metrics from one scenario contaminate another's Prometheus
  data.
- The EPP fairness scenario specifically needs the three-tier backends to be the *only*
  active workload so tier distribution is attributable to EPP decisions, not noise.
- CI resources (Kind cluster on a GitHub Actions runner) are too constrained to run
  multiple load generators simultaneously without hitting CPU throttling that skews results.

**Tradeoff accepted**: Five scenarios at ~5 minutes each is ~25 minutes of CI wall time.
This is acceptable for a nightly job but too slow for a per-PR gate on every commit.
Mitigated by the `--scenario` flag which lets CI run only the changed scenario on PRs
and reserve the full suite for nightly runs.

---

### 4.7 CPU-Throttled Tiers as MIG Simulation

**Decision**: Simulate GPU MIG partitions by running `llm-d-inference-sim` replicas
with different Kubernetes CPU limits and artificial response delays.

**Alternative**: Require real NVIDIA A100/H100 hardware with actual MIG partitions.

**Why CPU simulation:**
- MIG is hardware-locked to datacenter-class NVIDIA GPUs (A30, A100, H100). Consumer
  GPUs including the RTX 3050 do not support MIG even within the same Ampere architecture.
- Kind clusters (the local development environment) run on CPU-only nodes. No GPU
  hardware is available in standard CI.
- The EPP's routing logic does not inspect `nvidia.com/mig-*` resource types directly —
  it reads KV cache %, queue depth, and active request counts from Prometheus. Simulating
  different throughput capacities with CPU limits and `RESPONSE_DELAY_MS` environment
  variables exercises the same EPP decision paths that MIG would.

**Tradeoff accepted**: CPU simulation does not reproduce the PCIe bandwidth contention
or L3 cache sharing that real MIG environments exhibit. The "noisy neighbor" failure
mode (where one MIG partition's heavy decode phase saturates PCIe, degrading all other
partitions on the same physical card) cannot be reproduced. This is documented explicitly
in `ARCHITECTURE.md §7` so users are not misled.

**How to upgrade to real MIG when hardware is available:**
Replace the simulator `resources.limits` in tier manifests:
```yaml
# Simulation (CPU-based)
resources:
  limits:
    cpu: "4"
    memory: "4Gi"

# Real MIG (GPU-based)
resources:
  limits:
    nvidia.com/mig-3g.40gb: 1   # large partition
    # nvidia.com/mig-1g.10gb: 1 # small partition
```
Add the `mig.uuid` label to pod metadata so Prometheus can join token metrics to
physical device identity for the FinOps cost-per-token calculations.

---

### 4.8 Single Baseline File vs Per-Scenario Baselines

**Decision**: Use a single `baseline.json` (the S3 inference-routing result) as the
regression reference for all scenarios.

**Alternative**: Maintain a separate baseline file per scenario.

**Why single baseline:**
- Simplifies the regression check invocation — one `--baseline` flag, one comparison.
- The most important regression target is S3 (inference-routing P99) because that is
  the scenario closest to production traffic.
- Per-scenario baselines would require managing five files and keeping them synchronized
  across data plane changes.

**Tradeoff accepted**: Comparing S1 (baseline, no gateway) against S3's baseline JSON
produces a meaningless delta because they measure different things. The `CheckRegression`
function only compares same-named scenarios via `ScenarioName` matching, so this is not
a correctness issue — but it does mean that if the S1 result improves, the regression
check does not detect or celebrate it unless you explicitly baseline S1 separately.

---

### 4.9 Timestamp on Results vs Append-Only Log

**Decision**: Write each run as a separate timestamped JSON file
(`results/20260328120000_inference-routing.json`).

**Alternative**: Append each run as a line to a single JSONL (JSON Lines) file per scenario.

**Why separate files:**
- GitHub Actions artifact upload handles individual files more cleanly than one large JSONL.
- The HTML report can link to individual raw result files.
- Time-series analysis (trending P99 over multiple runs) is possible by sorting files by
  timestamp prefix and loading them in order.

**Tradeoff accepted**: The `results/` directory grows unbounded. In CI, artifact retention
policies (30 days) handle cleanup. For local development, users must manually prune old
results or use `--output` to point at a fresh directory per run.

---

## 5. Benchmark Scenarios — Deep Dive

### S1: TCP Baseline

**Purpose**: Establish the floor latency with zero gateway involvement.

**Setup**: Direct HTTP connection from inference-perf to the `llm-d-sim-large` Service,
bypassing the Gateway entirely. The simulator responds after its `RESPONSE_DELAY_MS`
(50 ms) with a synthetic response.

**What the P99 represents**: Network round-trip + kube-proxy overhead + simulator
processing. Any latency above this in S2–S5 is attributable to the gateway.

**Known confounders**: kube-proxy adds ~0.1 ms of iptables overhead per connection.
On clusters using eBPF-based CNI (Cilium, Calico eBPF mode) this is lower. The
baseline number is not truly "bare metal" — it is "cluster-floor latency."

---

### S2: Header Routing

**Purpose**: Measure the pure L7 proxy overhead with no body inspection.

**Setup**: kgateway with `EnableBodyParsing: false`. Routing happens via the
`x-model-name: meta-llama/Llama-3-8b` HTTP header. The EPP is not involved.

**Expected delta vs S1**: 1–3 ms. This is the cost of TLS termination, header
matching, and Envoy's filter chain with no body buffering.

**What a large S2–S1 delta means**: Envoy's listener configuration has unnecessary
filters enabled (e.g., JWT auth, rate limiting). Check `gateway.yaml` for
extraneous filter chain entries.

---

### S3: Inference Routing (Core Scenario)

**Purpose**: Measure the full EPP overhead — body parse + gRPC round-trip to EPP +
Prometheus metric read + routing header injection.

**Setup**: kgateway with `EnableBodyParsing: true`, `EnableInferenceRouting: true`.
Three backend tiers deployed (large: 2 replicas, medium: 1, small: 1).

**Expected delta vs S2**: 2–8 ms. This range comes from:
- JSON body parsing: ~0.1 ms for a small request body
- gRPC round-trip Envoy → EPP: ~0.5–1 ms
- EPP metric evaluation (in-memory, pre-scraped): ~0.1 ms
- Header injection and forwarding: ~0.1 ms

**What a large S3–S2 delta means (>8 ms)**:
- The EPP is making live Prometheus queries per request instead of using a pre-scraped
  cache. Check EPP log for `querying prometheus` per-request log lines.
- The `ext_proc` gRPC connection is not being reused. Envoy creates a new connection
  per request instead of pooling. Check `ext_proc` filter config for `grpc_service`
  connection pool settings.

---

### S4: Streaming (SSE)

**Purpose**: Measure streaming-specific metrics that S1–S3 cannot capture.

**TTFT (Time to First Token)**: The most important user-perceived metric for LLM
applications. Users experience TTFT as "how long before the chat box starts showing
output." For a 50 ms simulator, TTFT should be ≤ S3 P99 + 5 ms.

**ITL (Inter-Token Latency)**: The smoothness of token delivery. High ITL means
tokens arrive in bursts rather than smoothly, causing visible "stuttering" in chat UIs.
Expected: 5–20 µs for a local cluster.

**Setup**: `EnableBodyParsing: true`, `EnableInferenceRouting: true`, 30 RPS,
`Accept: text/event-stream` header required. The Go SSE client in
`pkg/scenarios/streaming.go` timestamps each `data:` chunk arrival.

**Known failure mode — stream fragmentation**: If any middleware in the filter chain
buffers the response body to inspect it (e.g., a safety filter or PII mask), the
entire SSE stream is held until the buffer fills, then released in a single burst.
This shows as TTFT = total response time and ITL = 0 (all tokens arrive simultaneously).
This is a critical failure to detect because it means streaming is effectively broken
even though P99 request latency looks normal.

---

### S5: EPP Fairness

**Purpose**: Validate that the EPP distributes traffic proportionally to backend capacity,
not uniformly.

**Expected distribution**:
| Tier | CPU Limit | Response Delay | Expected Traffic Share |
|---|---|---|---|
| large | 4 cores | 50 ms | ~70% |
| medium | 2 cores | 100 ms | ~20% |
| small | 0.5 cores | 200 ms | ~10% |

**How the check works**: After the load test, `ScrapeTierDistribution` queries
Prometheus for `sum by (tier) (rate(http_requests_total{namespace="..."}[1m]))`.
`CheckFairness` computes the deviation from the expected percentages and fails if any
tier deviates by more than `FairnessTolerancePct` (5 percentage points).

**What an equal-thirds distribution means**: The EPP is treating all endpoints as
identical regardless of capacity. This is the "dumb round-robin" failure mode. It
happens when the EPP's metric scrape interval is longer than the load test duration
(stale metrics) or when the `llm-d-inference-sim` pods are not emitting the KV cache /
queue depth metrics that the EPP depends on.

---

## 6. Metrics Reference

### Latency Metrics (from Prometheus)

| Metric | Prometheus Query (Envoy) | Prometheus Query (agentgateway) | Unit |
|---|---|---|---|
| P99 latency | `histogram_quantile(0.99, sum(rate(envoy_cluster_upstream_rq_time_bucket{...}[1m])) by (le))` | `histogram_quantile(0.99, sum(rate(agentgateway_request_duration_seconds_bucket{...}[1m])) by (le)) * 1000` | ms |
| P95 latency | same with 0.95 | same with 0.95 | ms |
| P50 latency | same with 0.50 | same with 0.50 | ms |
| EPP decision | `histogram_quantile(0.99, rate(epp_endpoint_selection_duration_seconds_bucket[1m])) * 1000` | same | ms |

**Important**: Latency queries must be scoped to the benchmark namespace using label
filters. Global cluster-wide queries return aggregate latency across all workloads,
not just the benchmark. The scoping filter is:
```
{envoy_cluster_name=~".*llm-d-sim.*", namespace="<benchmark-namespace>"}
```

### Resource Metrics (from Prometheus)

| Metric | Query | Unit |
|---|---|---|
| Gateway CPU | `rate(container_cpu_usage_seconds_total{pod=~"kgateway.*",namespace="..."}[1m]) * 1000` | millicores |
| Gateway Memory | `container_memory_working_set_bytes{pod=~"kgateway.*",namespace="..."} / 1024 / 1024` | MB |

### Streaming Metrics (direct measurement)

| Metric | Source | Unit |
|---|---|---|
| TTFT | Go SSE client: `time.Now().Sub(requestSentAt).Milliseconds()` on first `data:` chunk | ms |
| ITL | Go SSE client: mean of `chunkTimes[i] - chunkTimes[i-1]` across all chunks | µs |

### Derived Metrics (computed by the runner)

| Metric | Formula | Meaning |
|---|---|---|
| `GatewayOverheadMs` | `scenarioP99 - baselineP99` | The gateway tax |
| `ErrorRateDelta` | `currentErrorRate - baselineErrorRate` | Regression in reliability |
| `FairnessDeviation` | `abs(actualTierPct - expectedTierPct)` per tier | EPP routing quality |

---

## 7. The EPP — How It Works and Why It's Hard to Benchmark

The Endpoint Picker is a gRPC service that kgateway calls for every inference request.
Its job is to select the single best backend pod from the InferencePool.

### Decision Inputs

The EPP scores each pod in the pool using metrics scraped from Prometheus on a
configurable interval (default: 15 seconds):

1. **KV cache utilization %**: The fraction of the KV cache that is occupied.
   High utilization means the pod is working hard on existing requests and should
   receive fewer new ones.
2. **Queue depth**: How many requests are waiting to be processed. A non-zero
   queue means the pod is already saturated.
3. **Active request count**: How many requests the pod is currently generating
   tokens for. This is the real-time saturation indicator.
4. **LoRA adapter loaded**: Whether the requested LoRA adapter is already loaded
   in GPU memory. Routing to a pod that already has the adapter avoids a cold-load
   penalty of 200–500 ms.

### Priority and Load Shedding

The EPP also enforces request priority. Requests are tagged with one of three levels:
- **Critical**: Always routed, even if all pods are saturated.
- **Standard**: Routed unless the pool utilization exceeds a configurable threshold.
- **Sheddable**: Returned a 429 when the pool is under heavy load.

This means that during an overload event, an EPP-aware gateway degrades gracefully
(shedding low-priority traffic) instead of piling up a queue that causes P99 to explode.
Your benchmark **cannot observe this behavior** unless the S5 scenario runs at a RPS
that exceeds total pool capacity and you inspect the `LoadShedCount` metric.

### Why Stale Metrics Are the EPP's Achilles Heel

The EPP uses a Prometheus snapshot taken up to 15 seconds ago. Under a burst:

1. At T=0: All pods are idle, metrics show 0% utilization.
2. At T=1: 500 RPS arrives. EPP sees stale metrics, routes uniformly.
3. At T=5: Pod A's KV cache is full. EPP still sends requests to pod A.
4. At T=15: New Prometheus scrape. EPP finally sees pod A is full.
5. At T=16: Herd behavior — all new requests go to pods B and C, which now fill up.

This creates a sawtooth utilization pattern rather than the smooth distribution you want.
The benchmark can surface this as a high `FairnessDeviation` in S5 combined with a
P99 spike in S3. The `EPPDecisionLatencyMs` metric from Prometheus tells you how long
each individual EPP call took — it does not tell you whether the decision was based on
stale data.

---

## 8. MIG Simulation Strategy

### Why Real MIG Is Not Used

Multi-Instance GPU (MIG) is hardware-locked to NVIDIA datacenter GPUs: A30, A100, H100.
Consumer GPUs (including RTX 3050, 3080, 4090) do not support MIG regardless of driver
version. Kind clusters run on CPU-only nodes. Standard GitHub Actions runners have no
GPU hardware.

### How Simulation Works

The framework runs three `llm-d-inference-sim` Deployments with different Kubernetes
resource limits and environment variable delays:

```
tier-large  → CPU: 4 cores, MEM: 4Gi, RESPONSE_DELAY_MS: 50
tier-medium → CPU: 2 cores, MEM: 2Gi, RESPONSE_DELAY_MS: 100
tier-small  → CPU: 0.5 cores, MEM: 512Mi, RESPONSE_DELAY_MS: 200
```

The response delay simulates the inverse relationship between GPU partition size and
token generation speed: a 3g.40gb partition generates tokens faster than a 1g.10gb
partition.

### What the Simulation Does and Does Not Capture

**Does capture:**
- EPP routing decisions based on queue depth and active request count
- Traffic distribution proportionality (does the EPP send more to the faster backend?)
- Saturation behavior when the small tier fills up
- Cold-start vs warm-start routing (all tiers start cold in this simulation)

**Does not capture:**
- PCIe bandwidth contention between partitions (requires real hardware)
- L3 cache sharing effects
- Memory bandwidth saturation at the GPU die level
- NVLINK topology effects on multi-GPU nodes

### Upgrading to Real MIG Hardware

When running on a cluster with A100/H100 nodes:

1. Add node labels: `kubectl label nodes <node> nvidia.com/mig.config=all-1g.10gb`
2. Update tier manifests to request MIG slices:
   ```yaml
   resources:
     limits:
       nvidia.com/mig-3g.40gb: 1
   ```
3. Add `mig.uuid` label to pod spec for Prometheus attribution
4. Remove `RESPONSE_DELAY_MS` environment variable — actual GPU latency replaces it
5. Add DCGM Exporter to the monitoring stack for GPU-native metrics

---

## 9. Regression Detection

### How It Works

After every run, `metrics.CheckRegression` compares the current result against the
stored `baseline.json`:

```go
deltaPct := ((current.P99LatencyMs - baseline.P99LatencyMs) / baseline.P99LatencyMs) * 100
exceeded := deltaPct > thresholdPct
```

A second check on ErrorRate is also performed:
```go
errorRateExceeded := (current.ErrorRate - baseline.ErrorRate) > 1.0  // 1 percentage point
```

If either check exceeds the threshold, the run exits with code 1 and CI fails.

### Setting the Right Threshold

The default threshold is 20%. This is intentionally permissive because:
- Kind clusters on CI runners have variable CPU scheduling, adding ±5% noise to P99
- Container image pull time can inflate the first warm-up run by up to 10%
- The simulator's artificial delay is not perfectly stable under CPU throttling

For production hardware with stable GPU-backed backends, reduce to 5–10%.

### Updating the Baseline

When you intentionally improve performance (e.g., a new EPP algorithm that reduces
decision latency), update the baseline by running:

```bash
go run ./benchmarking/cmd/runner \
  --scenario inference-routing \
  --output results/ \
  --data-plane envoy
```

The runner automatically overwrites `results/baseline.json` when the baseline scenario
completes. Commit the updated file with a message explaining the improvement.

### What Does NOT Trigger Regression

- P50 or P95 increases (only P99 is compared)
- CPU or memory increases (resource overhead is tracked but not regression-gated)
- TTFT increases (streaming metrics are not regression-gated in the current design)
- EPP decision latency increases

Adding these as additional regression gates is tracked in §13.

---

## 10. Stub Mode

`--stub` enables running the full orchestration pipeline — scenario loading, report
generation, regression checks — without a live Kubernetes cluster.

### What Stub Mode Does

- Skips `k8s.NewK8sClient` and `metrics.NewPrometheusClient` initialization
- Returns `syntheticResults()` immediately for every scenario
- Synthetic values are based on realistic baseline.json numbers (P99=12.4ms, CPU=120m)
- All downstream logic runs normally: `computeGatewayOverhead`, regression check,
  HTML report generation, JSON file writing

### When to Use Stub Mode

- Validating report.html formatting and CSS changes
- Testing regression logic when you change the threshold calculation
- Running in a CI environment that does not have a Kind cluster available
- Development iteration on `main.go` without waiting for cluster setup

### When Not to Use Stub Mode

- Any performance measurement or comparison
- Before submitting benchmark results to maintainers
- When debugging real failures (stub hides all orchestration logic)

### Identifying Stub Results

Every stub result has a console prefix `[STUB]` and the HTML report includes a
banner warning: `⚠️ Results are synthetic — generated in stub mode`.
The JSON result files do not currently have a stub marker. This is a known gap.

---

## 11. Known Limitations

### L1 — EPP Metric Staleness Not Measured Directly

The framework measures whether traffic distribution is correct (S5 fairness) but does
not directly measure how stale the EPP's metric snapshot is at decision time. Adding a
Prometheus metric `epp_metric_snapshot_age_seconds` to the EPP would allow this.
Until then, the proxy is the `EPPDecisionLatencyMs` spike pattern under burst load.

### L2 — Single Namespace Only

All resources (simulator pods, gateway, inference-perf job) deploy into a single
namespace configured by `--namespace`. Multi-tenant scenarios (two teams sharing one
gateway) cannot be tested without forking the orchestrator.

### L3 — No Concurrent Scenario Support

Scenarios run sequentially. There is no way to measure gateway behavior when multiple
model types are being served simultaneously (e.g., a bursty Llama-3 workload and a
steady CodeLlama workload sharing the same pool).

### L4 — inference-perf Helm Chart Is a Local Copy

The `helm/inference-perf/` chart is adapted from the GIE project's chart because no
stable OCI registry URL exists yet. When the upstream chart is published, replace the
local copy with an OCI reference and remove `helm/inference-perf/` from the repo.

### L5 — Streaming TTFT Is Single-Request

`MeasureStreamingMetrics` makes one SSE request. One sample is not statistically
meaningful. The p99 TTFT across 50 requests would be more representative.
`MeasureStreamingMetricsSampled` exists but is not yet wired into the default flow.

### L6 — No Long-Running Stability Test

There is no scenario that runs for hours to detect memory leaks in the gateway process
or KV cache fragmentation in the simulator. This is out of scope for a GSoC project
but is the most important test for a production deployment.

### L7 — HTML Report Has No Time-Series View

The HTML report shows only the current run. There is no trend view showing P99 over
multiple runs. Implementing this requires reading all JSON files from `results/` and
rendering a chart. Tracked in §13.

### L8 — agentgateway Metric Names Are Not Verified

The agentgateway Prometheus metric names in `buildGatewayQueries` are based on the
Rust codebase as of early 2026. If agentgateway changes its metric names in a later
release, the agentgateway data plane run will return zero metrics silently. Verify
against the agentgateway `/metrics` endpoint before comparing data planes.

---

## 12. Operational Runbook

### Quick Start (Local Kind Cluster)

```bash
# 1. Bootstrap the cluster
./benchmarking/scripts/setup-kind.sh

# 2. Run all scenarios against the Envoy data plane
go run ./benchmarking/cmd/runner \
  --scenario all \
  --data-plane envoy \
  --output benchmarking/results/ \
  --threshold 20

# 3. Open the HTML report
open benchmarking/results/report.html
```

### Run a Single Scenario

```bash
go run ./benchmarking/cmd/runner \
  --scenario streaming \
  --data-plane envoy \
  --namespace default
```

### Compare Envoy vs agentgateway

```bash
# Run 1: Envoy
go run ./benchmarking/cmd/runner --data-plane envoy --output results/envoy/

# Run 2: agentgateway
go run ./benchmarking/cmd/runner --data-plane agentgateway --output results/rust/

# Both result sets are included in a single HTML report if --output points
# at the same directory. Each JSON file is stamped with the DataPlane field.
```

### Validate the Reporting Pipeline Without a Cluster

```bash
go run ./benchmarking/cmd/runner --stub --output /tmp/bench-test/
open /tmp/bench-test/report.html
```

### Update the Regression Baseline

```bash
go run ./benchmarking/cmd/runner \
  --scenario inference-routing \
  --output benchmarking/results/

# baseline.json is auto-updated when the baseline scenario runs.
# If you ran inference-routing only:
cp benchmarking/results/$(ls -t benchmarking/results/ | head -1) benchmarking/results/baseline.json
git add benchmarking/results/baseline.json
git commit -m "perf: update baseline after EPP scheduling improvement"
```

### Debugging a Job Timeout

When `WaitForJobComplete` times out:

```bash
# Check Job status
kubectl get jobs -n default

# Get Job pod logs
kubectl logs -n default -l job-name=inference-perf --tail=100

# Check if the simulator Service resolves
kubectl run -it --rm debug --image=busybox --restart=Never -- \
  wget -O- http://llm-d-sim-large.default.svc.cluster.local:8000/health
```

### Debugging the InferencePool matchLabels Error

If you see `matchLabels: expected string, got map`:

```bash
# Check which version of the CRD is installed
kubectl get crd inferencepool.inference.networking.x-k8s.io -o yaml | grep version

# If v1alpha2 is missing, install experimental Gateway API CRDs
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/experimental-install.yaml

# Re-run — applyInferenceManifestWithFallback will retry with the v1 manifest
```

---

## 13. Future Work

The following improvements are tracked but out of scope for the initial GSoC implementation.

### F1 — TTFT p99 Across Multiple Samples

Wire `MeasureStreamingMetricsSampled(url, model, n=10)` into the streaming scenario
and report `TTFTp99Ms` in the Results struct and HTML report.

### F2 — Streaming TTFT Regression Gate

Add `TTFTp99Ms` as a second regression check in `CheckRegression`. Threshold should
be set separately from P99 latency (suggested default: 30% increase).

### F3 — Time-Series Report View

Read all `results/*.json` files sorted by timestamp and render a P99 trend chart using
Chart.js in the HTML report. This lets maintainers see whether performance is trending
better or worse across PRs.

### F4 — FinOps Cost-Per-Token Metrics

Integrate the OpenTelemetry GenAI metrics proposed in the OpenCost feature request:
- `llm_tokens_emitted_total` counter from the simulator
- Derive `cost_per_million_tokens = (GPU_cost_per_hour / 3600) / (tokens_per_second)`
- Add `CostPerMillionTokens` to the Results struct and HTML report

This connects gateway routing efficiency (better EPP decisions → higher GPU utilization)
to dollar cost, making the framework useful for FinOps teams as well as platform engineers.

### F5 — Load Shedding Validation

Add a new scenario S6 that deliberately sends traffic at 150% of pool capacity to
trigger Sheddable-priority load shedding. Validate:
- The correct percentage of requests receive 429
- Critical-priority requests are not shed
- P99 for Critical requests remains stable during the overload

### F6 — Long-Running Stability Scenario

A 4-hour run at 50% capacity testing for memory leaks in the gateway process and
KV cache fragmentation in the simulator. This requires a persistent CI environment,
not a Kind cluster.

### F7 — Real MIG Validation on Cloud Hardware

When GKE or AWS EKS GPU nodes are available in CI, add a workflow that:
- Provisions a node pool with A100 MIG partitions
- Runs S5 with real `nvidia.com/mig-*` resource requests
- Compares EPP fairness results against the CPU-simulated baseline

### F8 — Multi-Tenant Scenario

Two namespaces sharing one gateway, each running inference-perf at different RPS.
Validates that the EPP correctly prioritizes Critical-tier traffic from one tenant
over Standard-tier traffic from another during an overload event.