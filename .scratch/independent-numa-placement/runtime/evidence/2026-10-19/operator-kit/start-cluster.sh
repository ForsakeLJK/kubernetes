#!/usr/bin/env bash
# Operator launcher for the observed two-node ARM64 QEMU guest.
set -euo pipefail
umask 077

KIT=$(cd "$(dirname "$0")" && pwd)
REPO=/home/jukeliu/k8s_workspace/kubernetes
BIN=/home/jukeliu/k8s_workspace/ticket11-bin
EXPECTED=b76f73a6f478ef3ed63cdf58bcb295dfcec1013b
export PATH="$BIN:$REPO/third_party/etcd:$PATH"

[[ $(uname -s) == Linux && $(uname -m) == aarch64 ]] || {
  echo 'Run this inside the Ubuntu ARM64 VM.'; exit 1;
}
[[ $(git -C "$REPO" rev-parse HEAD) == "$EXPECTED" ]] || {
  echo "Expected source commit $EXPECTED. Stop and inspect the checkout."; exit 1;
}
git -C "$REPO" diff --quiet HEAD -- hack || {
  echo 'The repository launch helpers have local changes. Review them first.'; exit 1;
}
(cd "$BIN" && sha256sum -c "$KIT/binaries.sha256")
for tool in cfssl cfssljson curl openssl etcd python3 ss ip; do
  command -v "$tool" >/dev/null || {
    echo "Missing $tool. Install the documented startup dependencies first."; exit 1;
  }
done
python3 - <<'PY'
import pathlib
root = pathlib.Path('/sys/devices/system/node')
expected = {'node0': '0-3', 'node1': '4-7'}
assert (root / 'online').read_text().strip() == '0-1', 'NUMA topology changed'
for node, cpus in expected.items():
    assert (root / node / 'cpulist').read_text().strip() == cpus, 'CPU topology changed'
PY
NODE_IP=$(python3 - <<'PY'
import ipaddress, json, subprocess
routes = json.loads(subprocess.check_output(
    ['ip', '-j', '-4', 'route', 'get', '1.1.1.1'], text=True))
address = routes[0].get('prefsrc')
assert address, 'Cannot determine the guest IPv4 address from its default route'
ip = ipaddress.ip_address(address)
assert not (ip.is_loopback or ip.is_unspecified or ip.is_link_local or ip.is_multicast), address
print(address)
PY
)
export NODE_IP

sudo -v
for process in kubelet kube-apiserver kube-controller kube-scheduler kube-proxy etcd; do
  if pgrep -x "$process" >/dev/null; then
    echo "An existing $process is running. Stop its owning cluster before this run."; exit 1
  fi
done
TASKS=$(sudo ctr -n k8s.io tasks ls)
if [[ -n $(printf '%s\n' "$TASKS" | tail -n +2) ]]; then
  printf '%s\n' "$TASKS"
  echo 'Existing Kubernetes runtime tasks need inspection before a new kubelet starts.'
  exit 1
fi
# Validate the existing etcd before changing swap or creating a cluster.
(cd "$REPO"; source hack/lib/init.sh; kube::etcd::validate)

RUN=$(mktemp -d "$HOME/numa-ticket11.XXXXXXXX")
export RUN REPO BIN
export LOG_DIR="$RUN/logs" TMP_DIR="$RUN/config" CERT_DIR="$RUN/certs"
export ETCD_DIR="$RUN/etcd" POD_MANIFEST_PATH="$RUN/static-pods"
export TICKET11_KUBELET_ROOT="/var/lib/kubelet-$(basename "$RUN")"
mkdir -p "$LOG_DIR" "$TMP_DIR" "$CERT_DIR" "$POD_MANIFEST_PATH"
cp "$KIT/binaries.sha256" "$KIT/build-provenance.txt" "$RUN/"
cp "$0" "$RUN/operator-start.sh"
git -C "$REPO" rev-parse HEAD > "$RUN/fork-commit.txt"
git -C "$REPO" status --short > "$RUN/source-status.txt"

# The upstream dev launcher has a hard-coded PKI cleanup path and may select
# old kubectl binaries by mtime. Adapt paths and prefer the guest InternalIP
# for kubelet access; Ubuntu's hostname may resolve to a loopback address.
python3 - <<'PY'
import difflib, os, pathlib, shlex
repo, run = pathlib.Path(os.environ['REPO']), pathlib.Path(os.environ['RUN'])
old = (repo / 'hack/local-up-cluster.sh').read_text()
new = old
replacements = {
    'KUBE_ROOT=$(dirname "${BASH_SOURCE[0]}")/..': 'KUBE_ROOT=' + shlex.quote(str(repo)),
    '/var/lib/kubelet': os.environ['TICKET11_KUBELET_ROOT'],
    'KUBECTL=$(kube::util::find-binary "kubectl")': 'KUBECTL="${GO_OUT}/kubectl"',
    '      --kubelet-port="${KUBELET_PORT}" \\':
        '      --kubelet-preferred-address-types=InternalIP ' + chr(92) + chr(10)
        + '      --kubelet-port="${KUBELET_PORT}" ' + chr(92),
}
for before, after in replacements.items():
    assert before in new, f'Launcher changed: missing {before}'
    new = new.replace(before, after)
(run / 'local-up-cluster.sh').write_text(new)
(run / 'launcher.diff').write_text(''.join(difflib.unified_diff(
    old.splitlines(True), new.splitlines(True), fromfile='original', tofile='ticket11')))
PY
bash -n "$RUN/local-up-cluster.sh"

export CPUMANAGER_POLICY=static MEMORY_MANAGER_POLICY=Static
export TOPOLOGY_MANAGER_POLICY=independent-numa CGROUP_DRIVER=systemd
export HOSTNAME_OVERRIDE=ubuntu-numa-test KUBELET_HOST=0.0.0.0
export CONTAINER_RUNTIME_ENDPOINT=unix:///run/containerd/containerd.sock
export FAIL_SWAP_ON=true PRESERVE_ETCD=true ENABLE_DAEMON=false
export REUSE_CERTS=false START_MODE=all
export KUBE_ENABLE_CLUSTER_DNS=false KUBE_DEFAULT_STORAGE_CLASS=false
export KUBETEST_IN_DOCKER=false EXTERNAL_CLOUD_PROVIDER=false
export FEATURE_GATES=AllAlpha=false
export EVICTION_HARD='memory.available<100Mi,nodefs.available<10%,nodefs.inodesFree<5%,imagefs.available<15%'
# 512Mi system + 1024Mi Kubernetes + 100Mi eviction = 2 * 818Mi.
# One reserved CPU per node leaves three exclusive CPUs on each node.
export KUBELET_FLAGS="--root-dir=$TICKET11_KUBELET_ROOT --cert-dir=$TICKET11_KUBELET_ROOT/pki --node-ip=$NODE_IP --topology-manager-scope=pod --reserved-cpus=0,4 --system-reserved=cpu=1,memory=512Mi --kube-reserved=cpu=1,memory=1024Mi --reserved-memory=0:memory=818Mi;1:memory=818Mi"
if [[ -f /run/systemd/resolve/resolv.conf ]]; then
  export KUBELET_RESOLV_CONF=/run/systemd/resolve/resolv.conf
fi
{
  printf 'export RUN=%q\n' "$RUN"
  printf 'export PATH=%q:"$PATH"\n' "$BIN"
  printf 'export KUBECONFIG=%q\n' "$CERT_DIR/admin.kubeconfig"
  printf 'export TICKET11_KUBELET_ROOT=%q\n' "$TICKET11_KUBELET_ROOT"
} > "$RUN/client-env.sh"
printf '%s\n' "$RUN" > "$HOME/.numa-ticket11-current"
{
  date -u
  uname -a
  lscpu -e=CPU,NODE
  numactl -H
  containerd --version
  etcd --version
  "$BIN/kubelet" --version
  "$BIN/kube-apiserver" --version
  swapon --show
} > "$RUN/environment.txt" 2>&1
printf '%s\n' "$KUBELET_FLAGS" > "$RUN/kubelet-flags.txt"
printf '%s\n' "$NODE_IP" > "$RUN/guest-node-ip.txt"
sudo swapoff -a
swapon --show > "$RUN/swap-after.txt"

echo "Evidence directory: $RUN"
echo "In another SSH terminal: source $RUN/client-env.sh"
echo 'Leave this terminal running. Ctrl-C stops the cluster; state and logs are retained.'
cd "$REPO"
bash "$RUN/local-up-cluster.sh" -o "$BIN" 2>&1 | tee "$RUN/startup.log"
