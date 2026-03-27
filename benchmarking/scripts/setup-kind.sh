#!/usr/bin/env bash
# scripts/setup-kind.sh
# Setup a local Kind cluster with kgateway + Prometheus for benchmarking
set -euo pipefail

# OCI charts require an explicit tag/version. Override when needed, e.g.:
# KGATEWAY_VERSION=v2.3.0 ./setup-kind.sh
KGATEWAY_VERSION="${KGATEWAY_VERSION:-v2.2.0-main}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-kgateway-bench}"
KUBE_CONTEXT="kind-${KIND_CLUSTER_NAME}"
# Default to a Kubernetes version used in repo CI/nightly compatibility runs.
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.30.13}"
GATEWAY_API_CRDS_URL="${GATEWAY_API_CRDS_URL:-https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/experimental-install.yaml}"
KUBECTL_RETRIES="${KUBECTL_RETRIES:-20}"
KUBECTL_RETRY_SLEEP_SECONDS="${KUBECTL_RETRY_SLEEP_SECONDS:-10}"
PROMETHEUS_WAIT_TIMEOUT="${PROMETHEUS_WAIT_TIMEOUT:-600s}"

retry() {
  local attempts="$1"
  local sleep_seconds="$2"
  shift 2

  local i
  for ((i = 1; i <= attempts; i++)); do
    if "$@"; then
      return 0
    fi
    echo "Attempt ${i}/${attempts} failed. Retrying in ${sleep_seconds}s..."
    sleep "${sleep_seconds}"
  done

  return 1
}

wait_for_apiserver() {
  echo "⏳ Waiting for Kubernetes API server to become ready..."
  retry 60 5 kubectl --context "${KUBE_CONTEXT}" --request-timeout=20s get --raw='/readyz' >/dev/null
}

wait_for_nodes() {
  echo "⏳ Waiting for Kubernetes nodes to report Ready..."
  retry 30 5 kubectl --context "${KUBE_CONTEXT}" --request-timeout=20s get nodes >/dev/null
  retry 30 5 kubectl --context "${KUBE_CONTEXT}" --request-timeout=20s wait --for=condition=Ready node --all --timeout=30s >/dev/null
}

wait_for_prometheus() {
  echo "⏳ Waiting for Prometheus components to become ready (timeout=${PROMETHEUS_WAIT_TIMEOUT})..."

  # Image pulls in CI/local dev can be flaky; retry rollout checks a few times.
  retry 3 20 kubectl --context "${KUBE_CONTEXT}" -n monitoring rollout status deployment/prometheus-server --timeout="${PROMETHEUS_WAIT_TIMEOUT}"
  retry 3 20 kubectl --context "${KUBE_CONTEXT}" -n monitoring rollout status deployment/prometheus-kube-state-metrics --timeout="${PROMETHEUS_WAIT_TIMEOUT}"
  retry 3 20 kubectl --context "${KUBE_CONTEXT}" -n monitoring rollout status deployment/prometheus-prometheus-pushgateway --timeout="${PROMETHEUS_WAIT_TIMEOUT}"
  retry 3 20 kubectl --context "${KUBE_CONTEXT}" -n monitoring rollout status daemonset/prometheus-prometheus-node-exporter --timeout="${PROMETHEUS_WAIT_TIMEOUT}"
  retry 3 20 kubectl --context "${KUBE_CONTEXT}" -n monitoring rollout status statefulset/prometheus-alertmanager --timeout="${PROMETHEUS_WAIT_TIMEOUT}"
}

kind create cluster --name "${KIND_CLUSTER_NAME}" --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  image: ${KIND_NODE_IMAGE}
  extraPortMappings:
  - containerPort: 8080
    hostPort: 8080
    protocol: TCP
EOF

kubectl config use-context "${KUBE_CONTEXT}" >/dev/null
wait_for_apiserver
wait_for_nodes

echo "✅ Kind cluster created. Installing Gateway API CRDs..."

# Install experimental Gateway API CRDs (required for InferencePool, InferenceModel, etc.)
echo "Downloading Gateway API CRDs from ${GATEWAY_API_CRDS_URL}"
tmp_gateway_crds_file="$(mktemp)"
trap 'rm -f "${tmp_gateway_crds_file}"' EXIT

retry 8 5 curl -fsSL "${GATEWAY_API_CRDS_URL}" -o "${tmp_gateway_crds_file}"
retry "${KUBECTL_RETRIES}" "${KUBECTL_RETRY_SLEEP_SECONDS}" kubectl --context "${KUBE_CONTEXT}" --request-timeout=30s apply -f "${tmp_gateway_crds_file}"

echo "Downloading Gateway API Inference Extension CRDs..."
retry "${KUBECTL_RETRIES}" "${KUBECTL_RETRY_SLEEP_SECONDS}" kubectl --context "${KUBE_CONTEXT}" --request-timeout=30s apply -f https://raw.githubusercontent.com/kubernetes-sigs/gateway-api-inference-extension/main/config/crd/bases/inference.networking.x-k8s.io_inferencepools.yaml
retry "${KUBECTL_RETRIES}" "${KUBECTL_RETRY_SLEEP_SECONDS}" kubectl --context "${KUBE_CONTEXT}" --request-timeout=30s apply -f https://raw.githubusercontent.com/kubernetes-sigs/gateway-api-inference-extension/main/config/crd/bases/inference.networking.x-k8s.io_inferencemodelrewrites.yaml
retry "${KUBECTL_RETRIES}" "${KUBECTL_RETRY_SLEEP_SECONDS}" kubectl --context "${KUBE_CONTEXT}" --request-timeout=30s apply -f https://raw.githubusercontent.com/kubernetes-sigs/gateway-api-inference-extension/main/config/crd/bases/inference.networking.x-k8s.io_inferenceobjectives.yaml

echo "✅ Installing kgateway with AI Extension enabled..."

echo "Using kgateway chart version: ${KGATEWAY_VERSION}"

helm upgrade -i kgateway-crds oci://cr.kgateway.dev/kgateway-dev/charts/kgateway-crds \
  --version "${KGATEWAY_VERSION}" \
  --namespace kgateway-system \
  --create-namespace

helm upgrade -i kgateway oci://cr.kgateway.dev/kgateway-dev/charts/kgateway \
  --version "${KGATEWAY_VERSION}" \
  --namespace kgateway-system \
  --create-namespace \
  --set gateway.aiExtension.enabled=true

kubectl wait --namespace kgateway-system \
  --for=condition=ready pod \
  --all \
  --timeout=180s

echo "✅ Installing Prometheus..."

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade -i prometheus prometheus-community/prometheus \
  --namespace monitoring \
  --create-namespace

if ! wait_for_prometheus; then
  echo "❌ Prometheus did not become ready in time. Debug info:"
  kubectl --context "${KUBE_CONTEXT}" -n monitoring get pods -o wide || true
  kubectl --context "${KUBE_CONTEXT}" -n monitoring describe pod -l app.kubernetes.io/name=prometheus || true
  kubectl --context "${KUBE_CONTEXT}" -n monitoring logs deployment/prometheus-server --tail=200 || true
  exit 1
fi

echo "📊 Cluster status:"
echo "------------------------------------------------------------"
kubectl get pods -A -o wide
echo "------------------------------------------------------------"
kubectl get svc -A
echo "------------------------------------------------------------"
echo "✅ Cluster 'kgateway-bench' is ready for benchmarking!"
