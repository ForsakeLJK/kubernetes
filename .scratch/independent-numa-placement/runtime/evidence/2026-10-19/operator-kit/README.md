# Ticket 11: QEMU startup checkpoint

Prepared 2026-10-19 from the operator's topology and runtime captures.
Target: Ubuntu ARM64 guest `ubuntu-numa-test`, source at
`/home/jukeliu/k8s_workspace/kubernetes`, binaries at
`/home/jukeliu/k8s_workspace/ticket11-bin`.

The Mac builds' embedded Go metadata identifies clean commit
`b76f73a6f478ef3ed63cdf58bcb295dfcec1013b` for all six components.
Their displayed Kubernetes version is a placeholder. `binaries.sha256`
ties the VM files to the builds inspected here; `build-provenance.txt`
retains their embedded metadata. Rebuilt binaries need new provenance and
checksums, not removal of the checksum guard.

## Run manually

On the Mac:

```sh
scp -P 2222 -r /Users/jukeliu/ra_workspace/numa_support_kubernetes/ticket11-operator \
  jukeliu@localhost:/home/jukeliu/k8s_workspace/
```

Inside Ubuntu, install certificate helpers if missing. These are compiled
utilities, not a Go compiler. Ubuntu provides ARM64 `golang-cfssl`:
https://packages.ubuntu.com/resolute/arm64/golang-cfssl

```sh
sudo apt-get update
sudo apt-get install -y golang-cfssl curl openssl
command -v cfssl cfssljson
```

The fork requires etcd >= 3.7.1. The launcher checks the installed version
before making runtime changes. If it reports an older etcd, preserve the
existing installation and use the repository's installer:

```sh
cd /home/jukeliu/k8s_workspace/kubernetes
cp -a third_party/etcd "third_party/etcd-before-ticket11-$(date -u +%Y%m%dT%H%M%SZ)"
bash hack/install-etcd.sh
```

Start in an SSH terminal, leaving it open:

```sh
bash /home/jukeliu/k8s_workspace/ticket11-operator/start-cluster.sh
```

The script checks source identity, binary hashes, topology, startup helpers,
existing processes/tasks and etcd. It then disables swap until reboot or manual
re-enablement, and starts the cluster. It does not edit fstab. Startup may need
network access for runtime sandbox images. CNI files are present in the supplied
inventory; functional Pod networking still requires a live test.

The configuration reserves CPUs 0 and 4, leaving three exclusive CPUs on each
NUMA node. Memory reservations are an initial test configuration: 512Mi for
system services, 1024Mi for Kubernetes, plus a 100Mi eviction threshold. Their
1636Mi sum is accounted as 818Mi per node. Observe actual load before larger
benchmarks; these numbers do not prove sufficient capacity for every experiment.
CPU reservations exclude those CPUs from exclusive workload allocations; they
do not pin all host/control-plane processes to them.

A copy of `hack/local-up-cluster.sh` uses the original source root, explicit new
kubectl path, a unique kubelet root/PKI directory, and the API server's
`--kubelet-preferred-address-types=InternalIP` flag. The kubelet registers the
guest IPv4 address selected by its default route via `--node-ip`. This avoids
the Node proxy refusing Ubuntu hostnames that resolve to loopback. The route
lookup does not send traffic to the lookup destination. The patch is retained as
`launcher.diff`. Existing manager checkpoints remain under the old root.
DNS and storage addons are disabled for this initial placement environment.

In a second SSH terminal, once startup finishes:

```sh
bash /home/jukeliu/k8s_workspace/ticket11-operator/check-cluster.sh
```

The checker saves API readiness, Node identity/status, actual kubelet configz,
Pod inventory, and custom API field discovery. It does not create test Pods.
Node information is saved before configz is requested. A failed configz request
retains its stderr and Node address diagnostics rather than producing an empty
`configz.json`.

## Evidence and failures

Each attempt creates `$HOME/numa-ticket11.XXXXXXXX`; its path is saved in
`$HOME/.numa-ticket11-current`. Certificate material and admin kubeconfig stay
private. To collect only startup diagnostics:

```sh
RUN=$(cat "$HOME/.numa-ticket11-current")
tar -czf "$HOME/ticket11-startup-diagnostics.tar.gz" -C "$RUN" \
  startup.log logs environment.txt kubelet-flags.txt launcher.diff
```

For a successful check, share `nodes.txt` and `configz.json` from the printed
readiness directory. These are startup evidence only, not placement PASS results.

Ctrl-C in the startup terminal stops cluster components. Etcd data, kubelet
state and logs are retained. It does not promise deletion of workload containers;
delete test Pods while the cluster is still running before a normal shutdown.
Do not use this launcher to perform the later kubelet-only restart test: that
test must preserve the running control plane and current kubelet identity/state.

Validation performed locally: all six binaries' embedded revision/platform/clean
metadata checked; SHA-256 manifest generated; Bash and embedded Python syntax
checked; launcher adaptation exercised against the current source and syntax
checked. No Linux startup, API readiness or runtime placement has been executed
by the assistant.
