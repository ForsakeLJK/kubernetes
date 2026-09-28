# 05: Preserve placement through container and kubelet restarts

**What to build:** Participating Pods retain their NUMA placement requirement through container and kubelet restarts. Valid restored CPU and memory assignments are reused; conflicting assignments block affected admission or starts/restarts with actionable diagnostics.

**Blocked by:** 03 — Extend placement to application containers, init containers, and sidecars; 04 — Confine requested hugepages to the selected NUMA node.

**Status:** resolved

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

## Answer

Recovered CPU sets and ordinary-memory and hugepage blocks are checked against the Pod's immutable requested node and resource declarations before reuse or container creation. Pod admission disregards resource-manager capacity hints that include already checkpointed reservations; concrete validation and allocation still enforce placement, while device hints retain their existing decision. Failed admissions restore the prior assignments and release only newly acquired resources. The temporary restored-assignment refusals are removed; resize remains refused.

Automated verification: the container-manager test tree passed with a short temporary path. On the final run, the unrelated `TestDevicePluginReRegistrationProbeMode` failed in isolation on this macOS host; the tree passed with that test excluded. Checkpoint reopen, assignment reuse, restart validation, conflicting assignments, hugepages, init containers, sidecars, and repeated failed recovery are covered. The Linux runtime-mask test compiled with `GOOS=linux GOARCH=arm64 go test -c ./pkg/kubelet/cm`; it could not execute on this macOS host. Linux multi-NUMA runtime acceptance remains for ticket 06.
