#!/usr/bin/env bash
# SPIKE v2 (E1 bound terminals). Supersedes the "copy a minimal kubeconfig" spike behind ADR 0003.
# The design system's mechanism: put a tiny file holding ONLY `current-context: <name>` in FRONT of the user's own
# KUBECONFIG list. kubectl/client-go take current-context from the first file that sets it, so the terminal is pinned
# and NO credential is copied anywhere. This script checks that on two kind clusters and measures the edge cases.
# Needs: kind, kubectl, helm, bash, zsh. Prints PASS/FAIL/INFO; non-zero exit on any FAIL. KEEP=1 keeps the clusters.
set -u
A=sx-spike-a
B=sx-spike-b
WORK=$(mktemp -d)
MAIN=$WORK/main.kubeconfig
PIN=$WORK/pin/b.kubeconfig
fails=0
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }
info() { echo "INFO  $*"; }
check() { if [ "$2" = "$3" ]; then pass "$1 (= $3)"; else fail "$1: expected '$2', got '$3'"; fi; }
cleanup() {
  if [ -z "${KEEP:-}" ]; then kind delete cluster --name "$A" >/dev/null 2>&1; kind delete cluster --name "$B" >/dev/null 2>&1; fi
  chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"
}
trap cleanup EXIT
for t in kind kubectl helm zsh bash; do command -v "$t" >/dev/null || { echo "missing tool: $t"; exit 2; }; done

echo "== creating two kind clusters (parallel)"
kind create cluster --name "$A" --kubeconfig "$WORK/a.kc" --wait 120s >/dev/null 2>&1 &
kind create cluster --name "$B" --kubeconfig "$WORK/b.kc" --wait 120s >/dev/null 2>&1 &
wait
[ -s "$WORK/a.kc" ] && [ -s "$WORK/b.kc" ] || { echo "cluster creation failed"; exit 2; }
KUBECONFIG="$WORK/a.kc:$WORK/b.kc" kubectl config view --flatten >"$MAIN"; chmod 600 "$MAIN"
kmain() { KUBECONFIG="$MAIN" kubectl "$@"; }
kmain config use-context "kind-$A" >/dev/null
kmain --context "kind-$B" create namespace only-in-b >/dev/null
mkdir -p "$WORK/chart/templates"
printf 'apiVersion: v2\nname: probe\nversion: 0.1.0\n' >"$WORK/chart/Chart.yaml"
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: probe}\n' >"$WORK/chart/templates/cm.yaml"
helm --kubeconfig "$MAIN" --kube-context "kind-$B" install probe "$WORK/chart" -n only-in-b >/dev/null 2>&1

echo "== the pin file: ONLY current-context, read-only, in a private directory"
umask 077; mkdir -p "$WORK/pin"; chmod 700 "$WORK/pin"
printf 'apiVersion: v1\nkind: Config\ncurrent-context: kind-%s\n' "$B" >"$PIN"; chmod 400 "$PIN"
BOUND="$PIN:$MAIN"            # what the terminal's KUBECONFIG becomes: pin first, then the user's own list
if grep -qE 'token|client-key|client-certificate|password|certificate-authority' "$PIN"; then fail "pin file contains credential-looking fields"; else pass "pin file holds no credential material ($(wc -c <"$PIN" | tr -d ' ') bytes)"; fi
if [ "$(uname)" = "Darwin" ]; then mode() { stat -f '%Lp' "$1"; }; else mode() { stat -c '%a' "$1"; }; fi
check "pin file mode" 400 "$(mode "$PIN")"
check "pin directory mode" 700 "$(mode "$WORK/pin")"

echo "== global context is A; the pinned shell must be on B"
check "global current-context is A" "kind-$A" "$(kmain config current-context)"
check "pinned current-context is B" "kind-$B" "$(KUBECONFIG=$BOUND kubectl config current-context)"
check "global kubectl talks to A" "node/$A-control-plane" "$(kmain get nodes -o name)"
check "pinned kubectl talks to B" "node/$B-control-plane" "$(KUBECONFIG=$BOUND kubectl get nodes -o name)"
check "namespace only-in-b visible when pinned" "namespace/only-in-b" "$(KUBECONFIG=$BOUND kubectl get ns only-in-b -o name)"
check "...and invisible globally" "" "$(kmain get ns only-in-b -o name 2>/dev/null)"
check "helm sees B's release when pinned" "probe" "$(KUBECONFIG=$BOUND helm list -n only-in-b -q 2>/dev/null)"
check "helm sees nothing globally" "" "$(KUBECONFIG=$MAIN helm list -n only-in-b -q 2>/dev/null)"
kmain config use-context "kind-$B" >/dev/null; kmain config use-context "kind-$A" >/dev/null
check "still pinned to B after global churn" "kind-$B" "$(KUBECONFIG=$BOUND kubectl config current-context)"

echo "== other contexts stay reachable on purpose (a deliberate --context is the user's intent)"
check "explicit --context other still works when pinned" "node/$A-control-plane" "$(KUBECONFIG=$BOUND kubectl --context "kind-$A" get nodes -o name)"

echo "== use-context INSIDE the pinned shell: the read-only pin must stop it, and must not leak into the user's file"
before=$(shasum "$MAIN" | cut -d' ' -f1)
out=$(KUBECONFIG=$BOUND kubectl config use-context "kind-$A" 2>&1); rc=$?
if [ $rc -ne 0 ]; then pass "use-context inside the pinned shell is refused (exit $rc)"; else fail "use-context inside the pinned shell SUCCEEDED: $out"; fi
check "pinned context unchanged after the attempt" "kind-$B" "$(KUBECONFIG=$BOUND kubectl config current-context)"
check "the user's own kubeconfig was not modified" "$before" "$(shasum "$MAIN" | cut -d' ' -f1)"
info "kubectl's message: $(echo "$out" | head -1 | cut -c1-160)"
echo "-- for contrast, a WRITABLE pin file:"
cp "$PIN" "$WORK/pin/writable.kubeconfig"; chmod 600 "$WORK/pin/writable.kubeconfig"
KUBECONFIG="$WORK/pin/writable.kubeconfig:$MAIN" kubectl config use-context "kind-$A" >/dev/null 2>&1
check "with a writable pin, use-context moves only the pin file (this is why it is read-only)" "kind-$A" "$(KUBECONFIG="$WORK/pin/writable.kubeconfig:$MAIN" kubectl config current-context)"
check "...and still not the user's file" "$before" "$(shasum "$MAIN" | cut -d' ' -f1)"

echo "== FAILURE MODE: shell startup files overriding KUBECONFIG (unchanged from the first spike)"
H=$WORK/home; mkdir -p "$H/.kube"
echo 'export KUBECONFIG="$HOME/.kube/config"' >"$H/.zshrc"; echo 'export KUBECONFIG="$HOME/.kube/config"' >"$H/.bashrc"
zsh_out=$(env HOME="$H" KUBECONFIG="$BOUND" zsh -i -c 'echo $KUBECONFIG' 2>/dev/null | tail -1)
bash_out=$(env HOME="$H" KUBECONFIG="$BOUND" bash -i -c 'echo $KUBECONFIG' 2>/dev/null | tail -1)
[ "$zsh_out" = "$BOUND" ] && info "zsh keeps the injected KUBECONFIG" || info "zsh: startup file OVERRIDES the injected KUBECONFIG"
[ "$bash_out" = "$BOUND" ] && info "bash keeps the injected KUBECONFIG" || info "bash: startup file OVERRIDES the injected KUBECONFIG"

echo
if [ "$fails" -eq 0 ]; then echo "SPIKE RESULT: all checks passed"; else echo "SPIKE RESULT: $fails check(s) FAILED"; exit 1; fi
