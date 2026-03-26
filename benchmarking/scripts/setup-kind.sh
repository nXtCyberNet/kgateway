#!/usr/bin/env bash
# scripts/setup-kind.sh
# Setup a local Kind cluster with kgateway + Prometheus for benchmarking
set -euo pipefail

kind create cluster --name kgateway-bench --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 8080
    hostPort: 8080
    protocol: TCP
EOF

echo "✅ Kind cluster created. Installing Gateway API CRDs..."

# Install experimental Gateway API CRDs (required for InferencePool, InferenceModel, etc.)
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/experimental-install.yaml

echo "✅ Installing kgateway with AI Extension enabled..."

helm upgrade -i kgateway oci://cr.kgateway.dev/kgateway-dev/charts/kgateway \
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

kubectl wait --namespace monitoring \
  --for=condition=ready pod \
  --all \
  --timeout=120s

echo "📊 Cluster status:"
echo "------------------------------------------------------------"
kubectl get pods -A -o wide
echo "------------------------------------------------------------"
kubectl get svc -A
echo "------------------------------------------------------------"
echo "✅ Cluster 'kgateway-bench' is ready for benchmarking!"
