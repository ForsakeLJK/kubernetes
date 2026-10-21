# 06: Enforce one placement pair across application, init, and sidecar containers

**What to build:** All supported independent placement forms work for Pods containing multiple application containers, sequential init containers, and restartable sidecars, with one CPU node and one memory node shared across the Pod.

**Blocked by:** 05 — Run CPU-only requests with automatic memory placement.

**Status:** resolved

**Type:** task

- [x] Remove the temporary multiple/init/sidecar-container refusals for ordinary-memory workloads.
- [x] Resolve one Pod-wide pair for explicit, CPU-only, and memory-only forms; apply it to every covered container while retaining per-container resource assignments.
- [x] Preserve existing effective-resource calculations, sequential init CPU/memory reuse, and sidecar overlap; do not sum every init container as if it runs simultaneously.
- [x] Validate every container's declarations and enforce the pair through each container's pre-start validation and runtime masks.
- [x] Roll back failures in later containers/providers without leaking earlier acquisitions or deleting valid pre-existing allocations/reuse state.
- [x] Reconstruct matching surviving assignments after restart, permit already released sequential init allocations, and reject inconsistent resource nodes across covered containers.
- [x] Cover device coordination and partial restoration across containers while preserving single-container, legacy, and ordinary behavior.
- [x] Verify successful app/init/sidecar sequences, capacity failures, repeated partial failures, cleanup, and container/kubelet recovery for all new placement forms; record results.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 06 of 11. Hugepage admission remains explicitly unsupported for new-field Pods until ticket 07.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14. Explicit pairs, CPU-only,
and memory-only forms now support multiple applications, sequential init
containers, and restartable sidecars with one CPU/memory node pair. Every
container's current declarations and concrete resource nodes are validated;
cached QoS status cannot bypass eligibility. Separate runtime masks remain
mandatory for every covered container.

Effective resource peaks, init reuse, and ordered sidecar overlap are preserved.
Checkpoint recovery reconstructs targets from surviving app/init/sidecar
assignments, permits released completed inits, and restores missing unfinished
inits using compatible future reservations without changing future assignments
or double-counting memory. Missing applications receive distinct simultaneous
CPU/device allocations. Conflicting sibling resource nodes refuse admission
and start without checkpoint repair.

Repeated failures after provider/container boundaries restore assignments,
reservations, both affinities, and reuse state while preserving restored and
unrelated allocations. Device topology preferences, partial checkpoints, later
plugin failures, successful cleanup, and single-container/legacy/ordinary
regressions are covered. Hugepage admission remains gated for ticket 07.

Focused and complete manager/kubelet race checks, all 11 platform typechecks,
Linux compilation, vet, formatting, and boilerplate pass. Standards and Spec
reviews report zero remaining findings. The full suite reports 162,408 tests,
417 skipped, and 143 failures in 10 groups; every failure name/package matches
ticket 05. Ancillary suites pass. See
[verification-06.md](../verification-06.md) for commands, review fixes, evidence,
and limits. No Linux VM/hardware runtime or performance result is claimed.

## Post-resolution correction (2026-10-19)

A post-milestone Spec review found that common-node alignment was under-counted
for recovered Pods with completed sequential init containers. Recovery skips
their released assignments, so they reported the shared CPU pool. The alignment
check now skips the same completed init containers that admission skips. See
[verification-review-corrections.md](../verification-review-corrections.md).
