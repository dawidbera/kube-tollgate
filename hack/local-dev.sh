#!/usr/bin/env bash
# local-dev.sh - Automated local development harness for kube-tollgate using k3d
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-kube-tollgate-dev}"
IMAGE_NAME="${IMG:-kube-tollgate:dev}"

echo "==> [kube-tollgate] Initializing local development environment"

# 1. Provision single-node k3d cluster
if ! k3d cluster list | grep -q "${CLUSTER_NAME}"; then
  echo "==> [k3d] Creating cluster ${CLUSTER_NAME}..."
  k3d cluster create "${CLUSTER_NAME}" \
    --agents 1 \
    --port "9443:9443@loadbalancer" \
    --port "8080:8080@loadbalancer"
else
  echo "==> [k3d] Cluster ${CLUSTER_NAME} is already running."
fi

# Ensure kubectl context points to the local k3d cluster
kubectl config use-context "k3d-${CLUSTER_NAME}"

# 2. Build local container image
echo "==> [docker] Building image ${IMAGE_NAME}..."
docker build -t "${IMAGE_NAME}" .

# 3. Import image into k3d cluster
echo "==> [k3d] Importing image into ${CLUSTER_NAME}..."
k3d image import "${IMAGE_NAME}" -c "${CLUSTER_NAME}"

# 4. Deploy manifests
echo "==> [kubectl] Deploying CRDs, RBAC, Webhooks, and Controller Manager..."
kubectl create namespace tollgate-system --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f config/crd/bases
kubectl apply -f config/rbac
kubectl apply -f config/webhook
kubectl apply -f config/manager

# 5. Wait for deployment readiness
echo "==> [kubectl] Waiting for tollgate-controller-manager to be ready..."
kubectl rollout status deployment/tollgate-controller-manager -n tollgate-system --timeout=120s

echo "==> [kube-tollgate] Local development environment is ready!"
kubectl get pods -n tollgate-system
