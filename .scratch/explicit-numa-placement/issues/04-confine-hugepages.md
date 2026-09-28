# 04: Confine requested hugepages to the selected NUMA node

**What to build:** A participating Pod receives ordinary memory and every requested hugepage size from its requested NUMA node. Admission is refused if that node lacks any required memory resource, regardless of capacity elsewhere.

**Blocked by:** 02 — Enforce CPU and memory placement for a single-container Pod.

**Status:** resolved

**Type:** task

- [x] Enable hugepage requests in otherwise supported participating Pods and remove the corresponding temporary restriction.
- [x] Satisfy each requested hugepage size and ordinary-memory requirement on the selected NUMA node while retaining existing memory accounting semantics.
- [x] Never expand the runtime memory-node mask or allocate requested hugepages on another NUMA node to satisfy a shortage.
- [x] Distinguish insufficient ordinary memory from insufficient capacity for a requested hugepage size in existing failure diagnostics, including the requested NUMA ID.
- [x] Release newly acquired ordinary-memory and hugepage reservations after any failed attempt, without removing pre-existing allocations. Verify normal cleanup as well.
- [x] Test sufficient local capacity, multiple requested page sizes, one insufficient size, spare hugepages on another node, and CPU/memory failure after partial reservation.
- [x] Verify runtime masks and allocated memory-block affinities refer only to the selected node. Preserve omitted-field hugepage behavior.
- [x] Implement the resource behavior independently of ticket 03; when both are present, it must compose with Pod-wide allocation and accounting rather than introduce a separate container-scope placement policy.

## Comments

Approved ticket breakdown: slice 04 of 06. Can proceed independently of ticket 03.

## Answer

Participating Pods can request hugepages. Pod hints and static memory allocation require ordinary memory and each requested hugepage size to fit on the selected NUMA node. Shortage diagnostics identify the resource and node. Init-only page sizes and concurrent sidecar demand are included in pod totals. Existing allocation rollback and cleanup account for both ordinary memory and hugepages.

Automated tests cover local success, multiple page sizes, shortages with spare capacity elsewhere, init and sidecar accounting, rollback, cleanup, omitted-field behavior, and runtime-mask construction. A Linux build of the runtime-mask test passed; execution and physical multi-NUMA runtime checks require the Linux host in ticket 06.
