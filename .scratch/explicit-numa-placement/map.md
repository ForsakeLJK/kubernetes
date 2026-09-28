# Explicit NUMA placement design

## Notes

Status: design questions settled; [spec](spec.md) drafted for final shared-understanding review. Implementation not started.

The request comes from the user's research work. No concrete motivating workload has been identified. Implementation has not been authorized by completion of the design interview.

## Decisions-so-far

- [Ticket 01](issues/01-validate-numa-placement.md) introduced the immutable Pod NUMA requirement and temporary kubelet refusal path. Allocation and Linux runtime checks remain in later slices.

- A Pod can express a hard NUMA placement requirement. Refuse admission when it cannot be satisfied; do not fall back to another NUMA node (round 1, Q2).
- The requirement covers CPU and memory placement together (round 1, Q3).
- Target a private research fork. Upstream acceptance is not currently a requirement (round 1, Q4).
- Canonical terminology is recorded in `../../CONTEXT.md`.
- Supported starting configuration: Linux, Guaranteed Pods with whole-number CPU requests, CPU Manager `static`, Memory Manager `Static`, and Topology Manager `single-numa-node` with `pod` scope. Reject participating Pods under unsupported configurations (round 2, Q5).
- Enforcement is kubelet-only. NUMA IDs are local to the selected machine. Machine selection is arranged separately; unsuitable assignments are rejected without a promise of automatic relocation (round 2, Q6).
- Device handling remains subject to existing topology policies. No new device-locality guarantee; existing policies may reject a Pod. CPU and memory requirements remain mandatory (round 2, Q7).
- One NUMA ID applies to every application container, init container, and sidecar (round 2, Q8).
- Use an optional integer PodSpec field; provisional name `spec.numaNode`. Omission preserves ordinary behavior and zero explicitly selects NUMA 0. Reject negative values during API validation and nonexistent local IDs at kubelet admission (round 3, Q9).
- Placement is immutable after creation. Changing the requested ID requires Pod replacement; container restarts retain the requirement (round 3, Q10).
- Pods omitting the field keep existing behavior, including on kubelets outside the supported configuration (round 3, Q11).
- Guarantee CPU affinity and memory-allocation confinement, not physical locality of every shared or previously allocated page (round 4, Q12).
- Each application container, init container, and sidecar must explicitly specify equal CPU/memory requests and limits; CPU quantities must be positive whole numbers. Reject participating Pods using Pod-level resource budgets (`spec.resources`) (round 4, Q13).
- Ordinary memory and every requested hugepage size must fit on the requested NUMA node; no fallback to another node (round 4, Q14).
- Reject ephemeral-container additions to participating Pods (round 4, Q15).
- Reject in-place CPU/memory resizing for participating Pods, regardless of other resize feature gates. Replace the Pod to change resource requirements; container restarts remain supported (round 4, Q16).
- No new feature gate in this private fork; field presence opts in, supported configuration is enforced at admission (round 4, Q17).
- Validate IDs in the range 0–63 and separately check membership in local topology (round 5, Q18).
- Reuse valid restored assignments; block admission or affected starts/restarts on incompatible assignments, with no automatic relocation or checkpoint repair (round 5, Q19).
- Use existing status/events with specific failure diagnostics. Failed attempts release newly acquired allocations without leaking reservations; no custom retries or rescheduling (round 5, Q20).
- Acceptance includes automated validation/allocation coverage and Linux runtime checks on a host with at least two NUMA nodes, including resource shortages, lifecycle, unsupported operations, omitted-field compatibility, and allocation cleanup (round 5, Q21).

## Fog

- No unresolved design questions from the interview. Final shared-understanding review of the drafted spec remains.

## Code grounding

Initial inspection at Kubernetes commit `68f9fc030c6`:

- Topology Manager coordinates resource-manager hints and admission; a hint alone is not a hard allocation boundary.
- CPU allocation may use CPUs outside the aligned mask (`pkg/kubelet/cm/cpumanager/policy_static.go`).
- Static Memory Manager may expand an insufficient NUMA mask (`pkg/kubelet/cm/memorymanager/policy_static.go`).
- Existing policies and scopes differ; eligibility restrictions and Pod-level resource-manager paths require explicit scoping.
- Linux container creation passes CPU and memory masks through CRI (`pkg/kubelet/cm/internal_container_lifecycle_linux.go`).
- Topology masks currently represent IDs 0 through 63 (`pkg/kubelet/cm/topologymanager/bitmask/bitmask.go`); NUMA IDs need not be contiguous.
- CPU and memory managers restore concrete assignments from checkpoints. Recovery must check compatibility with the Pod's requirement.
