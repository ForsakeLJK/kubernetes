# Independent CPU and memory NUMA placement design

Status: design and ticket breakdown approved on 2026-10-14; 11 tickets published. Ticket 01 is resolved; the remaining software/tooling tickets are not implemented.

Review: [specification](spec.md), [implementation plan](implementation-plan.md),
and [validation plan](validation-plan.md).

## Notes

Extend the private Kubernetes research fork for systems where DDR and HBM may
appear as separate NUMA nodes, including memory-only nodes. Preserve the existing
`spec.numaNode` feature and add STREAM evaluation.

The prior feature contract is in `../explicit-numa-placement/spec.md`. Its
single shared CPU/memory target and required single-numa-node alignment conflict
with the new split-target use case and need an explicit design revision.

## Decisions-so-far

- Ticket 01 resolved on 2026-10-14: separate internal targets, optional memory
  affinity, and rollback/cleanup preparation preserve legacy behavior. Focused
  tests and cross-platform checks pass; repository-wide failures and baseline
  audits are recorded in [verification-01.md](verification-01.md). See the
  [ticket answer](issues/01-prepare-placement-enforcement.md#answer).

- Q1: Allow `cpuNUMA` alone, `memNUMA` alone, or both together. The user chose
  the interpretation demonstrated by the supervisor's example. The supervisor's
  contradictory "not both" wording remains an external clarification item.
- Q2: An omitted new field imposes no explicit NUMA requirement for that resource.
  The system chooses a feasible placement; it must not implicitly copy the other
  resource's explicit ID. For example, memory-only node 3 must not require CPUs
  from node 3.
- Q3: Preserve `numaNode` as the existing combined CPU/memory requirement. Reject
  any combination of `numaNode` with either new field, even if IDs agree.
- Q4: Keep the existing Guaranteed QoS and positive whole-number CPU requirements
  for all new placement forms, including memory-only requests. Every covered
  container declares equal CPU and memory requests and limits.
- Q5: Each resource must fit within one NUMA node, including an automatically
  placed resource. CPU and memory may select different nodes. Refuse admission
  if the automatically placed resource only fits by spanning multiple nodes.
- Q6: STREAM acceptance requires placement evidence and controlled DDR/HBM
  bandwidth measurements without a predetermined speedup threshold. Hold CPU
  allocation, thread count, executable, and array sizes constant between paired
  runs; inspect actual benchmark-page residency as well as allowed masks.
- Q7: When a target is omitted, prefer the same node as the other resource when
  feasible, then the closest feasible node according to reported NUMA distances.
  This is a locality preference, not a performance guarantee.
- Q8: `memNUMA` confines ordinary memory and every requested hugepage size to the
  selected memory node. Refuse admission if any requested memory resource lacks
  capacity there, even if another node has spare capacity.
- Q9: Extend the existing lifecycle contract to the new fields: all application,
  init, and sidecar containers; immutable placement fields; preserved and
  validated placement across restarts; no in-place CPU/memory resize, ephemeral
  container additions, or Pod-level resource budgets.
- Q10: Introduce a new named kubelet topology policy that administrators enable.
  Do not implement the new fields as an exception under `single-numa-node`.
- Q11: Coordinate device topology with CPU placement; memory may use a separate
  node. Preserve existing device checks and allocation behavior, without a new
  hard device-locality guarantee.
- Q12: Separate software readiness (including automated tests and runnable
  runtime/STREAM tooling) from later runtime validation. The user may run runtime
  checks manually in a VM or obtain hardware from their team. On 2026-10-13 the
  user reports manually testing the previous feature in a laptop VM; no commands
  or output were supplied in this conversation. This supplements the old record,
  which only described the earlier agent execution environment. VM placement
  checks do not by themselves establish real DDR/HBM bandwidth performance.
- Q13: With the new policy, Pods omitting all placement fields follow the existing
  single-numa-node behavior; they do not opt into independent placement.
- Q14: Accept legacy `numaNode` under both single-numa-node and the new policy,
  preserving its combined hard requirement. New fields require the new policy.
- Q15: Refuse an automatic placement when the closest feasible node cannot be
  determined because relevant NUMA distances are unavailable or unusable. Do not
  fall back to a node-ID choice in this situation. Fully explicit pairs require
  feasibility checks, not distance ranking.
- Carry forward the existing feature's hard requirements, Pod-wide scope, Linux
  support, and unchanged scheduler. The detailed specification was approved on 2026-10-14.

## Design tree

1. API forms and omission semantics: settled in Q1-Q3.
2. Supported workloads: settled in Q4. Per-resource single-node placement: Q5.
   New named kubelet topology policy: Q10; compatibility: Q13-Q14.
   - Independent resource decisions and device-policy interaction.
   - Automatic placement when one target is omitted.
3. Enforcement and lifecycle.
   - Topology validation, capacity accounting, runtime masks.
   - Partial-failure cleanup and restored assignments.
4. Acceptance and STREAM evaluation.
   - Correctness criteria, benchmark comparisons, runtime evidence.
5. Specification and 11-ticket breakdown approved; implementation follows the published dependency frontier.

## Published tickets

The user approved the design and the following breakdown on 2026-10-14.
These tickets supersede the earlier eight-step work sequence; the implementation
plan remains architectural guidance and the specification remains the contract.
No parent issue was closed or modified.

| Ticket | Blocked by | Initial status |
| --- | --- | --- |
| [01: Prepare existing placement enforcement for separate resource targets](issues/01-prepare-placement-enforcement.md) | None | ready-for-agent |
| [02: Enable independent-numa policy for existing Pod forms](issues/02-enable-independent-policy.md) | 01 | ready-for-agent |
| [03: Run one application container with explicit CPU and memory targets](issues/03-run-explicit-cpu-memory-pairs.md) | 02 | ready-for-agent |
| [04: Run memory-only requests with automatic CPU placement](issues/04-run-memory-only-requests.md) | 03 | ready-for-agent |
| [05: Run CPU-only requests with automatic memory placement](issues/05-run-cpu-only-requests.md) | 04 | ready-for-agent |
| [06: Enforce one placement pair across application, init, and sidecar containers](issues/06-enforce-pod-wide-placement.md) | 05 | ready-for-agent |
| [07: Confine ordinary memory and requested hugepages to the memory target](issues/07-confine-hugepages.md) | 06 | ready-for-agent |
| [08: Package a reproducible STREAM workload with residency evidence](issues/08-package-stream-workload.md) | None | ready-for-agent |
| [09: Deliver a runnable VM and hardware placement-validation kit](issues/09-deliver-runtime-validation-kit.md) | 07 | ready-for-agent |
| [10: Automate paired STREAM evaluation and evidence reporting](issues/10-automate-paired-stream-evaluation.md) | 08, 09 | ready-for-agent |
| [11: Record user-operated VM or hardware acceptance evidence](issues/11-record-manual-runtime-evidence.md) | 10 | ready-for-agent |

Current frontier after ticket 01: **02 and 08**.

Initial frontier: **01 and 08**. A ready-for-agent status means the ticket is
prepared, not that its dependencies are resolved. Claim only a ticket whose
listed blockers are resolved; update its status to claimed before work and
record verification evidence when resolving it.

Tickets 01–10 deliver the software/tooling milestone. Ticket 11 is an
operator-assisted follow-up requiring a chosen VM/host or user-provided outputs;
it does not block declaring the software/tooling milestone ready.

## Code grounding

- cAdvisor discovers memory-only NUMA nodes; Memory Manager initializes state
  from machine topology. CPU topology intentionally lists only CPU-bearing nodes.
- Current Topology Manager placement uses one shared affinity; ordinary hint
  merging intersects affinities. Separate CPU and memory masks require a change
  to coordination, not just new API fields.
- Runtime configuration already has separate `CpusetCpus` and `CpusetMems` fields.
- Allocation rollback must cover both managers even if only one resource has an
  explicit target; current memory snapshots assume one explicit memory node.
- The existing runtime checker assumes one shared node and needs independent
  CPU and memory expectations.
- A resource-specific memory affinity store can separate memory/hugepage hints
  from CPU/device hints; existing `GetAffinity` is consumed by Device Manager.
- Device allocation retains existing preference/fallback behavior. CPU-anchored
  device hint coordination would not establish a new hard device-locality guarantee.
- Global NUMA-alignment success metrics/logs must not classify split CPU/memory
  targets as common-node alignment.
- The prior runtime execution record dated 2026-09-29 reports no available Linux
  multi-NUMA host. It is historical evidence, not a current hardware inventory.

## Fog

- No unresolved design or ticket-breakdown approval. The user approved the policy name `independent-numa`, specification, and published breakdown.
- Runtime execution requires the user-selected environment; ticket 11 is a separate milestone.
- Supervisor confirmation of the contradictory sentence is an external follow-up;
  the user selected support for both fields together in Q1.
- Runtime validation may be manual in a VM or on team-provided hardware, with
  claims limited to the environment actually used. No new runtime results are claimed.
