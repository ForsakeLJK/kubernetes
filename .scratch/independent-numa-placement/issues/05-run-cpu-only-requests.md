# 05: Run CPU-only requests with automatic memory placement

**What to build:** A single-application-container Pod can specify cpuNUMA alone and receive ordinary memory from one feasible automatically selected node, with strict confinement and stable placement across restarts.

**Blocked by:** 04 — Run memory-only requests with automatic CPU placement.

**Status:** resolved

**Type:** task

- [x] Remove the temporary CPU-only refusal. Keep the specified CPU node and CPU/device coordination mandatory.
- [x] Reuse the validated ranking behavior for memory candidates: restored target first, feasible common node next, then directed CPU-to-memory distance and known-distance ID tie-breaking.
- [x] Permit automatically selected memory-only nodes. Require sufficient memory on one node; never expand the memory mask or aggregate capacity across nodes.
- [x] Apply strict missing-distance refusal and its no-ranking-needed exceptions in the memory-selection direction. Include asymmetric distances so an accidental transpose fails tests.
- [x] Make memory allocation, reuse, accounting, pre-start validation, and runtime masks enforce the resolved automatic target as strongly as an explicit target.
- [x] Roll back both managers and preserve unrelated/restored assignments without dereferencing an omitted memory API field.
- [x] Restore and reuse matching automatic memory placement; newly available nearer nodes do not authorize relocation. Handle partial recovery with surviving assignments constraining selection.
- [x] Verify CPU-only, memory-only, explicit-pair, legacy, and ordinary behavior through masks and lifecycle/failure paths; record focused results.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 05 of 11. Ticket 04 is a real prerequisite because it delivers the shared distance decoder, candidate ranking, and strict failure semantics.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14 in `89624562382`. One
application container can specify `cpuNUMA` alone. Automatic ordinary memory
selection preserves surviving assignments, then chooses a feasible common node,
then the closest directed CPU-to-memory pair with ascending-ID ties. A memory
node may have no CPUs. Unknown or malformed relevant distances refuse ambiguous
ranking; explicit/restored pairs, common targets, and sole feasible candidates
need no comparison. Requests that fit only across multiple memory nodes refuse.

Both resolved targets are mandatory during allocation and pre-start validation.
Checkpoint recovery preserves automatic memory despite changed locality or
missing distances, including partial restoration. CPU/device coordination,
separate runtime masks, repeated rollback, restored/unrelated assignments, and
normal cleanup are covered. CPU-only, memory-only, explicit-pair, legacy, and
ordinary behavior pass focused regression checks. Later-ticket multi-container,
init/sidecar, and hugepage forms remain refused.

Focused manager/device/runtime-mask race tests, the complete kubelet race suite,
all 11 platform typechecks, Linux compilation, vet, formatting, and boilerplate
pass. Standards and Spec reviews report zero findings. The main full suite has
145 failures across 11 package groups; all failure names/packages appear in
previous ticket records. Ancillary suites pass. See
[verification-05.md](../verification-05.md) for commands, evidence, and limits.
No Linux VM/hardware runtime or performance result is claimed.
