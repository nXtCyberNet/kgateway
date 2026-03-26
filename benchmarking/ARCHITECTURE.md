# Architecture

The benchmarking suite consists of:
1. **Orchestrator** (Go): Deploys manifests, waits for pods, handles Prometheus queries.
2. **Load Generator**: inference-perf sent as a Job.
3. **Backends**: llm-d-inference-sim simulators with differing artificial delays and limits.
4. **Gateway**: kgateway proxy deployed via standard Helm logic.

P99 Latency and Error rates are sourced from both the client tools (for true metrics) and Prometheus (for gateway metrics).