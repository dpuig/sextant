#!/usr/bin/env bash
# SPIKE (Task 5). Throwaway: replaced by the real tests in Task 9/10, then deleted. Proves or disproves that a generated
# one-context kubeconfig, referenced through KUBECONFIG, keeps kubectl and helm on ONE cluster while the global
# current-context changes, and measures the failure modes that decide the design.
#
# Needs: kind (podman or docker), kubectl, helm, bash, zsh. Creates two kind clusters and removes them on exit
# (KEEP=1 keeps them). Prints PASS/FAIL/INFO; exits non-zero if any FAIL.
set -u
A=sx-spike-a
B=sx-spike-b
WORK=$(mktemp -d)
MAIN=$WORK/main.kubeconfig        # the user's "real" kubeconfig, holding both contexts
BOUND=$WORK/bound/b.kubeconfig    # the generated, minimal kubeconfig for cluster B
fails=0
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }
info() { echo "INFO  $*"; }
check() { # check "description" expected actual
  if [ "$2" = "$3" ]; then pass "$1 (= $3)"; else fail "$1: expected '$2', got '$3'"; fi
}
cleanup() {
  if [ -z "${KEEP:-}" ]; then
    kind delete cluster --name "$A" >/dev/null 2>&1
    kind delete cluster --name "$B" >/dev/null 2>&1
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

for t in kind kubectl helm zsh bash; do command -v "$t" >/dev/null || { echo "missing tool: $t"; exit 2; }; done

echo "== creating two kind clusters (parallel)"
kind create cluster --name "$A" --kubeconfig "$WORK/a.kc" --wait 120s >/dev/null 2>&1 &
kind create cluster --name "$B" --kubeconfig "$WORK/b.kc" --wait 120s >/dev/null 2>&1 &
wait
[ -s "$WORK/a.kc" ] && [ -s "$WORK/b.kc" ] || { echo "cluster creation failed"; exit 2; }
KUBECONFIG="$WORK/a.kc:$WORK/b.kc" kubectl config view --flatten >"$MAIN"
chmod 600 "$MAIN"
kmain() { KUBECONFIG="$MAIN" kubectl "$@"; }
kmain config use-context "kind-$A" >/dev/null

# Something that exists only in B, for kubectl and for helm.
kmain --context "kind-$B" create namespace only-in-b >/dev/null
mkdir -p "$WORK/chart/templates"
printf 'apiVersion: v2\nname: probe\nversion: 0.1.0\n' >"$WORK/chart/Chart.yaml"
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: probe}\n' >"$WORK/chart/templates/cm.yaml"
helm --kubeconfig "$MAIN" --kube-context "kind-$B" install probe "$WORK/chart" -n only-in-b >/dev/null 2>&1

echo "== generate the minimal kubeconfig for cluster B (what Task 9 will produce in TypeScript)"
umask 077
mkdir -p "$WORK/bound" && chmod 700 "$WORK/bound"
kmain config view --minify --flatten --context "kind-$B" >"$BOUND"
chmod 600 "$BOUND"
check "bound kubeconfig has exactly one context" 1 "$(KUBECONFIG=$BOUND kubectl config get-contexts -o name | wc -l | tr -d ' ')"
check "bound kubeconfig current-context" "kind-$B" "$(KUBECONFIG=$BOUND kubectl config current-context)"

echo "== the global context is A; a bound shell must still be on B"
check "global current-context is A" "kind-$A" "$(kmain config current-context)"
check "bound shell current-context is B" "kind-$B" "$(KUBECONFIG=$BOUND kubectl config current-context)"
check "global kubectl talks to A" "node/$A-control-plane" "$(kmain get nodes -o name)"
check "bound kubectl talks to B" "node/$B-control-plane" "$(KUBECONFIG=$BOUND kubectl get nodes -o name)"
check "namespace only-in-b is visible to the bound shell" "namespace/only-in-b" "$(KUBECONFIG=$BOUND kubectl get ns only-in-b -o name)"
check "...and invisible to the global one" "" "$(kmain get ns only-in-b -o name 2>/dev/null)"
check "helm in the bound shell sees B's release" "probe" "$(KUBECONFIG=$BOUND helm list -n only-in-b -q 2>/dev/null)"
check "helm with the global config sees nothing" "" "$(KUBECONFIG=$MAIN helm list -n only-in-b -q 2>/dev/null)"

echo "== change the global context to B then back to A: the bound shell never moves"
kmain config use-context "kind-$B" >/dev/null; kmain config use-context "kind-$A" >/dev/null
check "bound shell still B after global churn" "kind-$B" "$(KUBECONFIG=$BOUND kubectl config current-context)"

echo "== other contexts are unreachable from the bound shell (safe by default)"
out=$(KUBECONFIG=$BOUND kubectl --context "kind-$A" get ns 2>&1)
case "$out" in *"not found"*|*"does not exist"*) pass "kubectl --context <other> fails inside a bound shell";; *) fail "other context reachable: $out";; esac

echo "== file permissions"
if [ "$(uname)" = "Darwin" ]; then mode() { stat -f '%Lp' "$1"; }; else mode() { stat -c '%a' "$1"; }; fi
check "bound kubeconfig mode" 600 "$(mode "$BOUND")"
check "bound directory mode" 700 "$(mode "$WORK/bound")"

echo "== exec-plugin credentials: nothing secret has to be copied"
cat >"$WORK/exec.kubeconfig" <<'YAML'
apiVersion: v1
kind: Config
current-context: e
contexts: [{name: e, context: {cluster: c, user: u}}]
clusters: [{name: c, cluster: {server: "https://127.0.0.1:1"}}]
users: [{name: u, user: {exec: {apiVersion: client.authentication.k8s.io/v1, command: aws, args: [eks, get-token]}}}]
YAML
KUBECONFIG="$WORK/exec.kubeconfig" kubectl config view --minify --flatten >"$WORK/exec-min.kubeconfig"
if grep -qE 'client-key-data|client-certificate-data|token:' "$WORK/exec-min.kubeconfig"; then fail "exec-plugin minimal kubeconfig contains secret material"; else pass "exec-plugin minimal kubeconfig contains no secret material"; fi
grep -q 'command: aws' "$WORK/exec-min.kubeconfig" && pass "...and keeps the exec stanza" || fail "exec stanza lost"

echo "== inline credentials ARE copied for certificate and token users (the cost of this approach)"
if grep -q 'client-key-data' "$BOUND"; then info "bound file for a client-cert user contains client-key-data: handled by 0600 file in a 0700 per-user directory, deleted on close, on deactivate and swept at start"; fi

echo "== FAILURE MODE: does the user's shell startup file override KUBECONFIG after it is injected?"
H=$WORK/home; mkdir -p "$H/.kube"
echo 'export KUBECONFIG="$HOME/.kube/config"' >"$H/.zshrc"
echo 'export KUBECONFIG="$HOME/.kube/config"' >"$H/.bashrc"
zsh_out=$(env HOME="$H" KUBECONFIG="$BOUND" zsh -i -c 'echo $KUBECONFIG' 2>/dev/null | tail -1)
bash_out=$(env HOME="$H" KUBECONFIG="$BOUND" bash -i -c 'echo $KUBECONFIG' 2>/dev/null | tail -1)
[ "$zsh_out" = "$BOUND" ] && info "zsh keeps the injected KUBECONFIG" || info "zsh: startup file OVERRIDES the injected KUBECONFIG ($zsh_out)"
[ "$bash_out" = "$BOUND" ] && info "bash keeps the injected KUBECONFIG" || info "bash: startup file OVERRIDES the injected KUBECONFIG ($bash_out)"
# The mitigation under test: set it again after startup (what a terminal's sendText does).
zsh_fix=$(env HOME="$H" KUBECONFIG="$BOUND" zsh -i -c 'export KUBECONFIG="'"$BOUND"'"; echo $KUBECONFIG' 2>/dev/null | tail -1)
check "re-exporting after startup restores the bound path (zsh)" "$BOUND" "$zsh_fix"

echo
if [ "$fails" -eq 0 ]; then echo "SPIKE RESULT: all checks passed"; else echo "SPIKE RESULT: $fails check(s) FAILED"; exit 1; fi
