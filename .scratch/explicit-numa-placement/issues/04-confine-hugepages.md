# 04: Confine requested hugepages to the selected NUMA node

**What to build:** A participating Pod receives ordinary memory and every requested hugepage size from its requested NUMA node. Admission is refused if that node lacks any required memory resource, regardless of capacity elsewhere.

**Blocked by:** 02 — Enforce CPU and memory placement for a single-container Pod.

**Status:** ready-for-agent

**Type:** task

- [ ] Enable hugepage requests in otherwise supported participating Pods and remove the corresponding temporary restriction.
- [ ] Satisfy each requested hugepage size and ordinary-memory requirement on the selected NUMA node while retaining existing memory accounting semantics.
- [ ] Never expand the runtime memory-node mask or allocate requested hugepages on another NUMA node to satisfy a shortage.
- [ ] Distinguish insufficient ordinary memory from insufficient capacity for a requested hugepage size in existing failure diagnostics, including the requested NUMA ID.
- [ ] Release newly acquired ordinary-memory and hugepage reservations after any failed attempt, without removing pre-existing allocations. Verify normal cleanup as well.
- [ ] Test sufficient local capacity, multiple requested page sizes, one insufficient size, spare hugepages on another node, and CPU/memory failure after partial reservation.
- [ ] Verify runtime masks and allocated memory-block affinities refer only to the selected node. Preserve omitted-field hugepage behavior.
- [ ] Implement the resource behavior independently of ticket 03; when both are present, it must compose with Pod-wide allocation and accounting rather than introduce a separate container-scope placement policy.

## Comments

Approved ticket breakdown: slice 04 of 06. Can proceed independently of ticket 03.
