#!/usr/bin/env bash
set -euo pipefail
umask 077

# Run with nix develop -c bash deploy/kubernetes/test/run.sh. Every kubectl and
# helm invocation uses the disposable cluster's explicit kubeconfig.
repo=$(cd "$(dirname "$0")/../../.." && pwd)
cluster=${THREAVIA_TEST_CLUSTER:-threavia-execution-test}
test_dir=${THREAVIA_TEST_DIR:?Set THREAVIA_TEST_DIR to a private directory for test artifacts}
mkdir -p "$test_dir"
test_dir=$(cd "$test_dir" && pwd)
case "$cluster" in threavia-*) ;; *) echo 'Test cluster names must start with threavia-' >&2; exit 1;; esac
cleanup() {
  if [ "${THREAVIA_TEST_KEEP_CLUSTER:-0}" != 1 ]; then k3d cluster delete "$cluster"; fi
}
trap cleanup EXIT
if ! k3d cluster list -o json | jq -e --arg name "$cluster" '.[] | select(.name == $name)' >/dev/null; then
  k3d cluster create "$cluster" --servers 1 --agents 0 --no-lb --kubeconfig-update-default=false --kubeconfig-switch-context=false --wait --timeout 120s
fi
k3d kubeconfig get "$cluster" > "$test_dir/kubeconfig"
k() { kubectl --kubeconfig "$test_dir/kubeconfig" -n execution-test "$@"; }

if [ "${THREAVIA_TEST_SKIP_BUILD:-0}" != 1 ]; then
  for cmd in threavia-executor threavia-backend-codex threavia-backend-claude; do
    CGO_ENABLED=0 go build -trimpath -o "$test_dir/$cmd" "$repo/cmd/$cmd"
  done
  CGO_ENABLED=0 go build -o "$test_dir/fixture-provider" "$repo/internal/execution/integration/testdata/provider"
  CGO_ENABLED=0 go test -c -tags kubernetes_integration -o "$test_dir/execution-cluster-tests" "$repo/internal/execution/integration"
  mkdir -p "$test_dir/image-context"
  for cmd in threavia-executor threavia-backend-codex threavia-backend-claude execution-cluster-tests fixture-provider; do cp "$test_dir/$cmd" "$test_dir/image-context/$cmd"; done
  docker build -f "$repo/deploy/kubernetes/test/Dockerfile" -t threavia-execution-test:local "$test_dir/image-context"
fi
k3d image import -c "$cluster" threavia-execution-test:local
k create namespace execution-test --dry-run=client -o yaml | k apply -f -
k label namespace execution-test pod-security.kubernetes.io/enforce=restricted --overwrite
for claim in workspaces shared claude-native codex-native; do
  cat <<YAML | k apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: $claim}
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests: {storage: 1Gi}
YAML
done
for provider in claude codex; do
  if [ ! -f "$test_dir/$provider-token" ]; then openssl rand -hex 32 > "$test_dir/$provider-token"; fi
  k create secret generic "$provider-executor-client" --from-file="token=$test_dir/$provider-token" --dry-run=client -o yaml | k apply -f -
done
k apply -f "$repo/deploy/kubernetes/test/cluster.yaml"
k wait --for=jsonpath='{.status.phase}'=Succeeded pod/prepare --timeout=90s
k wait --for=condition=Ready pod/echo --timeout=90s
helm upgrade --install test-executor "$repo/deploy/helm/threavia-executor" --kubeconfig "$test_dir/kubeconfig" --namespace execution-test -f "$repo/deploy/kubernetes/test/values.yaml" --wait --timeout 3m
# The fixture tag is deliberately local. Recreate the controller so repeat
# runs exercise the imported build, not an older container with the same tag.
k rollout restart statefulset/test-executor
k rollout status statefulset/test-executor --timeout=180s
k delete job execution-integration --ignore-not-found --wait=true
k apply -f "$repo/deploy/kubernetes/test/job.yaml"
completed=0
for attempt in $(seq 1 120); do
  status=$(k get job execution-integration -o json)
  if printf '%s' "$status" | jq -e '.status.conditions[]? | select(.type == "Complete" and .status == "True")' >/dev/null; then completed=1; break; fi
  if printf '%s' "$status" | jq -e '.status.conditions[]? | select((.type == "Failed" or .type == "FailureTarget") and .status == "True")' >/dev/null; then break; fi
  sleep 5
done
if [ "$completed" != 1 ]; then
  k logs job/execution-integration --all-containers
  k logs statefulset/test-executor --tail=100
  k get pods -o wide
  exit 1
fi
k logs job/execution-integration
# No execution pod may outlive the completed suite (including descendants).
if [ "$(k get pods -l app.kubernetes.io/name=threavia-execution -o name | wc -l)" -ne 0 ]; then k wait --for=delete pod -l app.kubernetes.io/name=threavia-execution --timeout=45s; fi
if [ "$(k get secrets -l execution.threavia.io/controller=test-executor -o name | wc -l)" -ne 0 ]; then k wait --for=delete secret -l execution.threavia.io/controller=test-executor --timeout=45s; fi
# Verify CNI enforcement independently of the Monitor helper's seccomp filter.
# Both probes run an ordinary Go TCP dial with the runtime's standard seccomp.
echo_ip=$(k get service echo -o jsonpath='{.spec.clusterIP}')
cat <<YAML | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: audit-deny-egress}
spec:
  podSelector: {matchLabels: {app: netpol-deny}}
  policyTypes: [Egress]
  egress: []
YAML
for expected in allow deny; do
  k delete pod "netpol-$expected" --ignore-not-found=true --wait=true
  cat <<YAML | k apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: netpol-$expected
  labels: {app: netpol-$expected}
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 20
  automountServiceAccountToken: false
  securityContext: {runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, seccompProfile: {type: RuntimeDefault}}
  containers:
    - name: probe
      image: threavia-execution-test:local
      imagePullPolicy: Never
      command: [bash, -c, 'sleep 2; exec /usr/local/bin/fixture-provider --network-probe $echo_ip:8080 $expected']
      securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
YAML
  if ! k wait --for=jsonpath='{.status.phase}'=Succeeded "pod/netpol-$expected" --timeout=30s; then k logs "netpol-$expected"; exit 1; fi
  probe_log=$(k logs "netpol-$expected")
  printf '%s\n' "$probe_log"
  printf '%s' "$probe_log" | grep -F "network policy probe: $expected" >/dev/null
done
if [ "${THREAVIA_TEST_NATIVE:-0}" = 1 ]; then
  docker build --build-arg TARGETOS=linux --build-arg TARGETARCH=amd64 -f "$repo/deploy/docker/backend-codex.Dockerfile" -t threavia-backend-codex:kubernetes-dev "$repo"
  docker build -f "$repo/deploy/kubernetes/test/native.Dockerfile" -t threavia-codex-smoke:local "$test_dir/image-context"
  k3d image import -c "$cluster" threavia-codex-smoke:local
  k delete pod native-codex-smoke --ignore-not-found=true --wait=true
  k apply -f "$repo/deploy/kubernetes/test/native.yaml"
  if ! k wait --for=jsonpath='{.status.phase}'=Succeeded pod/native-codex-smoke --timeout=90s; then k logs native-codex-smoke; exit 1; fi
  k logs native-codex-smoke
fi
