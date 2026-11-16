# VHS Tape for kube-tollgate FinOps Admission Controller Demo
Output assets/demo.gif

Set FontSize 14
Set Width 1200
Set Height 680
Set Theme "Catppuccin Mocha"
Set Padding 20
Set TypingSpeed 35ms

# Setup prompt appearance and clean terminal
Hide
Type "export PS1='\[\033[01;32m\]dev@k8s\[\033[00m\]:\[\033[01;34m\]~/kube-tollgate\[\033[00m\]$ '"
Enter
Type "kubectl delete namespace team-billing team-strict --ignore-not-found=true --wait=false"
Enter
Sleep 1s
Type "clear"
Enter
Show

# 1. Apply FinOps Policy
Type "# 1. Apply FinOps TollgatePolicy for 'team-billing' namespace"
Enter
Sleep 800ms
Type "kubectl apply -f examples/00-policy.yaml"
Enter
Sleep 2s

# 2. Deploy unbounded pod (demonstrate mutating webhook)
Type "# 2. Deploy unbounded Pod (missing CPU/RAM requests and cost tags)"
Enter
Sleep 800ms
Type "kubectl apply -f examples/02-unbounded-pod-mutated.yaml"
Enter
Sleep 2s

Type "kubectl get pod unbounded-worker-mutated -n team-billing -o json | jq '{labels: .metadata.labels, resources: .spec.containers[0].resources}'"
Enter
Sleep 3s

# 3. Deploy compliant production pod
Type "# 3. Deploy compliant production Pod (satisfies all FinOps limits)"
Enter
Sleep 800ms
Type "kubectl apply -f examples/01-valid-pod.yaml"
Enter
Sleep 2.5s

# 4. Attempt to breach namespace budget ceiling (demonstrate validating webhook)
Type "# 4. Attempt to deploy oversized Pod (breaching namespace budget ceiling)"
Enter
Sleep 800ms
Type "kubectl apply -f examples/03-budget-exceeded-rejected.yaml"
Enter
Sleep 4.5s

# 5. Query live Prometheus telemetry
Type "# 5. Query live admission metrics from Prometheus exporter (:8080)"
Enter
Sleep 800ms
Type "POD=$(kubectl get pod -n tollgate-system -l app.kubernetes.io/name=kube-tollgate -o jsonpath='{.items[0].metadata.name}')"
Enter
Type "kubectl get --raw /api/v1/namespaces/tollgate-system/pods/$POD:8080/proxy/metrics | grep -E '^tollgate_(rejections_total|mutations_applied_total)'"
Enter
Sleep 4s
