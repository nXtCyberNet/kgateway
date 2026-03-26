#!/usr/bin/env bash
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

kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/experimental-install.yaml

helm upgrade -i kgateway oci://cr.kgateway.dev/kgateway-dev/charts/kgateway --namespace kgateway-system --create-namespace --set gateway.aiExtension.enabled=true

kubectl wait --namespace kgateway-system --for=condition=ready pod --all --timeout=120s

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm upgrade -i prometheus prometheus-community/prometheus --namespace monitoring --create-namespace

echo "Cluster kgateway-bench ready."
