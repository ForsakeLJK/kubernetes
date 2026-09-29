# Explicit NUMA placement: design, code changes, and testing

A Pod in this fork can set `spec.numaNode: 1` to require its CPUs and memory allocations on NUMA node 1 of the machine running it. The kubelet refuses the Pod if that node cannot satisfy the request, even when another NUMA node has spare resources. The requirement covers application containers, init containers, and sidecars, including requested hugepages.

The implementation described here is at commit `2d2f7ebd627`, compared with the original baseline `68f9fc030c6`. Implementation tickets 01 through 05 are resolved. Ticket 06 has a test procedure and fixtures, but its Linux multi-NUMA acceptance tests have not run. The [test results](runtime/results.md) record the missing host and the automated checks already performed.

## The design

`numaNode` is an optional integer from 0 through 63. Omitting it keeps the existing allocation path; setting it to zero explicitly selects NUMA 0. The API stores the field as a pointer so those cases remain distinct. The ID is local to the selected machine, and the kubelet checks that it exists in the machine's topology.

The scheduler has not changed. The operator still has to select a suitable machine. A Pod assigned to an unsuitable machine can fail kubelet admission, with no new mechanism to move it elsewhere.

The supported setup is deliberately narrow:

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

Paths below are relative to the Kubernetes repository. The links point to the main implementation files, with tests alongside them.

| Area | Files and reason for the change |
| --- | --- |
| Pod API | [External PodSpec](../../staging/src/k8s.io/api/core/v1/types.go) and [internal PodSpec](../../pkg/apis/core/types.go) add `NUMANode *int32`. Generated protobuf, deepcopy, OpenAPI, apply-configuration code, and API fixtures carry the field through serialization and clients. Workload API schemas change because they embed Pod templates. |
| Validation | [Core validation](../../pkg/apis/core/validation/validation.go) checks the ID and container resources, rejects Windows declarations and Pod-level budgets, and enforces immutability, resize, and ephemeral-container restrictions. |
| Kubelet admission | [NUMA admission handler](../../pkg/kubelet/numa_placement.go), registered in [kubelet initialization](../../pkg/kubelet/kubelet.go), rejects unsupported configurations and absent local IDs. |
| Topology coordination | [Pod-scope Topology Manager](../../pkg/kubelet/cm/topologymanager/scope_pod.go) selects the requested affinity, preserves device-policy checks, validates recovered assignments, and coordinates rollback across providers and containers. |
| CPU allocation | [Static CPU policy](../../pkg/kubelet/cm/cpumanager/policy_static.go) restricts allocation to the requested node. [CPU Manager](../../pkg/kubelet/cm/cpumanager/cpu_manager.go) validates restored assignments and snapshots assignment/reuse state for rollback. |
| Memory and hugepages | [Static memory policy](../../pkg/kubelet/cm/memorymanager/policy_static.go) checks local capacity, prevents mask expansion, and includes resources requested only by init containers or sidecars. [Memory Manager](../../pkg/kubelet/cm/memorymanager/memory_manager.go) validates restored blocks and restores accounting after failure. |
| Device cleanup | [Device Manager](../../pkg/kubelet/cm/devicemanager/manager.go) removes device allocation records created by an unsuccessful placement attempt and preserves earlier records. |
| Container startup | [Linux container lifecycle](../../pkg/kubelet/cm/internal_container_lifecycle_linux.go) requires validated CPU and memory assignments before supplying the runtime masks. |

No scheduler implementation changed. Tests cover API round-trips and client operations, validation, admission, allocation failures, init/sidecar accounting, hugepages, rollback, recovery, and construction of runtime masks. Constructing the correct runtime configuration still needs a separate check that the Linux runtime enforces it.

## Run the automated tests

Run these commands from the Kubernetes repository root with the toolchain required by its `go.mod` (Go 1.27.0 or newer for this checkout). The focused run exercises the NUMA tests without requiring a running cluster:

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

The existing results report passing focused tests, some sandbox-related socket failures in broader suites, and an incomplete repository-wide run. Earlier ticket records also contain an unclassified Device Manager race-test failure. There is no recorded full-suite pass. Those results are historical evidence, not a fresh test run for this guide.

## Try a Pod on a Linux test machine

Use a disposable test cluster with a Linux machine exposing at least two NUMA nodes. Both the API server and kubelet must come from this fork. Build the binaries on Linux for the target architecture:

```sh
make WHAT='cmd/kube-apiserver cmd/kubelet cmd/kubectl'
```

Install them through the test cluster's existing deployment method, retaining its certificates, runtime endpoint, and other cluster configuration. Building alone does not update the running components. An existing upstream cluster needs its API server and kubelet replaced with the fork builds before this test is meaningful.

Merge these settings into the test kubelet's configuration:

```yaml
cpuManagerPolicy: static
memoryManagerPolicy: Static
topologyManagerPolicy: single-numa-node
topologyManagerScope: pod
```

Static managers also need valid system reservations: reserve CPUs for system use and configure `reservedMemory` consistently with the node's allocatable memory reservations. Size these for the machine; the four settings above are not a complete kubelet configuration. Use a freshly configured test node when changing manager policies because existing checkpoints record policy state. Keep checkpoints intact for the recovery tests.

On the Linux node, inspect its topology:

```sh
lscpu -e=CPU,NODE
numactl -H
cat /sys/devices/system/node/online
cat /sys/devices/system/node/node0/cpulist
```

The first test needs one allocatable CPU and 128 MiB of memory on the requested NUMA node. On your workstation, set the Kubernetes node name and its SSH address, then create a dedicated namespace:

```sh
export NODE='replace-with-kubernetes-node-name'
export SSH_HOST='replace-with-node-ssh-address'
export NS=numa-placement-demo
export ID=0
kubectl create namespace "$NS"

cat > /tmp/numa-placement-demo.yaml <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: numa-demo
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

kubectl -n "$NS" apply -f /tmp/numa-placement-demo.yaml
kubectl -n "$NS" wait --for=condition=Ready pod/numa-demo --timeout=300s
kubectl -n "$NS" get pod numa-demo -o jsonpath='{.spec.numaNode}{"\n"}'
```

Use an available image with `sh`, `sleep`, and `cat` if the test node cannot pull BusyBox. If the Pod does not become ready, inspect `kubectl -n "$NS" describe pod numa-demo`. An image-pull failure does not test NUMA allocation.

Check the running process masks with the supplied checker, still from the repository root:

```sh
export NODE_CPUS="$(ssh "$SSH_HOST" "cat /sys/devices/system/node/node$ID/cpulist")"
python3 .scratch/explicit-numa-placement/runtime/check-masks.py \
  --namespace "$NS" --pod numa-demo --container app \
  --node "$ID" --node-cpus "$NODE_CPUS"
```

The checker reads `/proc/self/status` inside the container. It succeeds only when the allowed CPUs form a nonempty subset of that NUMA node's CPUs and `Mems_allowed_list` contains exactly the requested ID. CPU numbers depend on the host. For `ID=0`, the memory-node output must be `0`.

Delete the Pod, set `ID` to another existing NUMA node, recreate the manifest by rerunning the heredoc, and repeat the apply, wait, and checker commands. Changing the live Pod's ID is forbidden.

## Check rejection and compatibility

Keep `numa-demo` running for these update checks:

```sh
# Removing the requirement is an immutable-field change.
kubectl -n "$NS" patch pod numa-demo --type=merge \
  -p '{"spec":{"numaNode":null}}'

# Ephemeral debug containers are excluded.
kubectl -n "$NS" debug pod/numa-demo --image=busybox:1.36 --target=app

# An out-of-range ID must fail API validation.
sed -e 's/name: numa-demo/name: numa-invalid-id/' \
    -e 's/numaNode: .*/numaNode: 64/' /tmp/numa-placement-demo.yaml | \
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

To test compatibility, delete `numa-demo`, remove its `numaNode` line from a copy of the manifest, and recreate it. It should follow the existing policies, which may choose any eligible NUMA node. Do not apply the singleton-node expectation to this control Pod.

## Complete the runtime acceptance tests

The [runtime procedure](runtime/README.md) provides fixtures and commands for the remaining cases:

- Application containers, an ordinary init container, and a restartable sidecar. The fixture needs three CPUs and 384 MiB at steady state. Inspect the ordinary init during its ten-minute run.
- Hugepages on each tested NUMA node, including a write into the hugepage mount. Provision pages before testing and record per-node capacity.
- Container restart followed by another mask check. Then restart kubelet with its checkpoints preserved, force another container restart, and check the new container's masks.
- CPU, ordinary-memory, and hugepage shortages on the requested node while another NUMA node retains spare capacity. Bind test Pods to the machine so a scheduler refusal does not mask the kubelet behavior.
- Failed-admission and normal-cleanup checks against CPU/Memory Manager assignments, plus device-policy behavior when a suitable device plugin is available.

Record manifests, the fork commit, kubelet configuration, topology, process masks, and failure events in a dated copy of the [results record](runtime/results.md). Mark unavailable cases `NOT RUN`. A `Running` Pod alone does not establish correct placement, and a failed request alone does not prove partial-allocation rollback occurred.

After recording the results, remove the namespace created for this guide:

```sh
kubectl delete namespace "$NS"
```
