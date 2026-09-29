# Ticket 06: multi-NUMA Linux acceptance

This procedure is for a disposable Linux test node exposing at least two NUMA
nodes. Record the fork commit, kernel, runtime, kubelet configuration, node
topology, exact commands, Pod status/events, and command output in a dated copy
of [results.md](results.md). A passing API request or Topology Manager hint is
not evidence of actual container placement.

## Prerequisites and setup

1. Build and deploy **both** `kube-apiserver` and `kubelet` from the same fork
   commit. Use the fork's generated API artifacts and a CRI runtime that applies
   the CPU and memory masks passed by kubelet. Record the binary versions and
   runtime version. An unmodified API server or kubelet does not support this
   contract.
2. Configure the kubelet with CPU Manager `static`, Memory Manager `Static`,
   Topology Manager `single-numa-node`, and Topology Manager scope `pod`.
   Configure nonzero `reservedMemory` for each NUMA node and matching system
   reservations so static Memory Manager starts. Reserve CPUs for system use;
   ensure at least three whole allocatable CPUs and sufficient ordinary memory
   on each test node. Use a runtime with effective cpuset and mems enforcement.
   Start the kubelet with these settings before creating participating Pods.
3. On the Linux node, record `lscpu -e=CPU,NODE`, `numactl -H`,
   `cat /sys/devices/system/node/online`, each node's `cpulist`, ordinary memory,
   and per-node hugepage counts under
   `/sys/devices/system/node/node*/hugepages/`. Provision at least one 2 MiB
   hugepage on **each** tested node and verify its allocatable count in
   `kubectl describe node`. If a size is not supported or allocatable, mark its
   case unexecuted. Account for CPU and memory reserved by the kubelet and
   already running Pods when sizing cases.
4. Set `NODE` to the suitable Linux Kubernetes node name, `OTHER` to a real
   nonzero NUMA ID shown by that host, and `NS` to an isolated test namespace.
   The operator workstation needs `kubectl`, `jq`, `python3`, and SSH access to
   read the Linux node's topology.
   The fixture uses `spec.nodeName` to make machine selection explicit. The
   scheduler has not changed: the operator must arrange suitable machine
   selection, and a Pod assigned to an unsuitable node can fail kubelet
   admission without automatic relocation.

```sh
kubectl create namespace numa-acceptance
export NS=numa-acceptance NODE=<kubernetes-node-name> OTHER=<existing-nonzero-numa-id>
kubectl get node "$NODE" -o jsonpath='{.status.allocatable}'
kubectl -n "$NS" get pods -o wide
```

Use an image available to the test node if `busybox:1.36` cannot be pulled. The
image must provide `sh`, `sleep`, and `cat`. The fixture needs three exclusive
CPUs at steady state (sidecar plus two applications), two during the ordinary
init, and 384 MiB concurrent ordinary memory at steady state. Adjust resource
quantities before the run if the node's **per-node** allocatable capacity is
smaller. Keep every CPU request a positive whole number and every CPU/memory
request equal to its limit.

## Success and effective masks

From this directory, create and inspect one Pod for NUMA 0, then repeat for
`OTHER`. Inspect each ordinary init before creating the next Pod. The fixture
keeps that init running for ten minutes; increase its `sleep` if image pulls or
manual inspection need more time.

```sh
export ID=0 POD=numa-0
sed -e "s/__NAME__/$POD/g" -e "s/__NODE__/$NODE/g" \
    -e "s/__NUMA__/$ID/g" pod.yaml.in | kubectl -n "$NS" apply -f -
for attempt in $(seq 1 150); do
  kubectl -n "$NS" get pod "$POD" -o json | jq -e \
    '.status.initContainerStatuses[]? | select(.name=="init" and .state.running!=null)' \
    >/dev/null && break
  sleep 2
done
kubectl -n "$NS" get pod "$POD" -o wide
```

During the ordinary init's 600-second window, run the checker for `init` and
`sidecar`. After both application containers are running, run it for `app-a`,
`app-b`, and `sidecar`. Obtain `NODE_CPUS` **on the Linux node**, not from the
operator's laptop. A CPU list such as `0-3,8-11` is accepted. The checker calls
`kubectl exec ... cat /proc/self/status` and fails if effective allowed CPUs
include any CPU outside the requested node or if the effective memory-node mask
is anything other than the singleton requested node.

```sh
export NODE_CPUS="$(ssh "$NODE" "cat /sys/devices/system/node/node$ID/cpulist")"
python3 check-masks.py --namespace "$NS" --pod "$POD" --container init \
  --node "$ID" --node-cpus "$NODE_CPUS"
python3 check-masks.py --namespace "$NS" --pod "$POD" --container sidecar \
  --node "$ID" --node-cpus "$NODE_CPUS"
kubectl -n "$NS" wait --for=condition=Ready "pod/$POD" --timeout=900s
for container in app-a app-b sidecar; do
  python3 check-masks.py --namespace "$NS" --pod "$POD" --container "$container" \
    --node "$ID" --node-cpus "$NODE_CPUS"
done
```

If there are fewer than six allocatable CPUs for both concurrent fixtures,
run `kubectl -n "$NS" delete pod numa-0 --wait=true` before creating the other
fixture. Repeat the creation and checks with `ID=$OTHER`, `POD=numa-$OTHER`.
If the Kubernetes node name is not SSH resolvable, use its SSH address. Save
all checker output. A checker failure is an acceptance failure, including one
caused by the runtime ignoring masks.

Delete the ordinary fixtures first if they consume the target node's remaining
exclusive CPUs. Create a hugepage Pod for each ID with
`hugepages-pod.yaml.in`. Check its
effective masks, then write a page into the mounted hugepage filesystem and
record the result. A successful mount alone does not prove a page was allocated.

```sh
sed -e 's/__NAME__/numa-pages-0/g' -e "s/__NODE__/$NODE/g" \
    -e 's/__NUMA__/0/g' hugepages-pod.yaml.in | kubectl -n "$NS" apply -f -
kubectl -n "$NS" wait --for=condition=Ready pod/numa-pages-0 --timeout=300s
python3 check-masks.py --namespace "$NS" --pod numa-pages-0 --container hugepages \
  --node 0 --node-cpus "$(ssh "$NODE" 'cat /sys/devices/system/node/node0/cpulist')"
kubectl -n "$NS" exec numa-pages-0 -c hugepages -- \
  sh -c 'dd if=/dev/zero of=/hugepages/page bs=2M count=1 && cat /proc/self/status | grep -E "^(Cpus|Mems)_allowed_list"'
```

Repeat for `OTHER`, adapting name and node ID. For each requested hugepage
size supported by the host, adapt the fixture's resource key, quantity, and
`emptyDir.medium`; record the per-node free count before and after. The process
mask and successful page allocation are the runtime observations. Compare
manager state/checkpoints as an additional accounting observation, not as a
substitute for process masks.

## Lifecycle

Record each container's ID and restart count (`kubectl -n "$NS" get pod "$POD"
-o json`). Trigger a **container** restart by killing the container's PID 1
through `kubectl exec -c <container> "$POD" -- kill 1` (or `crictl stop` on
the node), wait for the count to increase, then rerun the checker on the new
container. Repeat for an application and the restartable sidecar. For an init
container, create a fresh fixture and stop it during its sleep, then inspect
the restarted init while it runs. Record the before/after container IDs.

Restart **kubelet** with the node's service manager
(`sudo systemctl restart kubelet` on a systemd node) while Pods remain running.
Record `systemctl status kubelet`, checkpoint files under the configured
`--root-dir`, container IDs, and Pod events. After kubelet is healthy, rerun
the checker on all running fixture containers, then force one container restart
and recheck. Preserve checkpoints during this test. Do not treat kubelet
restart alone as proof that recovered assignments were reused.

## Refusal, API, and compatibility matrix

For each refusal, save the submitted manifest, command exit code, `kubectl get
pod -o yaml` where a Pod exists, `kubectl describe pod`, and
`kubectl get events --field-selector involvedObject.name=<pod>`. Identify the
requested ID and failure category in the status or events. For API rejections,
save the API error. Run each case with a unique Pod name and delete it before
the next case.

| Case | Reproducible change / action | Expected observation |
| --- | --- | --- |
| Absent local ID | Set `numaNode` to an in-range ID absent from `/sys/devices/system/node/online` | Kubelet refusal names the missing ID. |
| Invalid IDs | Submit `-1` and `64` | API validation rejects both; `0` remains valid. |
| Unsupported manager setup | On a disposable second kubelet, change one required policy or pod scope at a time; submit a participating Pod | Kubelet refuses with configuration detail. Omitted-field Pod remains eligible. |
| Ineligible declarations | Change one CPU request to `500m`, remove a memory limit, make request differ from limit, or add `spec.resources` | API rejects each. |
| Immutable requirement | `kubectl patch pod <name> --type=merge -p '{"spec":{"numaNode":1}}'`; also try adding/removing the field | API rejects the change. |
| Resize | Submit a CPU or memory resize through the Pod resize API/subresource with resize enabled | Refused; replace Pod to change resources. |
| Ephemeral container | `kubectl debug -n "$NS" <pod> --image=busybox:1.36 --target=app-a` | API refuses the addition. |
| Eligible CPUs | Occupy all exclusive CPUs on requested node with Guaranteed Pods; leave another NUMA node with spare CPUs, then submit fixture | Kubelet refuses and names CPU shortage; no fallback. |
| Ordinary memory | Occupy or reserve requested node's allocatable ordinary memory while another node has spare memory; submit a Pod whose additional request cannot fit | Kubelet refuses and names ordinary memory shortage. |
| Hugepages | Occupy requested node's allocatable pages of the requested size while another node has spare pages; submit hugepage fixture | Kubelet refuses and names the hugepage size. |
| Device policy | With an existing topology-aware device plugin, request a device whose hint conflicts with selected node | Existing device/topology rejection remains; CPU/memory masks never broaden. No new device-locality guarantee is claimed. |

These commands make the API cases repeatable. Use a fresh name per trial and
save stdout/stderr. The resize command is relevant when the cluster permits
Pod resize; if that API is disabled, record `NOT RUN` for the feature-specific
resize check rather than counting the generic disabled-API error as a pass.

```sh
for invalid in -1 64; do
  sed -e "s/__NAME__/invalid-$invalid/g" -e "s/__NODE__/$NODE/g" \
      -e "s/__NUMA__/$invalid/g" pod.yaml.in | \
    kubectl -n "$NS" apply --dry-run=server -f -
done
kubectl -n "$NS" patch pod numa-0 --type=merge -p '{"spec":{"numaNode":1}}'
kubectl -n "$NS" patch pod numa-0 --subresource=resize --type=strategic \
  -p '{"spec":{"containers":[{"name":"app-a","resources":{"requests":{"cpu":"2","memory":"128Mi"},"limits":{"cpu":"2","memory":"128Mi"}}}]}}'
kubectl -n "$NS" debug pod/numa-0 --image=busybox:1.36 --target=app-a
```

For absent local ID, substitute a value from `0..63` that is absent from the
node's `/sys/devices/system/node/online` and create the Pod with a fresh name.
For declaration failures, edit a copy of `pod.yaml.in` one field at a time as
specified in the table and use `kubectl apply --dry-run=server -f <copy>`.
For unsupported configuration, change **one** of the four named kubelet
settings on a disposable node, restart kubelet, create a participating Pod
bound to it, collect refusal, restore the setting, and repeat for the next
setting. Record the kubelet config file and its before/after values.

Use per-node CPU lists and Memory Manager allocatable state before each
shortage case. Scale filler Pod CPU/memory/hugepage requests to the measured
remaining **requested-node** capacity; create filler Pods one at a time until
the next request cannot fit. Verify another node still has enough spare
capacity for that request. Save filler Pod manifests and observations so the
case is reproducible. A generic node-level `Insufficient` scheduler result is
not this test: bind to the test node and inspect kubelet refusal.

The following filler command can consume measured free CPUs or ordinary
memory on the requested node. Set `FILL_CPU` to a positive integer and
`FILL_MEMORY` to a quantity that fits locally; choose values separately for
the CPU and memory shortage trials. Keep its CPU/memory request and limit
equal. For hugepages, create copies of `hugepages-pod.yaml.in` with fresh names
until the requested node's page capacity is occupied, then submit one more.

```sh
export FILL_CPU=1 FILL_MEMORY=128Mi FILL_NAME=fill-0
cat <<EOF | kubectl -n "$NS" apply -f -
apiVersion: v1
kind: Pod
metadata: {name: $FILL_NAME}
spec:
  nodeName: $NODE
  numaNode: 0
  containers:
  - name: fill
    image: busybox:1.36
    command: ["sh", "-c", "sleep 3600"]
    resources:
      requests: {cpu: "$FILL_CPU", memory: $FILL_MEMORY}
      limits: {cpu: "$FILL_CPU", memory: $FILL_MEMORY}
EOF
kubectl -n "$NS" describe pod "$FILL_NAME"
```

For a partial-allocation failure, make the first container fit and a later
container fail on the requested node if the host's capacity and hint sequence
allow it. Admission may refuse based on hints before any concrete allocation;
record that as a refusal/cleanup observation, not as proof of partial rollback.
The preceding automated fault-injection tests cover the deterministic partial
allocation path. Compare CPU Manager and Memory Manager checkpoint assignments
and per-node available capacity before and after the failed attempt. On a
default kubelet root, capture `sudo cat /var/lib/kubelet/cpu_manager_state` and
`sudo cat /var/lib/kubelet/memory_manager_state` before and after, and check
that the failed Pod UID is absent while pre-existing Pod UIDs remain. Repeat
the same failing Pod creation; results must not worsen.
Then delete all test Pods and verify newly allocated assignments are released
and a formerly successful Pod can run again. Preserve unrelated pre-existing
assignments. Also delete a normally admitted Pod and verify its allocations
are released. Do not alter checkpoints by hand on a live node.

For compatibility, remove the `numaNode` line from the same fixtures and run
them with the same node assignment, then perform normal update, restart, and
cleanup. Record acceptance and actual masks without imposing the explicit-node
expectation; existing policy may choose either eligible node. Test omitted-field
Pods on the alternate kubelet configuration as well.

## Evidence and limits

Fill [results.md](results.md) with an explicit `PASS`, `FAIL`, or `NOT RUN`
for every case. A missing host or missing hugepage/device prerequisite means
`NOT RUN`, never `PASS`. Automated Go regression tests establish API/resource
manager behavior but cannot replace effective mask observations on Linux.

The guarantee is CPU affinity and memory-allocation confinement. It does not
prove physical locality of every shared/cached or previously allocated page,
and this procedure does not measure application performance.
