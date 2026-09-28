# 05: Preserve placement through container and kubelet restarts

**What to build:** Participating Pods retain their NUMA placement requirement through container and kubelet restarts. Valid restored CPU and memory assignments are reused; conflicting assignments block affected admission or starts/restarts with actionable diagnostics.

**Blocked by:** 03 — Extend placement to application containers, init containers, and sidecars; 04 — Confine requested hugepages to the selected NUMA node.

**Status:** ready-for-agent

**Type:** task

- [ ] Remove the temporary restored-assignment restriction after implementing validation against the immutable Pod requirement.
- [ ] Reuse valid restored CPU, ordinary-memory, and hugepage assignments without duplicate reservation or unintended movement. Validate concrete assignments, not only in-memory topology hints.
- [ ] Container starts/restarts receive CPUs belonging to the requested node and a memory-node mask containing exactly that node after recovery.
- [ ] If any restored assignment conflicts with the requirement, block new admission or affected starts/restarts and identify the requested node and conflict through existing status/events.
- [ ] Do not silently relocate resources, broaden masks, repair checkpoints, or introduce automatic eviction of already running containers. Preserve existing handling of corrupt manager checkpoints.
- [ ] Failed recovery attempts release only newly acquired allocations and preserve valid pre-existing assignments.
- [ ] Test container restart and checkpoint restoration for application containers, init containers, sidecars, ordinary memory, and hugepages, including matching and conflicting assignments and repeated recovery attempts.
- [ ] Test composition of tickets 03 and 04, including multi-container hugepage placement and cleanup around recovery failures.
- [ ] Verify all temporary implementation-not-yet-supported refusals introduced by earlier slices have been removed for workloads supported by the final spec; final exclusions remain enforced.
- [ ] Preserve omitted-field recovery behavior and immutability/resize/debugging restrictions.

## Comments

Approved ticket breakdown: slice 05 of 06. Both Pod-wide and hugepage support are prerequisites so recovery covers the complete supported resource model.
