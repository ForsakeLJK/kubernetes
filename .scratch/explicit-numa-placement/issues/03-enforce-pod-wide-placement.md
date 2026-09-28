# 03: Extend placement to application containers, init containers, and sidecars

**What to build:** All application containers, init containers, and sidecars in a participating Pod share the requested NUMA node, with correct resource accounting and complete cleanup when admission fails partway through the Pod.

**Blocked by:** 02 — Enforce CPU and memory placement for a single-container Pod.

**Status:** resolved

**Type:** task

- [x] Enable Pod-wide placement for multiple application containers, ordinary init containers, and restartable init containers/sidecars; remove the corresponding temporary restrictions.
- [x] Every covered container receives CPUs belonging to the requested node and a memory-node mask containing exactly that node.
- [x] Preserve existing resource accounting and reuse for sequential init containers, concurrent application containers, and overlapping sidecars. Do not sum sequential init-container requests as though they all run concurrently.
- [x] Refuse a Pod when its required concurrent resources cannot fit on the requested node, even when its containers could individually fit or another node has spare resources.
- [x] On failure in a later container or resource manager, release all allocations newly acquired by that attempt across the Pod. Preserve valid pre-existing assignments and prohibit partial admission from starting containers outside the requirement.
- [x] Test successful multi-container placement, sequential init resource reuse, sidecar overlap, insufficient combined capacity, and failures injected at successive allocation stages.
- [x] Test the runtime configuration produced for every covered container and resource release after normal Pod cleanup.
- [x] Retain hugepage and restored-assignment restrictions only until their respective supporting tickets land. This ticket must be independently implementable without ticket 04.
- [x] Preserve existing omitted-field behavior for multi-container Pods, init containers, sidecars, and resource reuse.

## Comments

Approved ticket breakdown: slice 03 of 06. Can proceed independently of ticket 04.

## Answer

Pod-scope admission now handles application containers, ordinary init containers, and restartable init containers together. Explicit CPU placement prefers reusable init CPUs on the requested node. Memory feasibility includes reusable init memory. Failure at any provider or later container rolls back new CPU, memory, and device allocations across the Pod while preserving pre-existing assignments. Hugepage and restored-assignment restrictions remain for tickets 04 and 05.

Synthetic topology tests cover concurrent applications, sequential init reuse, sidecar overlap, combined CPU and memory shortages, rollback at successive container stages, normal cleanup, and omitted-field behavior. Runtime configuration tests cover every container kind. Focused tests and the complete CPU Manager, Memory Manager, Topology Manager, and container-manager package suites pass. Live runtime mask checks require a Linux multi-NUMA host and were not run on this macOS machine.
