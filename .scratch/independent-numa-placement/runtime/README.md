# Ticket 09: Linux placement validation kit

This kit prepares and checks evidence. Executing the matrix on a VM or hardware
is ticket 11. No live placement, residency, or bandwidth result is supplied here.
The user reported manually testing the earlier feature in a laptop VM on
2026-10-13; its commands and outputs were not supplied. The
[older dated execution record](../../explicit-numa-placement/runtime/results.md)
and legacy fixtures remain historical evidence, unchanged.

Run commands below from this directory. The operator needs Python 3.10+,
kubectl, SSH access to the Linux node, and a container builder. Save evidence
outside the source tree. JSON manifests are also valid Kubernetes YAML.

## Prepare a disposable Linux environment

Deploy kube-apiserver and kubelet built from the **same fork commit**, with the
fork's generated API schema. Record that commit and binary versions separately;
an upstream binary with a similar version string is insufficient. Use a CRI
runtime enforcing the CPU and memory masks. Record kernel, runtime and image
digests, VM configuration and host backing where known. The scheduler is
unchanged: fixtures bind explicitly to the Kubernetes node name.

Configure before starting workloads:

```yaml
cpuManagerPolicy: static
memoryManagerPolicy: Static
topologyManagerPolicy: independent-numa
topologyManagerScope: pod
```

Add real CPU and memory reservations. Choose `reservedSystemCPUs` from
CPU-bearing nodes, leave sufficient exclusive CPUs, and disable incompatible
`align-by-socket`. Set `systemReserved`, `kubeReserved`, eviction thresholds and
per-node `reservedMemory` consistently: Memory Manager validates their totals
against Node Allocatable reservation. Record the entire effective configuration
and startup flags, including kubelet root directory. Do not copy another host's
reservation quantities. A memory-only node requires memory capacity/accounting;
it needs no invented CPU list or CPU reservation. Follow the fork's existing
Static Memory Manager startup validation for each actual memory resource.

Capture the topology on the **Linux node**, preserving sparse IDs and directed
distance rows. Never infer ID membership from the number of nodes, nor obtain
CPU lists from the operator laptop:

```sh
uname -a
lscpu -e=CPU,NODE
numactl -H
cat /sys/devices/system/node/online
for directory in /sys/devices/system/node/node[0-9]*; do
  echo "$directory"
  cat "$directory/cpulist" "$directory/distance" "$directory/meminfo"
  for pages in "$directory"/hugepages/hugepages-*; do
    test -d "$pages" || continue
    echo "$pages"
    cat "$pages/nr_hugepages" "$pages/free_hugepages" "$pages/surplus_hugepages"
  done
done
cat /sys/kernel/mm/transparent_hugepage/enabled
crictl version
```

Also save `kubectl get node NODE -o json`, existing Pods and resource-manager
checkpoints. Record distances' actual column ordering. On virtual NUMA, document
guest versus host topology and any unknown backing. VM evidence cannot establish
real DDR/HBM performance. Missing memory-only, hugepage, or device prerequisites
leave those cases NOT RUN.

Build the probe image, push it to the operator's registry if necessary, and
resolve its digest. The Dockerfile pins the same compiler base used in ticket08;
retain the build command, log, image inspection and registry digest. It adds no
application memory binding or migration. Choose container security settings
appropriate for query-only `move_pages`; denied instrumentation is NOT RUN for
residency, even when allocation and mask checks succeed.

```sh
podman build -t YOUR_REGISTRY/numa-probe:ticket09 .
podman push YOUR_REGISTRY/numa-probe:ticket09
cp config.example.json /tmp/numa-config.json
```

Edit the config with **actual** Kubernetes node, namespace, image digest, CPU
node C, memory node M, and CPU-bearing equal/legacy node E. Node 3 is merely an
example, not an HBM label. Set `hugepages` to all sizes provisioned on M (or `[]`
if none). Every byte quantity must be a positive multiple of that page size.
The generator preserves whole CPU requests and equal requests/limits for all
containers. Default peak usage is three CPUs, 384Mi ordinary memory and three
times each requested hugepage quantity: sidecar plus two apps. During init it
uses two containers. Size `touch_bytes` and hugepage allocations within each
container's limit, with process overhead. Repeat runs sequentially if capacity
does not support multiple fixtures. Do not equate aggregate host capacity with
available capacity on one target.

```sh
python3 fixtures.py --config /tmp/numa-config.json --output /tmp/numa-run-01
export RUN=/tmp/numa-run-01 NS=numa-validation NODE=ACTUAL_NODE SSH_NODE=ACTUAL_SSH_ADDRESS
kubectl create namespace "$NS"
```

The run contains explicit, CPU-only, memory-only, equal, legacy, ordinary and
hugepage manifests; a complete case checklist; and an environment template.
The generator refuses to overwrite a run. The image must be resolved by digest
before evidence is accepted. Fill environment.json with operator/date, exact
fork commit and separately identified matched component commits/versions, kernel,
runtime, image digest, VM/host boundary, and configuration/topology artifact paths.
Artifact entries point to files such as `commands/topology/stdout.txt`, not
capture directories. Keep raw config files and version/build outputs too.

## Capture commands and check every container

`capture.py` runs an explicit argv without a shell and retains command, UTC
times, exit status, stdout and stderr in a new directory, even for nonzero exits.
Use it for kubectl, SSH and observations. It does not interpret success.
For a compound remote command, pass the quoted script explicitly to `ssh`.

```sh
python3 capture.py --output "$RUN/commands/create-explicit" -- \
  kubectl -n "$NS" create -f "$RUN/manifests/explicit.json"
python3 capture.py --output "$RUN/commands/topology" -- \
  ssh "$SSH_NODE" 'numactl -H; cat /sys/devices/system/node/online'
```

The sidecar starts first, then the sequential init. Each probe touches private
anonymous pages (and each hugetlb file for the hugepage fixture), writes
`/tmp/numa-evidence/observation.json`, raw proc status/maps/numa_maps, and waits.
The init waits for `/tmp/numa-release`; inspect it **before** releasing it.
Allocation failure exits nonzero; the observation records the failed resource.
The probe never invokes bind or migration APIs. `move_pages` uses NULL nodes
and flags zero, querying evenly spaced pages including both endpoints. The
observation gives mapping addresses, byte lengths, total/sample counts, errors
and page nodes. Sampling proves only queried pages at the recorded instant.

For a fully explicit pair, set C/M from config. For a partial request, determine
the selected pair from recorded allocations/logs and independently verify:
restored targets first, feasible common node next, otherwise nearest feasible
directed distance, then ascending-ID tie. Save feasible capacities and distances.
Do not simply accept any singleton or assume node 0. Ordinary Pods use existing
single-node behavior; record their actual selected common node. Legacy/equal
requests use E for both expectations.

```sh
export POD=numa-explicit C=ACTUAL_CPU_ID M=ACTUAL_MEMORY_ID
export NODE_CPUS="$(ssh "$SSH_NODE" "cat /sys/devices/system/node/node$C/cpulist")"
kubectl -n "$NS" get pod "$POD" -o json
python3 ../../explicit-numa-placement/runtime/check-masks.py \
  --namespace "$NS" --pod "$POD" --container init \
  --cpu-node "$C" --memory-node "$M" --node-cpus "$NODE_CPUS" \
  --output "$RUN/init-mask.json"
kubectl -n "$NS" cp "$POD:/tmp/numa-evidence" "$RUN/init-probe" -c init
python3 check-residency.py "$RUN/init-probe/observation.json" \
  --memory-node "$M" --output "$RUN/init-residency.json"
```

Capture sidecar masks/residency during init too. Release init, wait for Ready,
then repeat checker/copy/residency for app-a, app-b and sidecar:

```sh
kubectl -n "$NS" exec "$POD" -c init -- touch /tmp/numa-release
kubectl -n "$NS" wait --for=condition=Ready "pod/$POD" --timeout=300s
for container in app-a app-b sidecar; do
  python3 ../../explicit-numa-placement/runtime/check-masks.py \
    --namespace "$NS" --pod "$POD" --container "$container" \
    --cpu-node "$C" --memory-node "$M" --node-cpus "$NODE_CPUS" \
    --output "$RUN/$container-mask.json"
  kubectl -n "$NS" cp "$POD:/tmp/numa-evidence" "$RUN/$container-probe" -c "$container"
  python3 check-residency.py "$RUN/$container-probe/observation.json" \
    --memory-node "$M" --output "$RUN/$container-residency.json"
done
```

Use new filenames for each phase. Preserve actual Pod UID, container IDs,
restart counts and runtime image IDs with `kubectl get pod -o json`. The mask
checker inspects the workload's `/proc/1/status`; `--status-file` can recheck
archived raw status. Its legacy `--node N --node-cpus LIST` interface still
checks both resources against N. Neither mask success nor selected hints prove
residency. The residency checker refuses missing/incomplete queries; it verifies
only the touched mappings listed by this probe. Shared/file-backed or earlier
pages are outside the confinement guarantee.

Repeat the whole procedure with each form. For `memory-only-node`, use an M
whose recorded cpulist is empty. For hugepages, use `hugepages.json`, verify
ordinary mapping plus **every** provisioned size, actual hugetlb allocation and
per-node counters before/after. Retain matching `/proc/maps` and `numa_maps`
to audit mapping identity. If the kernel cannot query hugetlb page locations,
retain its error and mark residency NOT RUN; successful mount/allocation or a
memory mask alone cannot fill that gap. The native probe supports one or more
page sizes in a single container.
Pass `--hugepage-size 2Mi` (repeated for every provisioned size) to the residency
checker for these captures; the report independently requires all configured
sizes for the hugepages row. The probe checks the mount's hugetlbfs type and
page size, and unlinks its files on normal exit so completed init allocations
can be reused by applications.

## Recovery and refusal matrix

The generated cases.json lists every required row and its evidence slots.
For each row save exact submitted manifest, command captures, Pod JSON/events,
and the relevant manager state. All participating forms share the completed
feature: repeat restart/exhaustion trials with explicit, CPU-only and memory-only
forms, recording the originally resolved pair for each. Use a separate dated
run for each topology/configuration, then link those runs in the final record.

| Case | Procedure and acceptance |
| --- | --- |
| Container restart | Save IDs and masks, use `crictl stop CONTAINER_ID` on the node for app-a and sidecar, wait for a new ID/restart count, recapture masks/residency. For init, use a fresh Pod, stop it before release and inspect its restarted instance. C/M must stay the same. |
| Kubelet restart | Save checkpoints and IDs, `sudo systemctl restart kubelet`, save service status/journal, recheck running containers. Preserve all checkpoint files. |
| Post-recovery start | After kubelet restart, stop an app/sidecar and inspect its new ID, masks and residency. Repeat with init while its gate is held. A surviving container alone does not prove recovery. |
| CPU exhaustion | From measured free CPUs on C, make single-container fillers with `fixtures.py --single --form explicit --name fill-cpu ...` and tuned config. Bind to the test node; keep memory small. Leave enough CPUs on another node for the test request. Repeated admissions must refuse CPU shortage without fallback. |
| Memory exhaustion | Fill/reserve ordinary memory on M, keeping sufficient CPU capacity. Record spare memory elsewhere. Submit explicit and CPU-only fixtures whose whole memory group cannot fit. Refuse rather than broaden M or span nodes. For memory-only requests M stays explicit. |
| Hugepage exhaustion | Fill a selected size on M while another node has enough of that size; ordinary memory and CPUs must still fit. Submit the hugepage fixture for each size separately and a combined-size fixture. Refuse the missing size without fallback. |
| Automatic aggregate-only | For each partial form arrange the omitted resource to fit only by summing multiple nodes; refuse. Separately exhaust the nearest node but leave a farther singleton feasible; choose the farther feasible node and verify actual masks/residency. |
| Missing/CPU-less IDs | Use an in-range ID absent from online nodes; then use a memory-only node as C. Save specific local topology refusal, distinguished from missing M. |
| Invalid API forms | On copies of generated JSON, set each field to -1 and 64, combine legacy with either new field, and submit server dry-runs. Require specific validation errors. Equal C/M is accepted separately. |
| Ineligible workload | One change at a time: 500m CPU, unequal/missing CPU/memory request/limit in app/init/sidecar, or spec.resources. Require feature-specific API rejection. |
| Unsupported configuration | Separate clean disposable nodes/configurations: old topology policies with new fields; independent-numa with container scope; non-static CPU/Memory Managers; align-by-socket. Save config, startup journal or Pod refusal. Startup failures and admission failures are distinct. |
| Immutability | For every placement field, test changing, adding and removing it on a real Pod, keeping other fields valid. Save API refusal; an unchanged-value update is allowed. |
| Resize/ephemeral | With those APIs enabled, use the resize subresource and ephemeralcontainers subresource. Require the feature-specific rejection. Disabled API/feature is NOT RUN. |
| Unrelated updates | Patch a metadata annotation on the placed Pod, verify it succeeds and placement persists. |
| Device compatible/conflict | Where a topology-aware plugin exists, save its resource/topology hints and request the real extended resource with matching CPU node and remote M. Compatible succeeds; incompatible CPU hint refuses. Existing device preference/fallback semantics remain; no new hard locality claim. Without plugin mark NOT RUN. |
| Legacy old policy | Separate clean single-numa-node node/configuration: run unchanged legacy fixtures/checker, verify app/init/sidecar and recovery. Never erase checkpoints to switch a live node's policy. |
| Ordinary compatibility | Run ordinary.json under independent-numa and old policy, including update/restart/cleanup; compare existing common-node behavior, not an arbitrary fixed node expectation. |
| Failure/normal cleanup | Capture before/after CPU, memory and device checkpoints, live unrelated Pod assignments, capacities/counters. Retry a refused request several times with fresh names, delete it, wait for reconciliation; failed UID must not remain. Delete an admitted fixture and prove resources return and a successful replacement starts. Preserve unrelated UIDs/assignments. |

Useful update commands (record stdout, stderr and return codes):

```sh
kubectl -n "$NS" patch pod "$POD" --type=merge -p '{"spec":{"cpuNUMA":null}}'
kubectl -n "$NS" patch pod "$POD" --subresource=resize --type=strategic \
  -p '{"spec":{"containers":[{"name":"app-a","resources":{"requests":{"cpu":"2","memory":"128Mi"},"limits":{"cpu":"2","memory":"128Mi"}}}]}}'
kubectl -n "$NS" debug "pod/$POD" --image=busybox:1.36 --target=app-a
kubectl -n "$NS" annotate pod "$POD" numa-validation=updated
kubectl -n "$NS" get events --field-selector "involvedObject.name=$POD" -o json
```

For invalid JSON use Python to edit a saved copy and give it a unique valid Pod
name; use `kubectl create --dry-run=server -f COPY`. For admission failures use
actual `create` with nodeName, then collect Pod status, describe, events and
kubelet journal. A scheduler shortage, timeout, unrelated pull error or generic
API-disabled response is not the intended refusal. Capture exact rejecting
layer and reason. Distance corruption/recovery conflict injection is permitted
only on a separate offline test environment, never by hand-editing live
checkpoints; otherwise rely on automated fault-injection evidence and mark the
corresponding runtime case NOT RUN.

For example, these invalid-ID/mixed-field copies are submitted to the API,
without bypassing validation in the fixture generator:

```sh
python3 - "$RUN" <<'PY'
import copy, json, pathlib, sys
run = pathlib.Path(sys.argv[1])
base = json.loads((run / 'manifests/explicit.json').read_text())
for field in ['cpuNUMA', 'memNUMA']:
    for value in [-1, 64]:
        pod = copy.deepcopy(base)
        pod['metadata']['name'] = f'invalid-{field.lower()}-{abs(value)}'
        pod['spec'][field] = value
        (run / f'{pod["metadata"]["name"]}.json').write_text(json.dumps(pod))
for field in ['cpuNUMA', 'memNUMA']:
    pod = copy.deepcopy(base)
    pod['metadata']['name'] = f'mixed-{field.lower()}'
    pod['spec'].pop('memNUMA' if field == 'cpuNUMA' else 'cpuNUMA')
    pod['spec']['numaNode'] = pod['spec'][field]
    (run / f'{pod["metadata"]["name"]}.json').write_text(json.dumps(pod))
PY
for file in "$RUN"/invalid-*.json "$RUN"/mixed-*.json; do
  name=$(basename "$file" .json)
  python3 capture.py --output "$RUN/commands/$name" -- \
    kubectl create --dry-run=server -f "$file"
done
```

For each capacity trial, save a separate tuned config with measured filler
CPU/memory quantities and the resolved C/M. Generate and create fillers with
unique names; record that the filler itself was admitted on those targets:

```sh
python3 fixtures.py --config /tmp/numa-filler-config.json --single \
  --form explicit --name fill-01 --output "$RUN/fill-01.json"
python3 capture.py --output "$RUN/commands/fill-01" -- \
  kubectl create -f "$RUN/fill-01.json"
```

Use `--form hugepages` for hugepage fillers. Tune one resource at a time, keep
all other resources feasible, and preserve the measured capacity of the spare
node. For change/add/remove update tests use the corresponding actual placed
Pod: cpu-only for adding memNUMA, memory-only for adding cpuNUMA, and ordinary
for adding numaNode. This avoids a mixed-field error masking immutability.
Repeat removal and changed-value tests on the appropriate nonempty fields.

On the node, save CPU/Memory Manager state under the configured root
(`cpu_manager_state`, `memory_manager_state`) and device checkpoint where
available, before failure, after refusal, after deletion and after successful
retry. Partial-allocation rollback requires proof allocations actually occurred;
hint-only refusal does not prove rollback. Keep deterministic automated
fault-injection evidence separate from runtime observations.

## Produce an honest report

Edit cases.json: set `result` to PASS/FAIL/NOT RUN, explain `reason`, and fill
each `evidence` slot with run-relative paths. `commands` references capture.py
directories; `masks` and `residency` reference checker JSON files. Other slots
reference raw, nonempty artifacts. Include all lifecycle phases and each tested
form/size; a case's reason states which variants actually ran. The report checks
evidence completeness and checker failures; **the operator adjudicates** refusal,
ranking, recovery, capacities and cleanup against the raw artifacts. It cannot
infer those semantics from a command's exit status. Do not claim a matrix row
passed after executing only one of its listed variants.

```sh
python3 report.py --run "$RUN" --output "$RUN/report-01"
```

Outputs are report.json and report.md with one PASS/FAIL/NOT RUN per case.
Missing environment or required evidence downgrades a proposed PASS to NOT RUN;
a checker FAIL makes that case FAIL. Pending/running command captures never pass.
Exit codes: 0 all cases PASS, 1 any FAIL, 2 incomplete/NOT RUN or invalid input.
Paths cannot escape the run. Reports and captures refuse to overwrite prior
outputs. Retain the run directory intact; hashes identify the referenced bytes.
Record limitations and who reviewed the raw evidence. This evidence kit makes
no performance claim; paired STREAM evaluation is ticket10.

## Local verification

```sh
python3 -m unittest discover -s tests -v
cc -std=c99 -D_GNU_SOURCE -Wall -Wextra -Werror probe.c -o /tmp/numa-probe
mypy fixtures.py capture.py check-residency.py report.py \
  ../../explicit-numa-placement/runtime/check-masks.py
```

Synthetic fixtures test tool behavior, not real placement. A native macOS
probe run can check artifact/allocation handling but cannot query Linux NUMA.
Actual execution and limitations are recorded in [verification-09.md](../verification-09.md).
