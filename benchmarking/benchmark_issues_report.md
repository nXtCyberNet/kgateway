# kgateway Benchmark Inference Issues Report

This document details the critical failures, configuration drifts, and execution blockers we encountered while testing the `kgateway` inference routing benchmarking suite (`go run ./cmd/runner`). 

These issues span across Kubernetes CRD validations, container image mismatches, and mathematical data generator exceptions.

---

## 1. Simulator Pod `CrashLoopBackOff` (Image Mismatch)

### Description
The kgateway simulator backends (`app=llm-d-sim` across `tier-large`, `tier-medium`, `tier-small`) were crashing immediately upon startup, remaining stuck in a `CrashLoopBackOff` loop.
* **Error Trigger:** `error during container init: exec: "cmd": executable file not found in $PATH`
* **Root Cause:** The `kgateway` README states that `quay.io/inference-perf/inference-perf:v0.4.0` should be used to bypass intermittent `403 Forbidden` GHCR pull errors. However, this fix applies **exclusively to the load generator job**. Applying it to the simulator tiers causes a crash because the `inference-perf` container is purely a Python load generator (and lacks the `cmd` binary expected by the simulator `args`). 
* **Resolution/State:** Reverted backend simulator pods strictly back to `ghcr.io/llm-d/llm-d-inference-sim:latest`.

## 2. `InferencePool` CRD Schema Validation Failure

### Description
When running scenarios `epp-fairness`, `inference-routing`, or `streaming`, the application of manifest files failed with API server admission errors.
* **Error Trigger:** 
  > `InferencePool "llama-pool" is invalid: [spec.selector.matchLabels: Invalid value: "object": spec.selector.matchLabels in body must be of type string: "object", spec.targetPortNumber: Required value]`
* **Root Cause:** Upstream changes to the `v1alpha2` Gateway API Inference Extension schema severely tightened validation. It flattened the endpoint `selector` configuration so it no longer accepts traditional Kubernetes nested `matchLabels` objects. Concurrently, it made the `targetPortNumber` property strictly mandatory.
* **Resolution/State:** Modified `manifests/inference-pool.yaml` to enforce a flat `selector: app: llm-d-sim` layout and explicitly provide `targetPortNumber: 8000`.

## 3. 8-Minute Job Timeout (ShareGPT Network Bottleneck)

### Description
The benchmark runner would freeze unconditionally for exactly 8 minutes on scenarios like `baseline` and `header-routing`, before failing via timeout. Concurrent `etcdserver: request timed out` anomalies occasionally occurred.
* **Error Trigger:** `timed out after 8m0s waiting for job inference-perf to complete`
* **Root Cause:** Configuring the benchmark parameters to use `dataType: shareGPT` (for realistic tokens) forces the `inference-perf` container to pull the `ShareGPT_V3` dataset JSON directly from HuggingFace AWS endpoints on every execution. Downloading and parsing this gigabyte-sized dataset consistently choked the CI environment, starving Kubelet of resources and violently violating the benchmark timeout limit.
* **Resolution/State:** Switched the benchmark methodology from downloading real dataset objects to generating mathematical distributions (`dataType: synthetic`).

## 4. `inference-perf` Python Crash on Synthetic Datasets

### Description
After circumventing the `ShareGPT` network bottleneck by using `dataType: synthetic`, the Pod immediately terminated on start and locked up without gathering metrics.
* **Error Trigger:** `Exception: synthetic data generator requires 'input_distribution' to be configured if no trace config is provided`
* **Root Cause:** The `inference-perf` Python tool strictly validates runtime configuration objects natively. If it observes the `dataType` is `synthetic`, it mathematically requires statistical limits (a `min`, `max`, `mean`, and `std_dev` for token output/input distribution) to synthesize conversational behavior. These tokens were missing in the Helm deployment ConfigMap config (`config.yml`). 
* **Resolution/State:** Edited `helm/inference-perf/templates/configmap.yaml` to dynamically inject the mathematically required `input_distribution` and `output_distribution` properties via Helm variables whenever `synthetic` requests are executed.

## 5. API Eventual Consistency ("Job Not Found" Crash)

### Description
The benchmark runner correctly submitted the load generator execution via `helm install`, but instantly failed.
* **Error Trigger:** `failed to get job inference-perf: jobs.batch "inference-perf" not found`
* **Root Cause:** Eventual consistency timing. The `executeScenario` task ran a synchronous Kubernetes API query (`k8sClient.WaitForJobComplete()`) just milliseconds after executing the asynchronous helm upgrade command. Under moderate local WSL or CI node load, etcd takes a split second to expose newly submitted `Job` elements to the client informer; meaning the `.Get()` request was transiently met with HTTP 404. Since `wait.go` lacked retry logic, the benchmark crashed cleanly on a perfectly transient event.
* **Resolution/State:** Altered `pkg/k8s/client.go` to explicitly trap and `time.Sleep()` over `k8s.io/apimachinery/pkg/api/errors.IsNotFound` exceptions, successfully allowing the benchmark to wait for API materialization.
