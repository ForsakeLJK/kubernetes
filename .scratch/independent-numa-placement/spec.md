# Independent CPU and memory NUMA placement

Status: approved by the user on 2026-10-14; implementation not started.

Design interview: [map.md](map.md). Implementation: [implementation-plan.md](implementation-plan.md).
Evaluation: [validation-plan.md](validation-plan.md).
Inspected fork baseline: `621a7c5c03e`.

## Purpose and boundaries

Extend this private Kubernetes research fork so a Pod can require CPU placement
on one NUMA node and memory allocation on another. The motivating machine has
CPUs and DDR on node 0 and HBM on memory-only node 3. NUMA IDs identify locations
inside a selected machine; node 3 is not a portable synonym for HBM.

This extends the [legacy contract](../explicit-numa-placement/spec.md). That
contract's common CPU/memory target remains valid for `numaNode`. The new policy
provides separate targets for the new fields. The scheduler remains unchanged;
operators arrange placement onto an appropriate Kubernetes node.

The guarantee is CPU affinity and memory-allocation confinement. It does not
guarantee physical locality of every shared, file-backed, or previously allocated
page, and introduces no page migration. Memory-tier discovery, automatic HBM
selection, upstream proposal work, and automatic rescheduling are outside scope.

## API contract

Add optional `*int32` fields `CPUNUMA` and `MemNUMA` to external and internal
PodSpec, serialized as `cpuNUMA` and `memNUMA` respectively. Go identifiers are
implementation names; the user-facing JSON/YAML spelling is the contract.

| Declaration | Meaning |
| --- | --- |
| `cpuNUMA: C` | CPU node C is required; select one memory node automatically. |
| `memNUMA: M` | Memory node M is required; select one CPU node automatically. |
| Both new fields | Require the specified CPU and memory nodes; they may be equal. |
| `numaNode: N` | Preserve the existing requirement that both resources use N. |
| No fields | Preserve ordinary policy behavior. |
| Legacy field with either new field | API rejection, even if values agree. |

- Preserve absence independently of zero. Zero selects NUMA node 0.
- Retain the range 0 through 63, inclusive, matching the current mask abstraction.
- All three placement fields are immutable, including adding or removing a field.
- A NUMA ID's membership and usable resource capacity are checked locally by kubelet.
  Sparse IDs are valid; do not compare an ID with the number of NUMA nodes.
- A CPU target must contain eligible CPUs. A memory target may contain no CPUs.
- Field presence opts a Pod into the existing eligibility and lifecycle restrictions.
- Retain the private fork's no-additional-feature-gate approach.
- Regenerate serialization, conversion, deepcopy, OpenAPI, and apply artifacts;
  assign unused protobuf field numbers after inspecting the schema at implementation.

Illustrative Pod, after the feature is implemented:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hbm-demo
spec:
  nodeName: example-worker
  cpuNUMA: 0
  memNUMA: 3
  containers:
    - name: app
      image: busybox:1.36
      command: ["sh", "-c", "sleep 3600"]
      resources:
        requests: {cpu: "1", memory: 128Mi}
        limits: {cpu: "1", memory: 128Mi}
```

This is a placement fixture, not a STREAM workload. Operators substitute their
actual machine and topology IDs. Benchmark resources are sized separately.

## Kubelet policy and compatibility

Approved new policy name: `independent-numa`.

Required configuration for participating Pods:

```yaml
cpuManagerPolicy: static
memoryManagerPolicy: Static
topologyManagerPolicy: independent-numa
topologyManagerScope: pod
```

This fragment omits platform-specific system CPU/memory reservations, which must
also satisfy the existing static-manager configuration requirements.

| Pod form | `single-numa-node` | `independent-numa` | Other policies |
| --- | --- | --- | --- |
| No placement fields | Existing behavior | Delegate to existing single-numa-node behavior | Existing behavior |
| `numaNode` | Existing hard placement | Same combined hard placement | Refuse as currently unsupported |
| Either/both new fields | Refuse as unsupported | Independent placement | Refuse as unsupported |

The new policy is Linux-only and supports only pod scope. Reject the new policy
with container scope during configuration/startup; do not change the global
default scope. Retain current topology node-count limits and options validation.
Extend the incompatibility with CPU Manager `align-by-socket`: CPU placement
still requires one NUMA node. Device handling is described below.

For participating Pods retain Guaranteed QoS, equal CPU/memory requests and
limits in every application/init/sidecar container, and positive whole-number
CPU requests. Reject Pod-level resource budgets (`spec.resources`). Ordinary
Pods remain subject to their existing eligibility and policy behavior.

## Placement selection

For a new-field Pod compute one Pod-wide pair `(C, M)`:

- All assigned CPUs for every covered container belong to C.
- Every covered container's memory-node mask is exactly `{M}`.
- Ordinary memory and every requested hugepage size are accounted for on M.
- All containers share the node pair; concrete CPU sets and quantities retain
  existing container allocation and init/sidecar resource reuse rules.
- Each resource must fit on its own single node. Aggregate capacity across nodes
  is not sufficient. CPU and memory may be on different nodes.

Build CPU candidates from CPU and device topology hints, and memory candidates
from ordinary-memory and hugepage hints. A candidate is feasible only when its
whole group can satisfy existing admission and allocation rules. Preserve
device hint preference/rejection semantics; an explicit CPU target does not
override an incompatible device hint. Explicit CPU/memory targets may override
those resource providers' ordinary placement preferences as legacy placement does.

Filter candidates by every explicit target before selection. A fixed target
never falls back to another node. For an omitted target:

1. Reuse a valid restored target if concrete allocations establish one.
2. Otherwise select the other resource's node when that candidate is feasible.
3. Otherwise select the feasible candidate with the smallest directed
   CPU-to-memory distance `distance[C][M]`.
4. Break equal, known distances by ascending candidate node ID.
5. If relevant distances cannot establish the closest feasible candidate,
   refuse admission with an actionable distance-data error. Never select by ID
   merely because distances are missing.

No distance comparison is needed for a fully specified pair, a feasible common
node (step 2), or a sole feasible candidate. No candidate means a resource or
topology feasibility failure, not a distance-data failure. When ranking multiple
nonlocal candidates, do not silently discard a candidate whose distance is unknown.

Normalize distance data by actual node IDs and the producer's documented column
ordering; validate dimensions and values before lookup. Do not directly index a
compact row by a sparse node ID. Ambiguous/unusable topology data must produce
the specified error rather than a panic or an invented distance. The new policy
loads available distance information without requiring the existing optional
`prefer-closest-numa-nodes` flag; existing policy option behavior remains intact.

## Architecture

Separate the requested constraints from the resolved placement:

- The Pod API stores optional requirements for CPU and memory.
- A request resolver distinguishes ordinary, legacy, and independent placement.
- The new policy produces a resolved placement plan with separate CPU/device
  and memory/hugepage affinities. Retain whether each target was explicit for
  diagnostics; both resolved targets become mandatory during allocation.
- Existing `Policy.Merge()` continues to return one affinity for ordinary paths.
  Add a separate independent-placement operation rather than encoding `{C,M}`
  into a single shared mask, which would allow unintended resource spreading.
- Keep existing `GetAffinity()` semantics for the CPU/device group. Give Memory
  Manager a memory-specific store view/accessor. Without an independent plan,
  that view delegates to existing shared affinity.
- Remove both affinities during cleanup, and replace neither while a Pod has
  valid preserved concrete assignments.

The new policy delegates no-field and legacy behavior to the existing
single-node policy and legacy explicit placement path. Split planning runs only
for Pods using the new fields under the new policy.

CPU Manager restricts allocation and reusable CPUs to C. Memory Manager
restricts allocation/accounting and reusable ordinary/hugepage memory to M.
Neither may broaden the resolved affinity, including when the field was omitted
and the target was selected automatically. Recheck actual capacity during
allocation; hints alone do not reserve resources or establish correctness.

CRI already carries separate `CpusetCpus` and `CpusetMems`; no CRI API extension
is needed. Before container creation, validate concrete CPU and memory
assignments against the resolved plan, then pass the concrete masks to runtime.

## Devices

Coordinate topology-aware device-plugin resources with the CPU group. Retain
existing device hint generation, preference, admission checks, allocation, and
checkpoint behavior. Memory may occupy another node.

This preserves existing device coordination within the CPU group. It deliberately
changes global CPU/memory co-location for new-field Pods and makes no new hard
device-locality guarantee: current device allocation includes preference and
fallback behavior. New DRA locality semantics are outside this extension.

## Admission, rollback, and recovery

API validation covers Pod-only constraints. Kubelet covers policy support,
local topology, feasibility, distance ranking, and concrete assignment consistency.
Use existing status/events and distinguish CPU target, memory target, resource,
and policy failures. A CPU-less CPU target and a missing memory node need
different actionable errors. Distance failures should suggest specifying both
targets or correcting topology data. No new public placement-status API is needed.

Treat admission as a transaction across every participating allocator, even
when only one resource has an explicit API field. Snapshot before mutation,
track newly acquired allocations, and roll back CPU, device, ordinary-memory,
hugepage, and init-reuse changes after any partial failure. Memory snapshots
must cover all affected nodes/resources, not dereference an optional API target
or restore an entire global state snapshot over unrelated allocations. Preserve
valid pre-existing assignments and report cleanup failures. Repeated failed
admissions must not leak reservations.

Preserve explicit and automatically selected targets across container and
kubelet restarts while allocations remain assigned. Reconstruct targets from
concrete CPU assignments and memory blocks; verify each resource's singleton
and agreement across covered containers, allowing already released sequential
init assignments. Conflicting explicit requirements, multi-node restored
assignments, or inconsistent surviving container assignments block affected
admission/start. Do not silently reselect, repair checkpoints, or broaden masks.
Where restoration is partial, surviving assignments constrain selection of the
missing side; normal capacity checks still apply. Do not introduce a new
checkpoint format unless implementation establishes that existing assignments
cannot preserve the required information; raise that finding for design review.

Reject in-place resource resizing and ephemeral-container additions. Preserve
unrelated valid updates. No automatic eviction, live migration, or custom retry
mechanism is introduced.

## Observability and completion

Log the resolved CPU/memory node pair and whether targets were explicit or
automatic using existing structured logging. Global common-node alignment
metrics must count only actual common-node placement; a successful split is an
admission success, not a global alignment success. Do not globally mark the new
policy as guaranteeing common alignment.

Milestone 1 delivers working code, generated artifacts, focused automated
regression coverage, and runnable runtime/STREAM tooling. Milestone 2 records
manual/runtime evidence from the environment the user chooses. VM topology
tests establish behavior exposed by that guest; they do not establish real HBM
performance without actual distinct memory hardware and verified mapping.

The user reports testing the previous feature manually in a laptop VM. Preserve
the older dated execution record as history and record future runs with commands
and outputs. No new tests or benchmark measurements were run during this design.
