# README

This repository is a fork of Kubernetes that introduces support for explicit NUMA node placement (CPU and Memory may reside in different NUMA nodes). This document provides instructions for building and using the new feature, followed by two examples. The examples were run on an Ubuntu arm64 virtual machine. The run on physical NUMA nodes has not been done yet.

## How to Build

The feature is on branch `numa_support_hbm` of `github.com/ForsakeLJK/kubernetes`. Build all six control-plane and node binaries from that branch. The API server needs the new Pod fields and the kubelet needs the `independent-numa` policy, so a stock binary for either one won't work.

### 1. Get the source and Go

```bash
git clone https://github.com/ForsakeLJK/kubernetes.git
cd kubernetes
git checkout numa_support_hbm
cat .go-version          # 1.27.1
go version               # must match .go-version
```

The build also needs `make`, `rsync` and Bash 4 or newer.

### 2. Build the binaries

On the target machine itself:

```bash
make WHAT="cmd/kube-apiserver cmd/kube-controller-manager cmd/kube-scheduler cmd/kubelet cmd/kube-proxy cmd/kubectl"
ls _output/bin/
```

To cross-compile from another machine (for example, from a Mac for an ARM64 Linux VM), set the platform:

```bash
KUBE_BUILD_PLATFORMS=linux/arm64 make WHAT="cmd/kube-apiserver cmd/kube-controller-manager cmd/kube-scheduler cmd/kubelet cmd/kube-proxy cmd/kubectl"
ls _output/local/bin/linux/arm64/
```

Use `linux/amd64` for an x86-64 node. Then copy the binaries to the node:

```bash
scp _output/local/bin/linux/arm64/* user@node:/path/to/numa-bin/
```

If you change the API types, run `make update` before you build to regenerate code. Don't edit generated files by hand.

### 3. Confirm the build includes the feature

The version string is a placeholder (`v0.0.0-master+$Format:%H$`), so check the commit embedded in each binary instead:

```bash
go version -m _output/local/bin/linux/arm64/kubelet | grep -E 'vcs.revision|vcs.modified'
git rev-parse HEAD       # should equal vcs.revision; vcs.modified should be false
```

Once the cluster is running, confirm that the API server serves the new fields:

```bash
kubectl explain pod.spec.cpuNUMA
kubectl explain pod.spec.memNUMA
```

### 4. Start a single-node test cluster (optional)

`hack/local-up-cluster.sh` reads the kubelet policy settings from environment variables. Run it on the Linux node, with etcd 3.7.1 or newer and containerd already installed:

```bash
export CPUMANAGER_POLICY=static
export MEMORY_MANAGER_POLICY=Static
export TOPOLOGY_MANAGER_POLICY=independent-numa
export CGROUP_DRIVER=systemd
export CONTAINER_RUNTIME_ENDPOINT=unix:///run/containerd/containerd.sock
export KUBELET_FLAGS="--topology-manager-scope=pod --reserved-cpus=0,4 --system-reserved=cpu=1,memory=512Mi --kube-reserved=cpu=1,memory=1024Mi --reserved-memory=0:memory=818Mi;1:memory=818Mi"
export EVICTION_HARD='memory.available<100Mi,nodefs.available<10%,nodefs.inodesFree<5%,imagefs.available<15%'

sudo swapoff -a
hack/install-etcd.sh                       # only if etcd is older than 3.7.1
export PATH="$PWD/third_party/etcd:$PATH"
hack/local-up-cluster.sh -o /path/to/numa-bin
```

The `--reserved-memory` total (818Mi + 818Mi = 1636Mi) has to equal system-reserved + kube-reserved + the memory eviction threshold (512 + 1024 + 100). Change these values to fit your machine. Leave this terminal open. The script prints the `KUBECONFIG` to export in a second terminal.

## How to Use

This fork lets a Pod pin its CPUs to one NUMA node and its memory to a different one, using two Pod fields: `cpuNUMA` and `memNUMA`. The kubelet enforces both as hard requirements. If it can't satisfy them, it refuses the Pod; it never falls back to another node.

### 1. Configure the kubelet

Add these settings to the kubelet configuration on every node that will run these Pods, then restart the kubelet:

```yaml
cpuManagerPolicy: static
memoryManagerPolicy: Static
topologyManagerPolicy: independent-numa
topologyManagerScope: pod
reservedSystemCPUs: "0,4"          # example: one CPU per NUMA node
reservedMemory:                    
  - numaNode: 0
    limits: {memory: 818Mi}
  - numaNode: 1
    limits: {memory: 818Mi}
```

- The `independent-numa` policy runs only on Linux and only with `topologyManagerScope: pod`. With `container` scope the kubelet won't start.
- Change the reserved CPUs and memory to fit your machine. The `reservedMemory` values must add up to the kubelet's `systemReserved` + `kubeReserved` + `evictionHard` memory.
- Before you write the Pod, look up the machine's NUMA layout:

```bash
lscpu | grep -i numa
cat /sys/devices/system/node/node*/cpulist
numactl -H          # node sizes and the distance matrix
```

### 2. Write the Pod

Put the fields at the top level of `spec`, not inside a container:

```yaml
spec:
  cpuNUMA: 0      # NUMA node that supplies every CPU in the Pod
  memNUMA: 1      # NUMA node that supplies all memory in the Pod
```

The Pod must meet these rules, or it is rejected:

- Guaranteed QoS. Every container (application, init and sidecar) sets CPU and memory requests equal to its limits.
- Each container requests a whole number of CPUs, such as `1` or `3` (not `500m`).
- No Pod-level `spec.resources`.
- Don't set `numaNode` in the same Pod as `cpuNUMA` or `memNUMA`. The API server rejects the combination even when the values match.
- Each value is between 0 and 63. The value must be a NUMA node ID that exists on the machine, not a count of nodes.
- The CPU node needs enough free exclusive CPUs, and the memory node needs enough free memory. Capacity added up across several nodes doesn't count.
- The scheduler doesn't know about these fields. Use `nodeName` or a node selector to put the Pod on a machine with the right topology.
- All three fields are immutable. You also can't resize the Pod in place or add ephemeral containers to it.

### 3. What the feature supports

| Pod spec                      | Result                                                       |
| ----------------------------- | ------------------------------------------------------------ |
| `cpuNUMA: C` and `memNUMA: M` | CPUs come from node C and memory from node M. C and M can be the same. |
| `cpuNUMA: C` only             | CPUs come from node C. The kubelet picks the memory node (see below). |
| `memNUMA: M` only             | Memory comes from node M. The kubelet picks the CPU node (see below). |
| `numaNode: N` (older field)   | CPUs and memory both come from node N, as before.            |
| No NUMA fields                | Same behavior as `single-numa-node`.                         |

Other supported behavior:

- A memory-only NUMA node with no CPUs, such as an HBM node, can be a `memNUMA` target. A `cpuNUMA` target must have CPUs that can be allocated.
- Hugepage requests are also served from the memory node.
- Topology-aware device-plugin devices are aligned with the CPU node.
- Every container in the Pod (init containers and sidecars too) gets the same CPU node and the same memory node.
- The selected node pair is kept across container restarts and kubelet restarts.

### 4. How the kubelet picks a node you didn't specify

When you set only one field, the kubelet chooses the other node in this order:

1. If the Pod already has CPU or memory assigned (for example, after a kubelet restart), the kubelet keeps that node.
2. Otherwise it tries the node you specified. With `cpuNUMA: 0` only, it tries to put memory on node 0 as well. If that node has enough free capacity, it is chosen.
3. Otherwise it picks the node that can fit the request and has the smallest NUMA distance `distance[CPU node][memory node]`, as shown in `numactl -H`.
4. If two nodes have the same distance, it picks the lower node ID.
5. If distance data is missing and the kubelet can't tell which node is closest, it refuses the Pod. It never guesses by node ID. To fix this, set both fields or correct the firmware's distance table.

If no node can fit the request, the kubelet refuses the Pod with a resource error. Kubelet logs and Pod events tell you whether the failure was in the CPU target, the memory target, the resources or the policy.

**Scope of the guarantee:** the container's CPU affinity covers only node C, and new memory can be allocated only on node M. Pages that already exist, are shared or are file-backed (such as the page cache) may sit on other nodes, and pages are never migrated.

## Example 1: CPU on NUMA node 0, memory on NUMA node 1

The example machine has two NUMA nodes. Node 0 has CPUs 0-3 and node 1 has CPUs 4-7. CPUs 0 and 4 are reserved for the system. On your own machine, replace the node name and CPU IDs with your values.

### Create the Pod

Get the Kubernetes node name, then write the manifest:

```bash
kubectl get nodes -o wide

cat > split-demo.yaml <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: split-demo
spec:
  nodeName: ubuntu-numa-test        # your node name
  cpuNUMA: 0
  memNUMA: 1
  containers:
    - name: app
      image: busybox:1.36
      command: ["sh", "-c", "sleep infinity"]
      resources:
        requests: {cpu: "1", memory: 256Mi}
        limits:   {cpu: "1", memory: 256Mi}
EOF

kubectl apply -f split-demo.yaml
kubectl wait --for=condition=Ready pod/split-demo --timeout=120s
```

If the Pod stays `Pending` or shows `Failed`, find the reason with:

```bash
kubectl describe pod split-demo | sed -n '/Events/,$p'
kubectl get pod split-demo -o jsonpath='{.status.reason}{"  "}{.status.message}{"\n"}'
```

### Verify 1: the API kept both fields

```bash
kubectl get pod split-demo -o jsonpath='cpuNUMA={.spec.cpuNUMA} memNUMA={.spec.memNUMA}{"\n"}'
```

Expected output: `cpuNUMA=0 memNUMA=1`. If a field is missing, the API server doesn't run the forked build.

### Verify 2: the container's CPU and memory masks

```bash
kubectl exec split-demo -- grep -E 'Cpus_allowed_list|Mems_allowed_list' /proc/self/status
kubectl exec split-demo -- cat /sys/fs/cgroup/cpuset.cpus.effective /sys/fs/cgroup/cpuset.mems.effective
```

Expected output:

```text
Cpus_allowed_list:  1          # one exclusive CPU from node 0 (1, 2 or 3)
Mems_allowed_list:  1          # memory can come only from node 1
1
1
```

Check that the CPU really belongs to node 0. Run this on the host:

```bash
cat /sys/devices/system/node/node0/cpulist     # 0-3
```

### Verify 3: the kubelet's allocation records

Run these on the host. The kubelet state directory is `/var/lib/kubelet` unless you set `--root-dir`:

```bash
sudo jq '.entries' /var/lib/kubelet/cpu_manager_state
sudo jq '.entries' /var/lib/kubelet/memory_manager_state
```

The CPU Manager entry for `app` should list a node 0 CPU. The Memory Manager entry should show `"numaAffinity": [1]` for `memory`.

### Verify 4: new memory really lands on node 1

Write 48 MiB to the container's `/dev/shm` (tmpfs). Then compare each NUMA node's `Shmem` counter on the host before and after:

```bash
grep Shmem: /sys/devices/system/node/node*/meminfo

kubectl exec split-demo -- dd if=/dev/zero of=/dev/shm/fill bs=1M count=48

grep Shmem: /sys/devices/system/node/node*/meminfo
```

The node 1 counter should go up by about 49152 kB, and node 0 should barely change. Clean up afterwards:

```bash
kubectl exec split-demo -- rm /dev/shm/fill
kubectl delete pod split-demo
```

You can also run a variant with only `cpuNUMA: 0`. Its `Mems_allowed_list` should be `0`, because when node 0 has free memory the kubelet picks the same node for memory (step 2 of the selection rules).

## Example 2: STREAM with CPU and memory on different NUMA nodes

This example runs STREAM in two Pods. In `stream-remote`, CPUs are on node 0 and memory is on node 1. In `stream-local`, both are on node 0. Comparing their Triad bandwidth shows the cost of the split placement. It uses the same two-node machine as Showcase 1, where node 0 has three exclusive CPUs (1-3). Run one Pod at a time so both get the same CPUs.

### Prepare the source and the host tools

Run these on the host:

```bash
sudo apt-get install -y numactl          # provides numastat
curl -fsSLO https://www.cs.virginia.edu/stream/FTP/Code/stream.c
sha256sum stream.c
# a52bae5e175bea3f7832112af9c085adab47117f7d2ce219165379849231692b  (STREAM 5.10)
```

### Create the split-placement Pod

```bash
cat > stream-remote.yaml <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: stream-remote
spec:
  nodeName: ubuntu-numa-test        # your node name
  cpuNUMA: 0
  memNUMA: 1
  containers:
    - name: stream
      image: gcc:14.2.0-bookworm
      command: ["sleep", "infinity"]
      resources:
        requests: {cpu: "3", memory: 1Gi}
        limits:   {cpu: "3", memory: 1Gi}
EOF

kubectl apply -f stream-remote.yaml
kubectl wait --for=condition=Ready pod/stream-remote --timeout=600s
kubectl exec stream-remote -- grep -E 'Cpus_allowed_list|Mems_allowed_list' /proc/self/status
# Cpus_allowed_list:  1-3
# Mems_allowed_list:  1
```

The memory node must have 1 GiB free on its own, and it does here. Three arrays of 20,000,000 doubles take 480 MB, so 1 GiB leaves room for the compiler and process overhead. Choose an array size of at least 4 times the last-level cache of the CPUs you use.

### Build and run STREAM

```bash
kubectl cp stream.c stream-remote:/tmp/stream.c

kubectl exec stream-remote -- gcc -O3 -fopenmp \
  -DSTREAM_ARRAY_SIZE=20000000 -DNTIMES=200 /tmp/stream.c -o /tmp/stream

kubectl exec stream-remote -- env OMP_NUM_THREADS=3 OMP_PROC_BIND=true /tmp/stream \
  | tee stream-remote.txt
```

With `NTIMES=200`, the run lasts long enough (tens of seconds) to do the checks below from a second terminal.

### Verify while STREAM is running

Run these on the host, in a second terminal:

```bash
PID=$(pgrep -x stream)

# CPU affinity: only node 0 CPUs
taskset -cp $PID

# Memory policy: only node 1
grep -E 'Cpus_allowed_list|Mems_allowed_list' /proc/$PID/status

# Which CPU each OpenMP thread is running on (PSR column)
ps -L -o tid,psr,comm -p $PID

# Where the process's memory actually lives, per NUMA node (MB)
sudo numastat -p $PID
```

Expected results:

- `taskset` reports `1-3`, and every thread's `PSR` is 1, 2 or 3.
- `Mems_allowed_list` is `1`.
- In `numastat -p`, nearly all of the about 458 MiB (480 MB) taken by the three arrays is in the `Node 1` column of the `Private` / `Total` rows. `Node 0` shows only a few MB of shared libraries and page cache.

### Verify the result

```bash
grep -E '^(Copy|Scale|Add|Triad)|Solution Validates' stream-remote.txt
```

The output has to contain `Solution Validates: avg error less than 1.000000e-13 on all three arrays`. If it doesn't, the bandwidth numbers can't be used.

### Compare with local placement

Delete the split Pod, then run the same steps in a Pod that keeps memory on node 0:

```bash
kubectl delete pod stream-remote --wait=true

sed -e 's/stream-remote/stream-local/' -e 's/memNUMA: 1/memNUMA: 0/' \
  stream-remote.yaml > stream-local.yaml
kubectl apply -f stream-local.yaml
kubectl wait --for=condition=Ready pod/stream-local --timeout=600s

kubectl cp stream.c stream-local:/tmp/stream.c
kubectl exec stream-local -- gcc -O3 -fopenmp \
  -DSTREAM_ARRAY_SIZE=20000000 -DNTIMES=200 /tmp/stream.c -o /tmp/stream
kubectl exec stream-local -- env OMP_NUM_THREADS=3 OMP_PROC_BIND=true /tmp/stream \
  | tee stream-local.txt

# while it runs, on the host: Node 0 should now hold the arrays
sudo numastat -p $(pgrep -x stream)

grep -H Triad stream-remote.txt stream-local.txt
kubectl delete pod stream-local
```

On physical hardware with real NUMA, `stream-local` may show the higher Triad rate. To test HBM, set `memNUMA` to the HBM node and compare again. In a VM, both guest NUMA nodes may be backed by the same host memory, so the rates can be about equal even though placement is correct. In that case, rely on `numastat`, not bandwidth, to confirm placement. For a fair comparison, use the same CPU set, keep the host idle, and run each configuration several times.



## AI Usage Declaration

GPT-6 Astra, GPT-6.1 Sol, and Claude Opus 5.5 were used to implement this feature.


