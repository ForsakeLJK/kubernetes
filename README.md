# Kubernetes NUMA placement support

[TOC]

## Introduction

This is a private fork of [Kubernetes](https://github.com/kubernetes/kubernetes) for research into explicit NUMA placement. It adds a Pod-level NUMA node requirement that the kubelet's resource managers enforce. The work targets research experiments; upstream acceptance is outside the current scope.

A Pod in this fork can set `spec.numaNode: 1` to require its CPUs and memory allocations on NUMA node 1 of the machine running it. The kubelet refuses the Pod if that node cannot satisfy the request, even when another NUMA node has spare resources. The requirement covers application containers, init containers, and sidecars, including requested hugepages.

The implementation covers placement, failure cleanup, and restart recovery. Linux multi-NUMA acceptance tests was run only on an Ubuntu ARM64 VM because no suitable host was available. The [test results](.scratch/explicit-numa-placement/runtime/results.md) record the automated checks and the runtime cases still awaiting execution.

## Run Example

The example below shows how to try the NUMA support. It was run on an Ubuntu ARM64 VM with four CPUs and about 8 GiB of RAM. The VM exposed NUMA nodes 0 and 1, with CPUs 0 and 1 on NUMA 0 and CPUs 2 and 3 on NUMA 1.

Run the commands inside the VM, starting from a clone of this repository and no existing Kubernetes cluster. Adjust the CPU IDs and per-node memory reservations to match your machine. The development script starts a disposable cluster using this fork's API server and kubelet.

### Build this fork

#### Find the repository and inspect the VM

Locate the clone:

```sh
find "$HOME" -maxdepth 5 -path '*/hack/local-up-cluster.sh' -print
```

Change into the directory containing `hack`, replacing `/path/to/clone` with that directory:

```sh
cd /path/to/clone
git rev-parse --short HEAD
cat .go-version
uname -m
nproc
free -h
df -h .
cat /sys/devices/system/node/online
lscpu -e=CPU,NODE
```

In the example VM, `uname -m` reported `aarch64`, the online NUMA nodes were `0-1`, and the CPU table was:

```text
CPU NODE
  0    0
  1    0
  2    1
  3    1
```

If Ubuntu sees only NUMA 0, configure the VM to expose another NUMA node before testing placement on both nodes. The hypervisor determines the topology visible to the guest.

#### Install the build and runtime tools

```sh
sudo apt update
sudo apt install -y \
  build-essential curl ca-certificates git jq python3 numactl \
  conntrack iptables iproute2 openssl containerd runc golang-cfssl

cfssl version
command -v cfssljson
numactl -H
```

The development script uses `cfssl` and `cfssljson` to generate certificates. Both must be available on ARM64.

#### Install Go

This checkout uses Go 1.27.1. Download the Linux ARM64 archive and verify it against the checksum from the [Go download page](https://go.dev/dl/):

```sh
curl -fL https://go.dev/dl/go1.27.1.linux-arm64.tar.gz \
  -o /tmp/go1.27.1.linux-arm64.tar.gz

echo '3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec  /tmp/go1.27.1.linux-arm64.tar.gz' \
  | sha256sum -c -
```

Continue when the checksum reports `OK`:

```sh
mkdir -p "$HOME/.local/go1.27.1"
tar -xzf /tmp/go1.27.1.linux-arm64.tar.gz \
  -C "$HOME/.local/go1.27.1" --strip-components=1

export PATH="$HOME/.local/go1.27.1/bin:$PATH"
go version
```

Expect `go1.27.1 linux/arm64`. Repeat the PATH export in any new terminal used to build the fork. For another architecture, use the matching archive and checksum from the download page.

#### Build the components and install etcd

From the repository root:

```sh
GOMAXPROCS=2 make GOFLAGS='-p=2' \
  WHAT='cmd/kube-apiserver cmd/kubelet cmd/kubectl cmd/kube-controller-manager cmd/kube-scheduler cmd/kube-proxy'

hack/install-etcd.sh
export PATH="$PWD/third_party/etcd:$PATH"
etcd --version
```

Limiting compilation to two workers reduces memory use on the VM.

### Run and check a Pod with NUMA placement

Each demo Pod has one container requesting one whole CPU and 128 MiB of memory, with matching limits. One Pod selects NUMA 0 and the other selects NUMA 1. The checks read each container's effective CPU and memory-node masks.

#### Configure containerd

Generate a fresh containerd configuration with systemd cgroups, matching the kubelet settings used below:

```sh
sudo mkdir -p /etc/containerd

containerd config default \
  | sed 's/SystemdCgroup = false/SystemdCgroup = true/' \
  | sudo tee /etc/containerd/config.toml >/dev/null

sudo systemctl enable containerd
sudo systemctl restart containerd
sudo systemctl is-active containerd
```

Expect `active`. Disable swap for this session and enable forwarding for the Pod network:

```sh
sudo swapoff -a
sudo sysctl -w net.ipv4.ip_forward=1
sudo sysctl -w net.ipv6.conf.all.forwarding=1
```

Swap may return after a reboot; rerun `swapoff` before starting the cluster.

#### Start the cluster

Reserve CPU 0 for system use. On the example topology, that leaves CPU 1 for an exclusive allocation on NUMA 0 and CPUs 2 and 3 on NUMA 1. The memory reservations total `1636Mi`: `1Gi` for the system, `512Mi` for Kubernetes, and the script's default `100Mi` eviction threshold. Each NUMA node reserves `818Mi`.

```sh
mkdir -p /tmp/numa-cluster-logs /tmp/numa-cluster-config

export HOSTNAME_OVERRIDE=numa-vm
export CGROUP_DRIVER=systemd
export FAIL_SWAP_ON=true
export CPUMANAGER_POLICY=static
export MEMORY_MANAGER_POLICY=Static
export TOPOLOGY_MANAGER_POLICY=single-numa-node
export KUBE_ENABLE_CLUSTER_DNS=false
export CONTROLPLANE_SUDO='sudo -E'
export LOG_DIR=/tmp/numa-cluster-logs
export TMP_DIR=/tmp/numa-cluster-config

export KUBELET_FLAGS='--topology-manager-scope=pod --reserved-cpus=0 --system-reserved=memory=1Gi --kube-reserved=memory=512Mi --reserved-memory=0:memory=818Mi;1:memory=818Mi'
```

The API server uses the node name `numa-vm` when connecting to kubelet for `kubectl exec`. Both processes run inside this VM, and the development kubelet listens on `127.0.0.1`. Add the hostname entry once and verify it:

```sh
echo '127.0.0.1 numa-vm' | sudo tee -a /etc/hosts
getent hosts numa-vm

sudo -v
hack/local-up-cluster.sh -o "$PWD/_output/bin"
```

Wait for `Local Kubernetes cluster is running. Press Ctrl-C to shut it down.` Leave this terminal open while testing. The script installs the CNI networking plugins it needs.

Keep `CONTROLPLANE_SUDO='sudo -E'` on subsequent starts. Otherwise, the script can start the API server without sudo after the certificate directory becomes writable, then fail to open the audit log left by the first run. If you start from a new terminal, restore the Go and etcd PATH entries and repeat the configuration exports above.

If startup times out waiting for the API server, inspect its log and the etcd log:

```sh
tail -n 80 /tmp/numa-cluster-logs/kube-apiserver.log
tail -n 40 /tmp/numa-cluster-logs/etcd.log
```

#### Connect from a second terminal

Open another terminal inside Ubuntu and change into the same repository root before setting PATH:

```sh
cd /path/to/clone
export PATH="$PWD/_output/bin:$PATH"
export KUBECONFIG=/var/run/kubernetes/admin.kubeconfig
export NS=numa-acceptance
export NODE=numa-vm

kubectl get nodes -o wide
kubectl explain pod.spec.numaNode
kubectl create namespace "$NS"
```

Expect `numa-vm` to be `Ready` and the second command to describe `numaNode`. If `kubectl` is not found, check that `$PWD/_output/bin/kubectl` exists. Setting PATH from your home directory points at the wrong location.

#### Create a Pod on each NUMA node

The explicit `nodeName` binds these Pods to the VM. The scheduler is unchanged; the operator chooses a machine with suitable topology and capacity.

```sh
for ID in 0 1; do
  cat > "/tmp/numa-demo-$ID.yaml" <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: numa-demo-$ID
  namespace: $NS
spec:
  nodeName: $NODE
  numaNode: $ID
  containers:
  - name: app
    image: busybox:1.36
    command: ["sh", "-c", "sleep 3600"]
    resources:
      requests: {cpu: "1", memory: 128Mi}
      limits: {cpu: "1", memory: 128Mi}
EOF

  kubectl apply -f "/tmp/numa-demo-$ID.yaml" || break
  kubectl -n "$NS" wait \
    --for=condition=Ready "pod/numa-demo-$ID" --timeout=300s || break
done
```

If a Pod does not become ready, inspect `kubectl -n "$NS" describe pod numa-demo-0` or `numa-demo-1`. An image-pull failure does not test NUMA allocation. Use an available image with `sh`, `sleep`, `cat`, and `grep` if the VM cannot pull BusyBox.

#### Check the effective masks

Read the allowed CPU and memory-node lists directly from a process inside each container:

```sh
kubectl -n "$NS" exec numa-demo-0 -c app -- \
  sh -c 'grep -E "^(Cpus_allowed_list|Mems_allowed_list):" /proc/self/status'

kubectl -n "$NS" exec numa-demo-1 -c app -- \
  sh -c 'grep -E "^(Cpus_allowed_list|Mems_allowed_list):" /proc/self/status'

lscpu -e=CPU,NODE
```

On the example topology, `numa-demo-0` should show CPU `1` and memory nodes `0`. `numa-demo-1` should show CPU `2` or `3` and memory nodes `1`. Every allowed CPU must belong to the requested NUMA node, and the memory-node list must contain exactly that node's ID. A Ready Pod alone does not establish placement. These masks describe CPU affinity and allowed memory allocation; they do not measure the physical location of every shared or cached page.

You can also run the supplied checker from the repository root:

```sh
for ID in 0 1; do
  python3 .scratch/explicit-numa-placement/runtime/check-masks.py \
    --namespace "$NS" --pod "numa-demo-$ID" --container app \
    --node "$ID" \
    --node-cpus "$(cat /sys/devices/system/node/node$ID/cpulist)" || break
done
```

Expect `PASS` for each Pod. If `kubectl exec` reports that it cannot resolve `numa-vm`, check `getent hosts numa-vm` and the hostname entry from the cluster setup above.

The full ticket06 fixture needs three allocatable CPUs per NUMA node. See [Complete the runtime acceptance tests](#complete-the-runtime-acceptance-tests) for the remaining cases.

Keep the Pods and cluster running for the rejection checks below. When finished, delete the namespace with `kubectl delete namespace "$NS"` and press Ctrl-C in the cluster terminal. By default, the development script removes its etcd data on shutdown. Follow the runtime procedure when testing restart recovery and preserve the manager checkpoints.

## Placement rules

`numaNode` is an optional integer from 0 through 63. Omitting it keeps the existing allocation path; setting it to zero explicitly selects NUMA 0. The API stores the field as a pointer so those cases remain distinct. The ID is local to the selected machine, and the kubelet checks that it exists in the machine's topology.

The scheduler has not changed. The operator still has to select a suitable machine. A Pod assigned to an unsuitable machine can fail kubelet admission, with no new mechanism to move it elsewhere.

Participating Pods require this setup:

| Requirement | Value |
| --- | --- |
| Operating system | Linux |
| CPU Manager | `static` |
| Memory Manager | `Static` |
| Topology Manager | `single-numa-node`, scope `pod` |
| Container resources | Equal CPU/memory requests and limits; positive whole-number CPU requests |
| Pod QoS | Guaranteed |

Every application container, init container, and sidecar needs those resource declarations. Pod-level resource budgets are excluded. The NUMA ID is immutable, CPU/memory resize is rejected, and ephemeral-container additions are rejected. Changing placement or resource requirements requires a replacement Pod. There is no new feature gate.

Placement has two observable parts: the container's allowed CPUs all belong to the selected NUMA node, and its allowed memory-node mask contains exactly that node. Memory Manager also accounts for ordinary memory and requested hugepage sizes there. Shared or previously allocated pages can have different physical locality; this feature does not migrate them or promise that every page a process can access lives on the selected node.

## How the kubelet enforces it

The API server checks the declaration and update restrictions. A new kubelet admission handler checks the operating system, manager configuration, and local NUMA ID before allocation.

Topology Manager then restricts the eligible CPU and memory hints to the requested node. This needed more than an extra hint: the original CPU allocation path could take CPUs outside the aligned mask, and Memory Manager could expand an insufficient mask. The new allocation paths prohibit both behaviors for explicitly placed Pods.

CPU Manager intersects available and reusable CPUs with the requested node's CPUs. Memory Manager checks ordinary memory and every requested hugepage size on that node. Existing rules for sequential init-container reuse and overlapping sidecars still determine how much resource the Pod needs at once.

Allocation can fail after an earlier manager or container has already reserved resources. Before each provider allocation, the new path records enough state to undo that attempt. A later failure runs cleanup in reverse order, restoring CPU/memory accounting and removing newly created device records while retaining pre-existing assignments.

On recovery, CPU and Memory Managers validate concrete checkpointed assignments against the request. Valid assignments are reused. Conflicting assignments block admission or affected container starts/restarts. Linux container creation checks the assignments again before passing `CpusetCpus` and `CpusetMems` to the runtime. This also catches missing assignments that would otherwise leave the runtime without the required masks.

Devices keep their existing topology-policy checks. A conflicting device hint can still cause rejection. The device-manager change handles rollback; it adds no device-locality guarantee.

## Where the code changed

| Area | Files and reason for the change |
| --- | --- |
| Pod API | [External PodSpec](staging/src/k8s.io/api/core/v1/types.go) and [internal PodSpec](pkg/apis/core/types.go) add `NUMANode *int32`. Generated protobuf, deepcopy, OpenAPI, apply-configuration code, and API fixtures carry the field through serialization and clients. Workload API schemas change because they embed Pod templates. |
| Validation | [Core validation](pkg/apis/core/validation/validation.go) checks the ID and container resources, rejects Windows declarations and Pod-level budgets, and enforces immutability, resize, and ephemeral-container restrictions. |
| Kubelet admission | [NUMA admission handler](pkg/kubelet/numa_placement.go), registered in [kubelet initialization](pkg/kubelet/kubelet.go), rejects unsupported configurations and absent local IDs. |
| Topology coordination | [Pod-scope Topology Manager](pkg/kubelet/cm/topologymanager/scope_pod.go) selects the requested affinity, preserves device-policy checks, validates recovered assignments, and coordinates rollback across providers and containers. |
| CPU allocation | [Static CPU policy](pkg/kubelet/cm/cpumanager/policy_static.go) restricts allocation to the requested node. [CPU Manager](pkg/kubelet/cm/cpumanager/cpu_manager.go) validates restored assignments and snapshots assignment/reuse state for rollback. |
| Memory and hugepages | [Static memory policy](pkg/kubelet/cm/memorymanager/policy_static.go) checks local capacity, prevents mask expansion, and includes resources requested only by init containers or sidecars. [Memory Manager](pkg/kubelet/cm/memorymanager/memory_manager.go) validates restored blocks and restores accounting after failure. |
| Device cleanup | [Device Manager](pkg/kubelet/cm/devicemanager/manager.go) removes device allocation records created by an unsuccessful placement attempt and preserves earlier records. |
| Container startup | [Linux container lifecycle](pkg/kubelet/cm/internal_container_lifecycle_linux.go) requires validated CPU and memory assignments before supplying the runtime masks. |

No scheduler implementation changed. Tests cover API round-trips and client operations, validation, admission, allocation failures, init/sidecar accounting, hugepages, rollback, recovery, and construction of runtime masks. Constructing the correct runtime configuration still needs a separate check that the Linux runtime enforces it.

## Run the automated tests

Run these commands from the root of this fork with the toolchain required by its `go.mod` (Go 1.27.0 or newer for this checkout). The focused run exercises the NUMA tests without requiring a running cluster:

```sh
go test -count=1 -run 'NUMA' \
  ./pkg/apis/core/v1 \
  ./pkg/apis/core/validation \
  ./pkg/kubelet \
  ./pkg/kubelet/cm \
  ./pkg/kubelet/cm/cpumanager \
  ./pkg/kubelet/cm/memorymanager \
  ./pkg/kubelet/cm/topologymanager \
  ./pkg/kubelet/cm/devicemanager \
  k8s.io/api/core/v1 \
  k8s.io/client-go/kubernetes/fake
```

Run on Linux to include the Linux-only container-lifecycle tests. A successful cross-compile on another operating system does not execute those tests. For broader regression coverage, rerun the command without `-run 'NUMA'`.

The existing results report passing focused tests, some sandbox-related socket failures in broader suites, and an incomplete repository-wide run. Earlier ticket records also contain an unclassified Device Manager race-test failure. There is no recorded full-suite pass. See the linked results record for the commands, environment, and outcomes.

## Check rejection and compatibility

Keep `numa-demo-0` running for these update checks:

```sh
# Removing the requirement is an immutable-field change.
kubectl -n "$NS" patch pod numa-demo-0 --type=merge \
  -p '{"spec":{"numaNode":null}}'

# Ephemeral debug containers are excluded.
kubectl -n "$NS" debug pod/numa-demo-0 --image=busybox:1.36 --target=app

# An out-of-range ID must fail API validation.
sed -e 's/name: numa-demo-0/name: numa-invalid-id/' \
    -e 's/numaNode: .*/numaNode: 64/' /tmp/numa-demo-0.yaml | \
  kubectl -n "$NS" create --dry-run=server -f -
```

Each operation should be rejected for the stated reason. The dry-run uses a separate Pod name so an existing test Pod does not obscure the validation result.

For a kubelet-level rejection, create a fresh Pod with an ID in the range 0 through 63 that is absent from the host's topology. Inspect its status and events:

```sh
export REJECTED_POD='replace-with-rejected-pod-name'
kubectl -n "$NS" describe pod "$REJECTED_POD"
kubectl -n "$NS" get events \
  --field-selector "involvedObject.name=$REJECTED_POD"
```

The implemented admission reasons include `NUMANodeNotFound`, `NUMAPlacementUnsupported`, and `NUMAPlacementFailed`. Device topology conflicts can retain the existing topology-affinity error. Read the message for the node ID and the specific failure.

To test compatibility, delete `numa-demo-0`, remove its `numaNode` line from a copy of the manifest, and recreate it. It should follow the existing policies, which may choose any eligible NUMA node. Do not apply the singleton-node expectation to this control Pod.

## Complete the runtime acceptance tests

The [runtime procedure](.scratch/explicit-numa-placement/runtime/README.md) provides fixtures and commands for the remaining cases:

- Application containers, an ordinary init container, and a restartable sidecar. The fixture needs three CPUs and 384 MiB at steady state. Inspect the ordinary init during its ten-minute run.
- Hugepages on each tested NUMA node, including a write into the hugepage mount. Provision pages before testing and record per-node capacity.
- Container restart followed by another mask check. Then restart kubelet with its checkpoints preserved, force another container restart, and check the new container's masks.
- CPU, ordinary-memory, and hugepage shortages on the requested node while another NUMA node retains spare capacity. Bind test Pods to the machine so a scheduler refusal does not mask the kubelet behavior.
- Failed-admission and normal-cleanup checks against CPU/Memory Manager assignments, plus device-policy behavior when a suitable device plugin is available.

Record manifests, the fork commit, kubelet configuration, topology, process masks, and failure events in a dated copy of the [results record](.scratch/explicit-numa-placement/runtime/results.md). Mark unavailable cases `NOT RUN`. A `Running` Pod alone does not establish correct placement, and a failed request alone does not prove partial-allocation rollback occurred.

After recording the results, remove the test namespace:

```sh
kubectl delete namespace "$NS"
```

## Design records and upstream Kubernetes

The [feature specification](.scratch/explicit-numa-placement/spec.md) records the agreed behavior. The [implementation tickets](.scratch/explicit-numa-placement/issues/) contain the development and verification history, and the [runtime procedure](.scratch/explicit-numa-placement/runtime/README.md) includes the multi-container and hugepage fixtures.

Kubernetes is an open source system for deploying and managing containerized applications. General Kubernetes documentation and contributor resources remain available upstream:

- [Kubernetes documentation](https://kubernetes.io)
- [Developer documentation](https://git.k8s.io/community/contributors/devel#readme)
- [Troubleshooting](https://kubernetes.io/docs/tasks/debug/)
- [Community and communication](https://git.k8s.io/community/communication)
- [Published components](staging/README.md)

Use [CONTRIBUTING.md](CONTRIBUTING.md) for the repository's contribution guidance and [LICENSE](LICENSE) for its license. The NUMA field and behavior described above are additions in this fork.
