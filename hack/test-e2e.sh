#!/usr/bin/env bash
# test-e2e.sh - End-to-end integration test runner for kube-tollgate admission scenarios
set -euo pipefail

echo "==> [e2e] Running kube-tollgate end-to-end admission tests"

# 1. Apply baseline policy and namespace
echo "==> [e2e] Applying 00-policy.yaml..."
kubectl apply -f examples/00-policy.yaml

# Wait briefly for policy controller sync
sleep 2

# 2. Test Scenario 01: Valid Pod (should be admitted)
echo "==> [e2e] Testing Scenario 01: Valid Pod admission..."
kubectl delete pod valid-billing-worker -n team-billing --wait=false --ignore-not-found=true
kubectl apply -f examples/01-valid-pod.yaml
echo "==> [e2e] Scenario 01 PASSED: Valid pod was successfully admitted."

# 3. Test Scenario 02: Unbounded Pod (should be mutated)
echo "==> [e2e] Testing Scenario 02: Unbounded Pod mutation..."
kubectl delete pod unbounded-worker-mutated -n team-billing --wait=false --ignore-not-found=true
kubectl apply -f examples/02-unbounded-pod-mutated.yaml

# Verify injected defaults
CPU_REQ=$(kubectl get pod unbounded-worker-mutated -n team-billing -o jsonpath='{.spec.containers[0].resources.requests.cpu}')
COST_CENTER=$(kubectl get pod unbounded-worker-mutated -n team-billing -o jsonpath='{.metadata.labels.cost-center}')

if [[ "${CPU_REQ}" != "100m" ]]; then
  echo "==> [e2e] ERROR: Expected CPU request 100m, got ${CPU_REQ}"
  exit 1
fi

if [[ "${COST_CENTER}" != "CC-1001" ]]; then
  echo "==> [e2e] ERROR: Expected cost-center label CC-1001, got ${COST_CENTER}"
  exit 1
fi
echo "==> [e2e] Scenario 02 PASSED: Unbounded pod was successfully mutated with defaults."

# 4. Test Scenario 03: Budget Exceeded (should be rejected with 403 BUDGET_EXCEEDED)
echo "==> [e2e] Testing Scenario 03: Budget Exceeded rejection..."
OUT_SCENARIO_03=$(kubectl apply -f examples/03-budget-exceeded-rejected.yaml 2>&1 || true)
if echo "${OUT_SCENARIO_03}" | grep -q "BUDGET_EXCEEDED"; then
  echo "==> [e2e] Scenario 03 PASSED: Oversized pod was rejected with BUDGET_EXCEEDED."
else
  echo "==> [e2e] ERROR: Expected rejection with BUDGET_EXCEEDED, but request was admitted or failed with different error: ${OUT_SCENARIO_03}"
  exit 1
fi

# 5. Test Scenario 04: Missing/Invalid Labels (should be rejected with 403 MISSING_OR_INVALID_LABEL)
echo "==> [e2e] Testing Scenario 04: Missing/Invalid Labels rejection..."
OUT_SCENARIO_04=$(kubectl apply -f examples/04-missing-labels-rejected.yaml 2>&1 || true)
if echo "${OUT_SCENARIO_04}" | grep -q "MISSING_OR_INVALID_LABEL"; then
  echo "==> [e2e] Scenario 04 PASSED: Noncompliant pod was rejected with MISSING_OR_INVALID_LABEL."
else
  echo "==> [e2e] ERROR: Expected rejection with MISSING_OR_INVALID_LABEL, but request was admitted or failed with different error: ${OUT_SCENARIO_04}"
  exit 1
fi

# Cleanup test pods
kubectl delete pod valid-billing-worker unbounded-worker-mutated -n team-billing --wait=false --ignore-not-found=true
kubectl delete namespace team-strict --ignore-not-found=true

echo "==> [e2e] All end-to-end integration tests PASSED successfully!"
